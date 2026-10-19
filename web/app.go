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
	"strings"
	"sync"
)

// Names of a church's folders under its own folder in the data dir.
const (
	tenantRoot  = "Taler"
	tenantInbox = "Innboks"
)

// LegacyTenant is the id of the church set up with Config.Root and Inbox
// (the --root and --inbox flags), so existing setups keep working.
const LegacyTenant = "default"

const registryFile = "tenants.json"

// AppConfig sets up all churches.
type AppConfig struct {
	// Base is the setup shared by every church (cache, layout, ffmpeg...).
	// Its Root and Inbox, if set, make the legacy church.
	Base Config
	// DataDir holds the church registry (tenants.json) and a folder per added
	// church: <id>/Taler and <id>/Innboks. "" = no churches can be added and
	// nothing is saved.
	DataDir string
	// LegacyName is the display name of the legacy church.
	LegacyName string
}

// TenantInfo is a church as listed in the UI and saved in the registry.
type TenantInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Meta *Meta  `json:"meta,omitempty"` // default tags; nil = DefaultMeta
}

type registry struct {
	Tenants []TenantInfo `json:"tenants"`
}

type tenant struct {
	TenantInfo
	srv *Server
	api http.Handler
}

// App serves several churches (tenants), each with its own date folders,
// inbox and settings. Which one a request is for is the "tenant" query
// parameter or the X-Tenant header; without either it is the first church.
// For now this is only a switch in the UI; access control can be added later
// where the tenant is chosen (App.tenantFor).
type App struct {
	cfg    AppConfig
	limits *limits

	mu      sync.Mutex
	tenants []*tenant
}

// NewApp starts every church in the registry (and the legacy one).
func NewApp(cfg AppConfig) (*App, error) {
	if cfg.LegacyName == "" {
		cfg.LegacyName = "Default"
	}
	if cfg.Base.Logger == nil {
		cfg.Base.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	a := &App{cfg: cfg, limits: newLimits()}
	reg, err := a.loadRegistry()
	if err != nil {
		return nil, err
	}
	if cfg.Base.Root != "" {
		found := false
		for _, ti := range reg.Tenants {
			found = found || ti.ID == LegacyTenant
		}
		if !found {
			reg.Tenants = append([]TenantInfo{{ID: LegacyTenant, Name: cfg.LegacyName}}, reg.Tenants...)
		}
	}
	for _, ti := range reg.Tenants {
		if _, err := a.start(ti); err != nil {
			a.Close()
			return nil, fmt.Errorf("church %s: %w", ti.ID, err)
		}
	}
	if len(a.tenants) == 0 {
		return nil, errors.New("no churches: set --root, or --data-dir with a tenants.json")
	}
	return a, nil
}

func (a *App) loadRegistry() (registry, error) {
	var reg registry
	if a.cfg.DataDir == "" {
		return reg, nil
	}
	b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, registryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return reg, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &reg)
	}
	if err != nil {
		return reg, fmt.Errorf("%s: %w", registryFile, err)
	}
	return reg, nil
}

// saveRegistry writes the registry atomically. The caller holds a.mu.
func (a *App) saveRegistry() error {
	if a.cfg.DataDir == "" {
		return nil
	}
	reg := registry{Tenants: []TenantInfo{}}
	for _, t := range a.tenants {
		reg.Tenants = append(reg.Tenants, t.TenantInfo)
	}
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(a.cfg.DataDir, registryFile)
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// start brings up the Server of one church and adds it to the list.
func (a *App) start(ti TenantInfo) (*tenant, error) {
	cfg := a.cfg.Base
	cfg.limits = a.limits
	cfg.Meta = ti.Meta
	cfg.SaveMeta = func(m Meta) error { return a.setMeta(ti.ID, m) }
	if ti.ID != LegacyTenant {
		dir := filepath.Join(a.cfg.DataDir, ti.ID)
		cfg.Root, cfg.Inbox = filepath.Join(dir, tenantRoot), filepath.Join(dir, tenantInbox)
		for _, d := range []string{cfg.Root, cfg.Inbox} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return nil, err
			}
		}
	}
	srv, err := New(cfg)
	if err != nil {
		return nil, err
	}
	t := &tenant{TenantInfo: ti, srv: srv, api: srv.apiMux()}
	a.tenants = append(a.tenants, t)
	return t, nil
}

// setMeta stores a church's default tags in the registry.
func (a *App) setMeta(id string, m Meta) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tenants {
		if t.ID == id {
			old := t.Meta
			t.Meta = &m
			if err := a.saveRegistry(); err != nil {
				t.Meta = old
				return err
			}
			return nil
		}
	}
	return errors.New("no such church")
}

// Close stops every church's background work.
func (a *App) Close() {
	for _, t := range a.tenants {
		t.srv.Close()
	}
}

// tenantFor picks the church a request is for.
func (a *App) tenantFor(r *http.Request) (*tenant, bool) {
	id := r.URL.Query().Get("tenant")
	if id == "" {
		id = r.Header.Get("X-Tenant")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tenants {
		if id == "" || t.ID == id {
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
	mux.HandleFunc("GET /api/tenants", a.handleTenants)
	mux.HandleFunc("POST /api/tenants", a.handleTenantCreate)
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
	// Once a login proxy sets a cookie, other sites must not be able to
	// start runs, uploads or imports on the user's behalf.
	return http.NewCrossOriginProtection().Handler(mux)
}

func (a *App) handleTenants(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	list := []TenantInfo{}
	for _, t := range a.tenants {
		list = append(list, t.TenantInfo)
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"tenants": list, "default": list[0].ID, "canAdd": a.cfg.DataDir != ""})
}

var reNotID = regexp.MustCompile(`[^a-z0-9]+`)

// tenantID makes a folder-safe id from a church name.
func tenantID(name string) string {
	r := strings.NewReplacer("æ", "ae", "ø", "o", "å", "a", "ä", "a", "ö", "o", "é", "e")
	id := strings.Trim(reNotID.ReplaceAllString(r.Replace(strings.ToLower(name)), "-"), "-")
	if id == LegacyTenant {
		id = ""
	}
	return id
}

func (a *App) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
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
	name := strings.TrimSpace(q.Name)
	id := tenantID(name)
	if name == "" || id == "" || len(name) > 80 {
		writeError(w, http.StatusBadRequest, errors.New("give the church a name (letters and digits, up to 80 characters)"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tenants {
		if t.ID == id || strings.EqualFold(t.Name, name) {
			writeError(w, http.StatusConflict, fmt.Errorf("there is already a church called %q", t.Name))
			return
		}
	}
	t, err := a.start(TenantInfo{ID: id, Name: name})
	if err == nil {
		if err = a.saveRegistry(); err != nil {
			a.tenants = a.tenants[:len(a.tenants)-1]
			t.srv.Close()
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("added church %s (%s)", t.Name, t.ID)
	writeJSON(w, http.StatusCreated, t.TenantInfo)
}
