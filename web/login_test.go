package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProvider is an OpenID Connect provider for the tests: discovery, keys,
// and a token endpoint that signs whatever ID token the test asks for.
type fakeProvider struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	sub      string
	nonce    string               // from the authorize URL
	verifier string               // the code_verifier the token request must match
	change   func(map[string]any) // tamper with the claims
	signWith *rsa.PrivateKey      // nil = key
	logout   bool                 // has an end_session_endpoint
	idToken  string               // the last ID token it handed out
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{key: key, sub: "user-1"}
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		meta := map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		}
		if p.logout {
			meta["end_session_endpoint"] = p.srv.URL + "/end-session?lang=en"
		}
		json.NewEncoder(w).Encode(meta)
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != p.verifier {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims := map[string]any{"iss": p.srv.URL, "sub": p.sub, "aud": "studio", "exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Unix(), "nonce": p.nonce, "name": "Kari Nordmann", "email": "kari@example.org"}
		if p.change != nil {
			p.change(claims)
		}
		p.idToken = p.sign(t, claims)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600,
			"id_token": p.idToken})
	})
	return p
}

func (p *fakeProvider) sign(t *testing.T, claims map[string]any) string {
	b64 := base64.RawURLEncoding.EncodeToString
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	msg := b64(head) + "." + b64(body)
	sum := sha256.Sum256([]byte(msg))
	key := p.key
	if p.signWith != nil {
		key = p.signWith
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return msg + "." + b64(sig)
}

type loginApp struct {
	t    *testing.T
	prov *fakeProvider
	app  *App
	h    http.Handler
	data string
}

func newLoginApp(t *testing.T) *loginApp {
	t.Helper()
	p := newFakeProvider(t)
	data := t.TempDir()
	for _, id := range []string{"kirke-a", "kirke-b"} {
		os.MkdirAll(filepath.Join(data, id, "Taler"), 0o755)
	}
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data,
		OIDC: &OIDCOptions{Issuer: p.srv.URL, ClientID: "studio", PublicURL: "https://studio.example/"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return &loginApp{t: t, prov: p, app: a, h: a.Handler(), data: data}
}

func (l *loginApp) do(method, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://studio.example"+target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	l.h.ServeHTTP(w, r)
	return w
}

func cookieOf(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login goes through the whole login, as the browser would, and returns the
// callback's answer. code is what the provider hands back.
func (l *loginApp) login(next, code string) *httptest.ResponseRecorder {
	t := l.t
	w := l.do("GET", "/auth/login?next="+url.QueryEscape(next), "")
	if w.Code != http.StatusFound {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	to, _ := url.Parse(w.Header().Get("Location"))
	q := to.Query()
	if !strings.HasPrefix(to.String(), l.prov.srv.URL+"/authorize") || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "https://studio.example/auth/callback" || q.Get("client_id") != "studio" {
		t.Fatalf("authorize URL: %s", to)
	}
	l.prov.nonce, l.prov.verifier = q.Get("nonce"), q.Get("code_challenge")
	state := cookieOf(w, loginCookie)
	if state == nil || !state.HttpOnly || !state.Secure {
		t.Fatalf("login cookie: %+v", state)
	}
	return l.do("GET", "/auth/callback?code="+code+"&state="+url.QueryEscape(q.Get("state")), "", state)
}

func (l *loginApp) session() *http.Cookie {
	w := l.login("/", "good-code")
	c := cookieOf(w, sessionCookie)
	if w.Code != http.StatusFound || c == nil {
		l.t.Fatalf("callback: %d %s", w.Code, w.Header().Get("Location"))
	}
	return c
}

func TestLoginFlow(t *testing.T) {
	l := newLoginApp(t)
	// Without a session, the studio goes to the login and the API says 401.
	if w := l.do("GET", "/", ""); w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "https://studio.example/auth/login?next=") {
		t.Errorf("page without session: %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, u := range []string{"/api/tenants", "/api/folders?tenant=kirke-a", "/api/session"} {
		if w := l.do("GET", u, ""); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"login":true`) {
			t.Errorf("%s without session: %d %s", u, w.Code, w.Body.String())
		}
	}
	if w := l.do("GET", "/index.html", ""); w.Code != http.StatusFound {
		t.Errorf("index.html without session: %d", w.Code)
	}
	// Paths that clean to the API are no way around it.
	for _, u := range []string{"//api/tenants", "/x/../api/tenants", "/./api/session"} {
		if w := l.do("GET", u, ""); w.Code == http.StatusOK {
			t.Errorf("%s without session: %d", u, w.Code)
		}
	}
	// The page that says you are logged out is there without one.
	if w := l.do("GET", "/loggedout.html", ""); w.Code != http.StatusOK {
		t.Errorf("loggedout.html: %d", w.Code)
	}
	// So is the health check.
	if w := l.do("GET", "/healthz", ""); w.Code != http.StatusOK {
		t.Errorf("healthz: %d", w.Code)
	}

	w := l.login("/#/kirke-a/settings", "good-code")
	c := cookieOf(w, sessionCookie)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/#/kirke-a/settings" || c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("callback: %d %s %+v", w.Code, w.Header().Get("Location"), c)
	}
	// Logged in, but a member of no church yet.
	if w := l.do("GET", "/api/tenants", "", c); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "invite link") {
		t.Errorf("no church: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("GET", "/api/folders?tenant=kirke-a", "", c); w.Code != http.StatusNotFound {
		t.Errorf("church of others: %d", w.Code)
	}
	got := decode[map[string]string](t, l.do("GET", "/api/session", "", c))
	if got["logoutUrl"] != "auth/logout" || got["user"] != "Kari Nordmann" {
		t.Errorf("session: %v", got)
	}
	// users.json has who it is; sessions.json only the hash of the cookie.
	b, _ := os.ReadFile(filepath.Join(l.data, usersFile))
	if !strings.Contains(string(b), `"sub": "user-1"`) || !strings.Contains(string(b), "kari@example.org") {
		t.Errorf("users.json: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(l.data, sessionsFile))
	if strings.Contains(string(b), c.Value) || !strings.Contains(string(b), hashToken(c.Value)) {
		t.Errorf("sessions.json: %s", b)
	}

	// A restart keeps the session.
	a2, err := NewApp(l.app.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a2.Close)
	if w := httpDo(a2.Handler(), "GET", "/api/session", c); w.Code != http.StatusOK {
		t.Errorf("after a restart: %d", w.Code)
	}

	// Logging out ends it, there and then.
	w = l.do("POST", "/auth/logout", "", c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://studio.example/loggedout.html" || cookieOf(w, sessionCookie).MaxAge >= 0 {
		t.Errorf("logout: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := l.do("GET", "/api/session", "", c); w.Code != http.StatusUnauthorized {
		t.Errorf("after logout: %d", w.Code)
	}
}

// sessionGone checks that the session of c is ended: no access with it, and
// not in sessions.json.
func (l *loginApp) sessionGone(c *http.Cookie) {
	l.t.Helper()
	if w := l.do("GET", "/api/session", "", c); w.Code != http.StatusUnauthorized {
		l.t.Errorf("after logout: %d", w.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(l.data, sessionsFile)); strings.Contains(string(b), hashToken(c.Value)) {
		l.t.Errorf("sessions.json still has the session: %s", b)
	}
}

// Logging out at the provider too (OpenID Connect RP-Initiated Logout): the
// studio's session ends first, then the browser goes to the provider's
// end_session_endpoint, which sends it back to auth/logout/callback.
func TestLogoutAtProvider(t *testing.T) {
	l := newLoginApp(t)
	l.prov.logout = true
	c := l.session()
	idToken := l.prov.idToken
	b, _ := os.ReadFile(filepath.Join(l.data, sessionsFile))
	if !strings.Contains(string(b), idToken) {
		t.Errorf("sessions.json lacks the ID token")
	}
	w := l.do("POST", "/auth/logout", "", c)
	if w.Code != http.StatusSeeOther || cookieOf(w, sessionCookie).MaxAge >= 0 {
		t.Fatalf("logout: %d %s", w.Code, w.Header().Get("Location"))
	}
	// The session is gone before the browser is sent on.
	l.sessionGone(c)
	to, _ := url.Parse(w.Header().Get("Location"))
	q := to.Query()
	state := cookieOf(w, logoutCookie)
	if to.Host != strings.TrimPrefix(l.prov.srv.URL, "http://") || to.Path != "/end-session" || q.Get("lang") != "en" ||
		q.Get("id_token_hint") != idToken || q.Get("client_id") != "studio" ||
		q.Get("post_logout_redirect_uri") != "https://studio.example/auth/logout/callback" ||
		q.Get("state") == "" || state == nil || state.Value != q.Get("state") || !state.HttpOnly || !state.Secure {
		t.Fatalf("end session URL: %s, state cookie %+v", to, state)
	}

	// Back from the provider: to loggedout.html, with the state cookie cleared;
	// a wrong or missing state goes there too (the session is gone already).
	for name, req := range map[string]struct {
		state  string
		cookie *http.Cookie
	}{
		"right state": {q.Get("state"), state},
		"wrong state": {"forged", state},
		"no cookie":   {q.Get("state"), nil},
	} {
		cookies := []*http.Cookie{}
		if req.cookie != nil {
			cookies = append(cookies, req.cookie)
		}
		w := l.do("GET", "/auth/logout/callback?state="+url.QueryEscape(req.state), "", cookies...)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://studio.example/loggedout.html" ||
			cookieOf(w, logoutCookie) == nil || cookieOf(w, logoutCookie).MaxAge >= 0 || cookieOf(w, sessionCookie) != nil {
			t.Errorf("%s: %d %s", name, w.Code, w.Header().Get("Location"))
		}
	}
}

// Without an end_session_endpoint, without a kept ID token (a session from
// before they were kept), or with the provider down, logging out goes straight
// to loggedout.html, as before.
func TestLogoutWithoutProvider(t *testing.T) {
	check := func(t *testing.T, l *loginApp, h http.Handler, c *http.Cookie) {
		t.Helper()
		r := httptest.NewRequest("POST", "https://studio.example/auth/logout", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://studio.example/loggedout.html" || cookieOf(w, logoutCookie) != nil {
			t.Errorf("logout: %d %s", w.Code, w.Header().Get("Location"))
		}
		l.sessionGone(c)
	}
	t.Run("no end_session_endpoint", func(t *testing.T) {
		l := newLoginApp(t)
		check(t, l, l.h, l.session())
	})
	t.Run("no ID token kept", func(t *testing.T) {
		l := newLoginApp(t)
		l.prov.logout = true
		c := l.session()
		l.app.auth.mu.Lock()
		l.app.auth.session[hashToken(c.Value)].IDToken = ""
		l.app.auth.mu.Unlock()
		check(t, l, l.h, c)
	})
	t.Run("provider down", func(t *testing.T) {
		l := newLoginApp(t)
		l.prov.logout = true
		c := l.session()
		// A restarted studio has not found the provider yet, and cannot now.
		a2, err := NewApp(l.app.cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(a2.Close)
		l.prov.srv.Close()
		check(t, &loginApp{t: t, prov: l.prov, app: a2, h: a2.Handler(), data: l.data}, a2.Handler(), c)
	})
}

func httpDo(h http.Handler, method, target string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://studio.example"+target, nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A token that is not right in any way gives no session.
func TestLoginRefusesBadTokens(t *testing.T) {
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, setup := range map[string]func(p *fakeProvider){
		"wrong signature": func(p *fakeProvider) { p.signWith = other },
		"wrong audience":  func(p *fakeProvider) { p.change = func(c map[string]any) { c["aud"] = "someone-else" } },
		"wrong issuer":    func(p *fakeProvider) { p.change = func(c map[string]any) { c["iss"] = "https://evil.example" } },
		"expired": func(p *fakeProvider) {
			p.change = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }
		},
		"wrong nonce": func(p *fakeProvider) { p.change = func(c map[string]any) { c["nonce"] = "replayed" } },
	} {
		t.Run(name, func(t *testing.T) {
			l := newLoginApp(t)
			setup(l.prov)
			w := l.login("/", "good-code")
			if cookieOf(w, sessionCookie) != nil || w.Header().Get("Location") != "https://studio.example/loggedout.html?failed" {
				t.Errorf("%d %s", w.Code, w.Header().Get("Location"))
			}
		})
	}
	t.Run("wrong code", func(t *testing.T) {
		l := newLoginApp(t)
		if w := l.login("/", "bad-code"); cookieOf(w, sessionCookie) != nil {
			t.Error("a session from a bad code")
		}
	})
	t.Run("wrong state", func(t *testing.T) {
		l := newLoginApp(t)
		w := l.do("GET", "/auth/login", "")
		state := cookieOf(w, loginCookie)
		if w := l.do("GET", "/auth/callback?code=good-code&state=forged", "", state); cookieOf(w, sessionCookie) != nil {
			t.Error("a session with a forged state")
		}
		if w := l.do("GET", "/auth/callback?code=good-code&state=x", ""); cookieOf(w, sessionCookie) != nil {
			t.Error("a session without the login cookie")
		}
	})
	t.Run("forged cookie", func(t *testing.T) {
		l := newLoginApp(t)
		if w := l.do("GET", "/api/session", "", &http.Cookie{Name: sessionCookie, Value: "made-up"}); w.Code != http.StatusUnauthorized {
			t.Errorf("made-up session: %d", w.Code)
		}
	})
}

// After the login, the browser goes back only to a page of the studio.
func TestLoginNextStaysHere(t *testing.T) {
	l := newLoginApp(t)
	for next, want := range map[string]string{
		"/invite.html#kirke-a.x": "/invite.html#kirke-a.x",
		"https://evil.example/":  "/",
		"//evil.example/":        "/",
		"/\\evil.example":        "/",
		"/\t/evil.example":       "/",
		"/%09/evil.example":      "/%09/evil.example",
		"":                       "/",
	} {
		if got := l.login(next, "good-code").Header().Get("Location"); got != want {
			t.Errorf("next %q: went to %q, want %q", next, got, want)
		}
	}
}

func TestInvite(t *testing.T) {
	l := newLoginApp(t)
	c := l.session()
	token, err := CreateInvite(l.data, "kirke-a", RoleMember, time.Hour, "admin cli")
	if err != nil {
		t.Fatal(err)
	}
	// Only its hash is kept.
	b, _ := os.ReadFile(filepath.Join(l.data, "kirke-a", invitesFile))
	if _, secret, _ := strings.Cut(token, "."); strings.Contains(string(b), secret) {
		t.Errorf("invites.json has the secret: %s", b)
	}
	// Opening the link needs a login.
	if w := l.do("GET", "/api/invite?token="+url.QueryEscape(token), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("invite without session: %d", w.Code)
	}
	info := decode[map[string]any](t, l.do("GET", "/api/invite?token="+url.QueryEscape(token), "", c))
	if info["role"] != RoleMember || info["member"] != false || info["church"].(map[string]any)["id"] != "kirke-a" {
		t.Errorf("invite info: %v", info)
	}
	if w := l.do("POST", "/api/invite", `{"token":"`+token+`"}`, c); w.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	// Now a member of kirke-a, and of nothing else.
	type list struct{ Tenants []TenantInfo }
	if l2 := decode[list](t, l.do("GET", "/api/tenants", "", c)); len(l2.Tenants) != 1 || l2.Tenants[0].ID != "kirke-a" {
		t.Errorf("churches: %+v", l2)
	}
	if w := l.do("GET", "/api/config?tenant=kirke-a", "", c); w.Code != http.StatusOK {
		t.Errorf("own church: %d", w.Code)
	}
	if w := l.do("GET", "/api/config?tenant=kirke-b", "", c); w.Code != http.StatusNotFound {
		t.Errorf("other church: %d", w.Code)
	}
	// Used up.
	if w := l.do("POST", "/api/invite", `{"token":"`+token+`"}`, c); w.Code != http.StatusNotFound {
		t.Errorf("second use: %d", w.Code)
	}
	b, _ = os.ReadFile(filepath.Join(l.data, "kirke-a", membersFile))
	if !strings.Contains(string(b), `"role": "member"`) || !strings.Contains(string(b), `"sub": "user-1"`) {
		t.Errorf("members.json: %s", b)
	}
	// A member stays one after a restart.
	a2, err := NewApp(l.app.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a2.Close)
	if w := httpDo(a2.Handler(), "GET", "/api/config?tenant=kirke-a", c); w.Code != http.StatusOK {
		t.Errorf("after a restart: %d", w.Code)
	}

	// An admin invite makes a member an admin.
	token, _ = CreateInvite(l.data, "kirke-a", RoleAdmin, time.Hour, "admin cli")
	if w := l.do("POST", "/api/invite", `{"token":"`+token+`"}`, c); w.Code != http.StatusOK {
		t.Errorf("admin invite: %d", w.Code)
	}
	if ms := l.app.tenants[0].members; len(ms) != 1 || ms[0].Role != RoleAdmin {
		t.Errorf("members: %+v", ms)
	}

	// Wrong, made-up and expired tokens.
	expired, _ := CreateInvite(l.data, "kirke-b", RoleMember, time.Nanosecond, "admin cli")
	time.Sleep(time.Millisecond)
	for _, bad := range []string{expired, "kirke-b.made-up", "kirke-c." + strings.Split(token, ".")[1], "", "nodot"} {
		if w := l.do("POST", "/api/invite", `{"token":"`+bad+`"}`, c); w.Code != http.StatusNotFound {
			t.Errorf("token %q: %d", bad, w.Code)
		}
	}
	// Another church's id with this token's secret is no good either.
	t2, _ := CreateInvite(l.data, "kirke-a", RoleMember, time.Hour, "admin cli")
	if w := l.do("POST", "/api/invite", `{"token":"kirke-b.`+strings.Split(t2, ".")[1]+`"}`, c); w.Code != http.StatusNotFound {
		t.Errorf("secret on another church: %d", w.Code)
	}
}

func TestCreateInviteChecks(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "kirke-a", "Taler"), 0o755)
	for _, c := range []struct{ church, role string }{{"kirke-x", RoleMember}, {"../kirke-a", RoleMember}, {"", RoleMember}, {"kirke-a", "owner"}} {
		if _, err := CreateInvite(data, c.church, c.role, time.Hour, "admin cli"); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

func TestLoginSettings(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "kirke-a", "Taler"), 0o755)
	base := Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}
	ok := OIDCOptions{Issuer: "https://id.example", ClientID: "studio", PublicURL: "https://studio.example/"}
	for name, cfg := range map[string]AppConfig{
		"no data dir":   {Base: Config{Root: data, CacheDir: base.CacheDir, Logger: base.Logger}, OIDC: &ok},
		"no public URL": {Base: base, DataDir: data, OIDC: &OIDCOptions{Issuer: ok.Issuer, ClientID: "studio"}},
		"relative URL":  {Base: base, DataDir: data, OIDC: &OIDCOptions{Issuer: ok.Issuer, ClientID: "studio", PublicURL: "/studio/"}},
	} {
		if a, err := NewApp(cfg); err == nil {
			a.Close()
			t.Errorf("%s: started", name)
		}
	}
	// The provider is only asked when someone logs in: the studio starts while it is down.
	a, err := NewApp(AppConfig{Base: base, DataDir: data, OIDC: &ok})
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
}
