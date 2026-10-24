package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Login with OpenID Connect. The studio is the client: a page without a
// session goes to the provider (authorization code with PKCE), the callback
// checks the ID token (signature, issuer, audience, expiry, nonce) and starts a
// session of the studio's own. A user is the provider's issuer and sub, and
// nothing else: their name and email are kept for display, never to give
// access. Who may use which church is the church's members (members.go).

// OIDCOptions turn on the login; see AppConfig.OIDC.
type OIDCOptions struct {
	Issuer       string // the provider, as in its discovery document
	ClientID     string
	ClientSecret string // "" = a public client (PKCE only)
	// PublicURL is the studio's address as the browser sees it (with a
	// trailing /); the callback is PublicURL + "auth/callback".
	PublicURL string
	// CookieDomain sets the session cookie's Domain (a parent domain the
	// studio's hosts share); "" = this host only.
	CookieDomain string
}

const (
	sessionCookie = "studio_session"
	loginCookie   = "studio_login"
	logoutCookie  = "studio_logout"
	sessionFor    = 30 * 24 * time.Hour
	loginFor      = 10 * time.Minute
	usersFile     = "users.json"
	sessionsFile  = "sessions.json"
)

// userID is who a user is: their provider and their id there.
type userID struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"sub"`
}

// userRecord is a user as kept in users.json.
type userRecord struct {
	userID
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

type session struct {
	User    userID    `json:"user"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	// IDToken is the provider's ID token from the login, sent back to it as
	// id_token_hint when logging out there. Kept here, never in a cookie.
	IDToken string `json:"idToken,omitempty"`
}

// auth is the login's state: the provider (found on first use, so the studio
// starts while the provider is down), sessions and users, both kept in the
// data dir so a restart logs nobody out.
type auth struct {
	opt     OIDCOptions
	dir     string
	base    *url.URL // PublicURL
	secure  bool     // cookies only over HTTPS
	scopes  []string
	client  *http.Client
	log     *log.Logger
	mu      sync.Mutex
	prov    *oidc.Provider
	users   map[string]*userRecord // by key()
	session map[string]*session    // by the hash of the cookie's value
}

func (id userID) key() string { return id.Issuer + " " + id.Subject }

func newAuth(opt OIDCOptions, dataDir string, logger *log.Logger) (*auth, error) {
	if dataDir == "" {
		return nil, errors.New("the login needs --data-dir (users and members are kept there)")
	}
	if opt.Issuer == "" || opt.ClientID == "" || opt.PublicURL == "" {
		return nil, errors.New("the login needs the issuer, the client id and the public URL")
	}
	base, err := url.Parse(opt.PublicURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, fmt.Errorf("public URL %q: give the whole address, like https://studio.example.org/", opt.PublicURL)
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	au := &auth{opt: opt, dir: dataDir, base: base, secure: base.Scheme == "https",
		scopes: []string{oidc.ScopeOpenID, "profile", "email"}, client: &http.Client{Timeout: 15 * time.Second}, log: logger,
		users: map[string]*userRecord{}, session: map[string]*session{}}
	var users []*userRecord
	if err := readJSONFile(filepath.Join(dataDir, usersFile), &users); err != nil {
		return nil, err
	}
	for _, u := range users {
		au.users[u.key()] = u
	}
	if err := readJSONFile(filepath.Join(dataDir, sessionsFile), &au.session); err != nil {
		return nil, err
	}
	return au, nil
}

// provider finds the provider's endpoints and keys the first time it is needed.
func (au *auth) provider(ctx context.Context) (*oidc.Provider, error) {
	au.mu.Lock()
	defer au.mu.Unlock()
	if au.prov != nil {
		return au.prov, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, au.client), au.opt.Issuer)
	if err != nil {
		return nil, err
	}
	au.prov = p
	return p, nil
}

func (au *auth) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: au.opt.ClientID, ClientSecret: au.opt.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: au.url("auth/callback"), Scopes: au.scopes}
}

// url is a path of the studio as an absolute address.
func (au *auth) url(path string) string {
	return au.base.ResolveReference(&url.URL{Path: path}).String()
}

func (au *auth) cookie(name, value string, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{Name: name, Value: value, Path: au.base.Path, HttpOnly: true, Secure: au.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(maxAge.Seconds())}
	if name == sessionCookie {
		c.Domain = au.opt.CookieDomain
	}
	if maxAge < 0 {
		c.MaxAge = -1
	}
	return c
}

func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// sessionOf is the user of the request's session cookie.
func (au *auth) sessionOf(r *http.Request) (userID, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return userID{}, false
	}
	au.mu.Lock()
	defer au.mu.Unlock()
	s := au.session[hashToken(c.Value)]
	if s == nil || time.Now().After(s.Expires) {
		return userID{}, false
	}
	return s.User, true
}

// user is a user's name (their name, else their id) and email, for showing.
func (au *auth) user(id userID) (name, email string) {
	au.mu.Lock()
	defer au.mu.Unlock()
	if u := au.users[id.key()]; u != nil {
		return cmpOr(u.Name, cmpOr(u.Email, id.Subject)), u.Email
	}
	return id.Subject, ""
}

// userName is the name to show for a user: their name, else their email,
// else their id.
func (au *auth) userName(id userID) string {
	au.mu.Lock()
	defer au.mu.Unlock()
	if u := au.users[id.key()]; u != nil {
		if u.Name != "" {
			return u.Name
		}
		if u.Email != "" {
			return u.Email
		}
	}
	return id.Subject
}

// saveLocked writes users.json and sessions.json (without the expired ones);
// call with au.mu held.
func (au *auth) saveLocked() error {
	now := time.Now()
	for k, s := range au.session {
		if now.After(s.Expires) {
			delete(au.session, k)
		}
	}
	users := make([]*userRecord, 0, len(au.users))
	for _, u := range au.users {
		users = append(users, u)
	}
	if err := writeJSONFile(filepath.Join(au.dir, usersFile), users); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(au.dir, sessionsFile), au.session)
}

// loginState is what the login cookie carries from /auth/login to the
// callback.
type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"next"`
}

// safeNext is where to go after the login: a path of this studio, else its start.
func (au *auth) safeNext(next string) string {
	bad := strings.ContainsFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' })
	if bad || !strings.HasPrefix(next, au.base.Path) || strings.HasPrefix(next, "//") {
		return au.base.Path
	}
	return next
}

// handleLogin sends the browser to the provider: GET auth/login?next=<path>.
func (au *auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	p, err := au.provider(r.Context())
	if err != nil {
		au.fail(w, r, fmt.Errorf("the login service cannot be reached: %w", err))
		return
	}
	st := loginState{State: randomToken(), Nonce: randomToken(), Verifier: oauth2.GenerateVerifier(), Next: au.safeNext(r.URL.Query().Get("next"))}
	b, _ := json.Marshal(st)
	http.SetCookie(w, au.cookie(loginCookie, base64.RawURLEncoding.EncodeToString(b), loginFor))
	http.Redirect(w, r, au.oauth(p).AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

// handleCallback is where the provider sends the browser back: GET auth/callback?code=&state=.
func (au *auth) handleCallback(w http.ResponseWriter, r *http.Request) {
	var st loginState
	c, err := r.Cookie(loginCookie)
	if err == nil {
		var b []byte
		if b, err = base64.RawURLEncoding.DecodeString(c.Value); err == nil {
			err = json.Unmarshal(b, &st)
		}
	}
	http.SetCookie(w, au.cookie(loginCookie, "", -1))
	q := r.URL.Query()
	switch {
	case err != nil || st.State == "":
		au.fail(w, r, errors.New("the login was not started here, or took too long"))
		return
	case q.Get("error") != "":
		au.fail(w, r, fmt.Errorf("the login service said: %s %s", q.Get("error"), q.Get("error_description")))
		return
	case q.Get("state") != st.State:
		au.fail(w, r, errors.New("the login's state does not match"))
		return
	}
	p, err := au.provider(r.Context())
	if err != nil {
		au.fail(w, r, fmt.Errorf("the login service cannot be reached: %w", err))
		return
	}
	ctx := oidc.ClientContext(r.Context(), au.client)
	tok, err := au.oauth(p).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		au.fail(w, r, fmt.Errorf("the code was not accepted: %w", err))
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		au.fail(w, r, errors.New("the login service sent no ID token"))
		return
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: au.opt.ClientID}).Verify(ctx, raw)
	if err != nil {
		au.fail(w, r, fmt.Errorf("the ID token is not valid: %w", err))
		return
	}
	if idt.Nonce != st.Nonce {
		au.fail(w, r, errors.New("the ID token's nonce does not match"))
		return
	}
	var claims struct {
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
		Email    string `json:"email"`
	}
	idt.Claims(&claims)
	id := userID{Issuer: idt.Issuer, Subject: idt.Subject}
	value := randomToken()
	now := time.Now().UTC()
	au.mu.Lock()
	u := au.users[id.key()]
	if u == nil {
		u = &userRecord{userID: id, FirstSeen: now}
		au.users[id.key()] = u
	}
	u.Name, u.Email, u.LastSeen = cmpOr(claims.Name, claims.Username), claims.Email, now
	au.session[hashToken(value)] = &session{User: id, Created: now, Expires: now.Add(sessionFor), IDToken: raw}
	err = au.saveLocked()
	au.mu.Unlock()
	if err != nil {
		au.fail(w, r, err)
		return
	}
	http.SetCookie(w, au.cookie(sessionCookie, value, sessionFor))
	http.Redirect(w, r, st.Next, http.StatusFound)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// handleLogout ends the session: POST auth/logout. The studio's session ends
// first; then, when the provider has an end_session_endpoint (OpenID Connect
// RP-Initiated Logout), the browser goes there to end the login at the
// provider too, which sends it back to auth/logout/callback. Without one, or
// when the provider cannot be reached, it goes straight to loggedout.html.
func (au *auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie(sessionCookie); err == nil {
		au.mu.Lock()
		if s := au.session[hashToken(c.Value)]; s != nil {
			idToken = s.IDToken
			delete(au.session, hashToken(c.Value))
			au.saveLocked()
		}
		au.mu.Unlock()
	}
	http.SetCookie(w, au.cookie(sessionCookie, "", -1))
	state := randomToken()
	if end := au.endSession(r.Context(), idToken, state); end != "" {
		http.SetCookie(w, au.cookie(logoutCookie, state, loginFor))
		http.Redirect(w, r, end, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, au.url("loggedout.html"), http.StatusSeeOther)
}

// endSession is the provider's address that ends the login there, or "" when
// there is none: no ID token to send (a session from before they were kept),
// no end_session_endpoint, or the provider cannot be reached.
func (au *auth) endSession(ctx context.Context, idToken, state string) string {
	if idToken == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := au.provider(ctx)
	if err != nil {
		au.log.Printf("login: logging out at the provider: %v", err)
		return ""
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := p.Claims(&meta); err != nil || meta.EndSession == "" {
		return ""
	}
	u, err := url.Parse(meta.EndSession)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	q := u.Query()
	q.Set("id_token_hint", idToken)
	q.Set("client_id", au.opt.ClientID)
	q.Set("post_logout_redirect_uri", au.url("auth/logout/callback"))
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

// handleLogoutCallback is where the provider sends the browser after logging
// out there: GET auth/logout/callback?state=. The studio's session is already
// gone, so a missing or wrong state is only logged.
func (au *auth) handleLogoutCallback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(logoutCookie)
	http.SetCookie(w, au.cookie(logoutCookie, "", -1))
	if err != nil || c.Value == "" || r.URL.Query().Get("state") != c.Value {
		au.log.Printf("login: the logout came back without its state")
	}
	http.Redirect(w, r, au.url("loggedout.html"), http.StatusSeeOther)
}

// fail logs why a login did not work and shows the page that says so.
func (au *auth) fail(w http.ResponseWriter, r *http.Request, err error) {
	au.log.Printf("login: %v", err)
	http.Redirect(w, r, au.url("loggedout.html")+"?failed", http.StatusSeeOther)
}

// publicPath reports the paths anyone may fetch without a session: the login
// itself, the health check, and the static files but the studio's page (no
// studio data is in them).
func publicPath(rel string) bool {
	switch {
	case strings.HasPrefix(rel, "auth/"), rel == "healthz":
		return true
	case rel == "" || rel == "index.html" || strings.HasPrefix(rel, "api/"):
		return false
	}
	return true
}

// require lets only requests with a session through, apart from publicPath:
// a page goes to the login and comes back, the API answers 401.
func (au *auth) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel, ok := strings.CutPrefix(r.URL.Path, "/")
		if !ok || publicPath(rel) {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := au.sessionOf(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(rel, "api/") {
			// login: the page should go to the login (util.js toLogin).
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "log in first", "login": true})
			return
		}
		http.Redirect(w, r, au.url("auth/login")+"?next="+url.QueryEscape(au.base.Path+rel), http.StatusFound)
	})
}

// readJSONFile reads path into v; a file that is not there leaves v as it is.
func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		err = json.Unmarshal(b, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// writeJSONFile writes v to path atomically, readable by its owner only.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return normalizer.Rename(tmp, path)
}
