package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// A church is a folder in the data dir:
//
//	<data-dir>/<id>/church.json   name, default tags
//	<data-dir>/<id>/Taler/        the sermons
//	<data-dir>/<id>/Innboks/      the inbox
const (
	tenantRoot  = "Taler"
	tenantInbox = "Innboks"
	churchFile  = "church.json"
)

// RootTenant is the id of the church set up with Config.Root and Inbox
// (the --root and --inbox flags): a single church without a church folder,
// so its settings are not saved.
const RootTenant = "default"

// AppConfig sets up all churches.
type AppConfig struct {
	// Base is the setup shared by every church (cache, layout,
	// ffmpeg...). Its Root and Inbox, if set, make the root church.
	Base Config
	// DataDir holds a folder per church. "" = no churches can be added.
	DataDir string
	// OIDC turns on the studio's own login (login.go): a church is then for
	// its members (members.go). nil = no access control (see access.go).
	OIDC *OIDCOptions
	// Share turns on shares (codes for the public upload page); nil = off.
	Share *ShareOptions
	// ParallelRuns is how many runs (and samples) can work at once, across all churches; 0 = 1. A run uses
	// about one core; more of them wait their turn. One recording is never run twice at once.
	ParallelRuns int
}

// churchConfig is church.json. Everything is optional: a church is a folder
// with a Taler folder in it, named after the folder until it is renamed.
type churchConfig struct {
	Name string `json:"name,omitempty"`
	Meta *Meta  `json:"meta,omitempty"` // default tags; nil = DefaultMeta
	// Processing is the default processing (processing.go); nil = the built-in.
	Processing *processing `json:"processing,omitempty"`
	// EndedSharesDays is how many days a share that has ended is kept before it is removed by itself;
	// nil = defaultEndedSharesDays, 0 = never.
	EndedSharesDays *int `json:"endedSharesDays,omitempty"`
	// Clips are the intro and outro clips (clips.go): the list, the defaults, the pause.
	Clips *clipLibrary `json:"clips,omitempty"`
}

// TenantInfo is a church as listed in the UI.
type TenantInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"` // the church's own logo (see logo.go): its version; "" = none
	Role string `json:"role,omitempty"` // the user's: admin or member (in the church list)
}

type tenant struct {
	id  string
	dir string // the church folder; "" for the root church
	cfg churchConfig
	srv *Server
	api http.Handler

	shares  []*Share // guarded by App.shareMu
	members []member // guarded by App.mu
}

func (t *tenant) info() TenantInfo {
	name := t.cfg.Name
	if name == "" {
		name = t.id
		if t.id == RootTenant {
			name = "Default"
		}
	}
	return TenantInfo{ID: t.id, Name: name, Logo: t.logoVersion()}
}

// App serves several churches (tenants), each with its own date folders,
// inbox and settings. Which one a request is for is the "tenant" query
// parameter or the X-Tenant header; without either it is the first church the
// user sees. Who sees which church is in access.go; every request to a church
// goes through App.tenantFor.
type App struct {
	cfg    AppConfig
	limits *limits

	createMu sync.Mutex // one church is added at a time
	mu       sync.Mutex
	tenants  []*tenant

	share   *shareState // nil = shares are off
	shareMu sync.Mutex  // every church's shares and uploads in progress
	auth    *auth       // nil = no login of its own
}

// shareState is what the public upload page keeps in memory.
type shareState struct {
	limits *limiter
	busy   map[string]bool // uploads receiving a chunk now
	// sending counts the files a download share is sending now, by share id
	sending map[string]int
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewApp starts the root church (if any) and every church folder in the
// data dir.
func NewApp(cfg AppConfig) (*App, error) {
	if cfg.Base.Logger == nil {
		cfg.Base.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	a := &App{cfg: cfg, limits: newLimits(cfg.ParallelRuns)}
	if cfg.Share != nil {
		a.share = &shareState{limits: newLimiter(cfg.Share.Limits), busy: map[string]bool{}, sending: map[string]int{}, done: make(chan struct{})}
	}
	fail := func(err error) (*App, error) { a.Close(); return nil, err }
	if cfg.OIDC != nil {
		var err error
		if a.auth, err = newAuth(*cfg.OIDC, cfg.DataDir, cfg.Base.Logger); err != nil {
			return nil, err
		}
	}
	if cfg.Base.Root != "" {
		if _, err := a.start(RootTenant, "", churchConfig{}); err != nil {
			return fail(fmt.Errorf("--root: %w", err))
		}
	}
	if cfg.DataDir != "" {
		ids, err := churchFolders(cfg.DataDir)
		if err != nil {
			return fail(err)
		}
		for _, id := range ids {
			if reservedID(id) {
				return fail(fmt.Errorf("church folder %q: the name is reserved", id))
			}
			dir := filepath.Join(cfg.DataDir, id)
			cc, err := readChurch(dir)
			if err == nil {
				_, err = a.start(id, dir, cc)
			}
			if err != nil {
				return fail(fmt.Errorf("church %s: %w", id, err))
			}
		}
	}
	if len(a.tenants) == 0 {
		return nil, errors.New("no churches: set --root, or --data-dir with a folder per church (<id>/Taler)")
	}
	if a.share != nil {
		a.share.wg.Add(1)
		go a.keepSweeping()
	}
	return a, nil
}

// churchFolders lists the folders of dir that are churches: they have a
// church.json or a Taler folder. Sorted, so the first one is always the same.
func churchFolders(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	var ids []string
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		for _, marker := range []string{churchFile, tenantRoot} {
			if _, err := os.Stat(filepath.Join(dir, de.Name(), marker)); err == nil {
				ids = append(ids, de.Name())
				break
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func readChurch(dir string) (churchConfig, error) {
	var cc churchConfig
	b, err := os.ReadFile(filepath.Join(dir, churchFile))
	if errors.Is(err, fs.ErrNotExist) {
		return cc, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &cc)
	}
	if err != nil {
		return cc, fmt.Errorf("%s: %w", churchFile, err)
	}
	return cc, nil
}

// writeChurch saves church.json atomically.
func writeChurch(dir string, cc churchConfig) error {
	b, err := json.MarshalIndent(cc, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, churchFile)
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return normalizer.Rename(tmp, path)
}

// start brings up the Server of one church and adds it to the list.
func (a *App) start(id, dir string, cc churchConfig) (*tenant, error) {
	cfg := a.cfg.Base
	cfg.limits = a.limits
	t := &tenant{id: id, dir: dir, cfg: cc}
	if dir != "" {
		cfg.Root, cfg.Inbox = filepath.Join(dir, tenantRoot), filepath.Join(dir, tenantInbox)
		for _, d := range []string{cfg.Root, cfg.Inbox} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return nil, err
			}
		}
		cfg.Meta = cc.Meta
		cfg.SaveMeta = func(m Meta) error { return a.update(t, func(c *churchConfig) { c.Meta = &m }) }
		cfg.Processing = cc.Processing
		cfg.SaveProcessing = func(p *processing) error { return a.update(t, func(c *churchConfig) { c.Processing = p }) }
		cfg.ClipsDir, cfg.Clips = filepath.Join(dir, clipsDir), cc.Clips
		cfg.SaveClips = func(l clipLibrary) error { return a.update(t, func(c *churchConfig) { c.Clips = &l }) }
	}
	if dir != "" {
		var err error
		if t.shares, err = readShares(dir); err != nil {
			return nil, err
		}
		if t.members, err = readMembers(dir); err != nil {
			return nil, err
		}
	}
	srv, err := New(cfg)
	if err != nil {
		return nil, err
	}
	t.srv, t.api = srv, srv.apiMux()
	a.mu.Lock()
	a.tenants = append(a.tenants, t)
	a.mu.Unlock()
	return t, nil
}

// update changes a church's settings and saves its church.json; if saving
// fails they are left as they were. The root church has no folder: its
// settings stay in memory.
func (a *App) update(t *tenant, change func(*churchConfig)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := t.cfg
	change(&next)
	if t.dir != "" {
		if err := writeChurch(t.dir, next); err != nil {
			return err
		}
	}
	t.cfg = next
	return nil
}

// Busy reports whether stopping now would break something: a church is busy (Server.Busy), or the share
// page is receiving or sending.
func (a *App) Busy() bool {
	a.mu.Lock()
	tenants := append([]*tenant(nil), a.tenants...)
	a.mu.Unlock()
	for _, t := range tenants {
		if t.srv.Busy() {
			return true
		}
	}
	if a.share == nil {
		return false
	}
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	return len(a.share.busy) > 0 || len(a.share.sending) > 0
}

// Close stops every church's background work.
func (a *App) Close() {
	if a.share != nil {
		close(a.share.done)
		a.share.wg.Wait()
	}
	for _, t := range a.tenants {
		if t.srv != nil {
			t.srv.Close()
		}
	}
}

// tenantFor picks the church a request is for: the one it names, else the
// first the user sees. A church the user does not see is "no such church".
func (a *App) tenantFor(r *http.Request) (*tenant, bool) {
	id := r.URL.Query().Get("tenant")
	if id == "" {
		id = r.Header.Get("X-Tenant")
	}
	for _, t := range a.visible(a.userOf(r)) {
		if id == "" || t.id == id {
			return t, true
		}
	}
	return nil, false
}

// Handler is the whole site: the front end and the API of every church.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	mux.Handle("GET /", http.FileServerFS(sub))
	mux.HandleFunc("GET /api/session", a.handleSession)
	// healthz is for the health checks (Kubernetes): open, also with the login.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	if a.auth != nil {
		mux.HandleFunc("GET /auth/login", a.auth.handleLogin)
		mux.HandleFunc("GET /auth/callback", a.auth.handleCallback)
		mux.HandleFunc("POST /auth/logout", a.auth.handleLogout)
		mux.HandleFunc("GET /auth/logout/callback", a.auth.handleLogoutCallback)
		mux.HandleFunc("GET /api/invite", a.handleInvite)
		mux.HandleFunc("POST /api/invite", a.handleInviteAccept)
		mux.HandleFunc("GET /api/members", a.handleMembers)
		mux.HandleFunc("PUT /api/members/{id}", a.handleMemberRole)
		mux.HandleFunc("POST /api/members/{id}/remove", a.handleMemberRemove)
		mux.HandleFunc("POST /api/members/leave", a.handleLeave)
		mux.HandleFunc("POST /api/invites", a.handleInviteCreate)
		mux.HandleFunc("POST /api/invites/{id}/revoke", a.handleInviteRevoke)
	}
	mux.HandleFunc("GET /api/tenants", a.handleTenants)
	mux.HandleFunc("POST /api/tenants", a.handleTenantCreate)
	mux.HandleFunc("PUT /api/church", a.handleChurchRename)
	mux.HandleFunc("GET /api/logo", a.handleLogo)
	mux.HandleFunc("PUT /api/logo", a.handleLogoPut)
	mux.HandleFunc("DELETE /api/logo", a.handleLogoDelete)
	if a.share != nil {
		mux.HandleFunc("GET /api/shares", a.handleShares)
		mux.HandleFunc("POST /api/shares", a.handleShareCreate)
		mux.HandleFunc("POST /api/shares/{id}/pause", a.handleSharePause)
		mux.HandleFunc("POST /api/shares/{id}/resume", a.handleShareResume)
		mux.HandleFunc("POST /api/shares/{id}/remove", a.handleShareRemove)
		mux.HandleFunc("POST /api/shares/remove-ended", a.handleShareRemoveEnded)
		mux.HandleFunc("PUT /api/shares/ended-days", a.handleEndedSharesDays)
		mux.HandleFunc("GET /api/shares/active", a.handleSharesActive)
	}
	toChurch := func(w http.ResponseWriter, r *http.Request) {
		t, ok := a.tenantFor(r)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("no such church"))
			return
		}
		t.api.ServeHTTP(w, r)
	}
	for _, method := range []string{"GET", "POST", "PUT"} {
		mux.HandleFunc(method+" /api/", toChurch)
	}
	var h http.Handler = mux
	if a.auth != nil {
		h = a.auth.require(a.requireAdmin(mux))
	}
	// Once there is a login cookie, other sites must not be able to
	// start runs, uploads or imports on the user's behalf.
	return http.NewCrossOriginProtection().Handler(h)
}

// handleSession tells the page what it needs before it knows the user has
// a church: where to log out.
func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	if a.auth != nil {
		u := a.userOf(r)
		writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": "auth/logout", "user": a.auth.userName(u.id)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (a *App) handleTenants(w http.ResponseWriter, r *http.Request) {
	u := a.userOf(r)
	list := []TenantInfo{}
	for _, t := range a.visible(u) {
		i := t.info()
		i.Role = a.roleOf(u, t)
		list = append(list, i)
	}
	if len(list) == 0 {
		writeError(w, http.StatusForbidden, errNoChurch())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": list, "default": list[0].ID, "canAdd": a.canAdd(), "shares": a.share != nil,
		"login": a.auth != nil})
}

var reNotID = regexp.MustCompile(`[^a-z0-9]+`)

// tenantID makes a folder-safe id from a church name ("" if it has no letters
// or digits). It may be reserved.
func tenantID(name string) string {
	r := strings.NewReplacer("æ", "ae", "ø", "o", "å", "a", "ä", "a", "ö", "o", "é", "e")
	id := strings.Trim(reNotID.ReplaceAllString(r.Replace(strings.ToLower(name)), "-"), "-")
	return id
}

// churchName checks a church's display name.
func churchName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 80 || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }) {
		return "", errors.New("give the church a name (up to 80 characters)")
	}
	return name, nil
}

// nameTaken reports the church (other than t) that already has this name.
func (a *App) nameTaken(t *tenant, name string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, o := range a.tenants {
		if o != t && strings.EqualFold(o.info().Name, name) {
			return o.info().Name, true
		}
	}
	return "", false
}

// canAdd reports whether churches are added in the studio: only without the
// login (with it, `sermon-studio admin` does that), and with a data dir.
func (a *App) canAdd() bool { return a.auth == nil && a.cfg.DataDir != "" }

func (a *App) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	if a.auth != nil {
		writeError(w, http.StatusForbidden, errors.New("churches are not added in the studio when it has a login"))
		return
	}
	if a.cfg.DataDir == "" {
		writeError(w, http.StatusNotFound, errors.New("adding churches needs --data-dir"))
		return
	}
	var q struct {
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.createMu.Lock()
	defer a.createMu.Unlock()
	name, err := churchName(q.Name)
	id := tenantID(name)
	if err == nil && id == "" {
		err = errors.New("the name needs letters or digits")
	}
	if err == nil && reservedID(id) {
		err = fmt.Errorf("the name %q is reserved: pick another", id)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	dir := filepath.Join(a.cfg.DataDir, id)
	if taken, ok := a.nameTaken(nil, name); ok {
		writeError(w, http.StatusConflict, fmt.Errorf("there is already a church called %q", taken))
		return
	}
	if _, err := os.Stat(dir); err == nil {
		writeError(w, http.StatusConflict, fmt.Errorf("the folder %s already exists", id))
		return
	}
	cc := churchConfig{Name: name}
	t, err := a.start(id, dir, cc) // makes the folders
	if err == nil {
		err = writeChurch(dir, cc)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("added church %s (%s)", name, id)
	writeJSON(w, http.StatusCreated, t.info())
}

// handleChurchRename changes the display name of the selected church.
func (a *App) handleChurchRename(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tenantFor(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
		return
	}
	var q struct {
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name, err := churchName(q.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if taken, ok := a.nameTaken(t, name); ok {
		writeError(w, http.StatusConflict, fmt.Errorf("there is already a church called %q", taken))
		return
	}
	if err := a.update(t, func(c *churchConfig) { c.Name = name }); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, t.info())
}
