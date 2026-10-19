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
)

// A church is a folder in the data dir:
//
//	<data-dir>/<id>/church.json   name, default tags, remembered names
//	<data-dir>/<id>/Taler/        the sermons
//	<data-dir>/<id>/Innboks/      the inbox
const (
	tenantRoot  = "Taler"
	tenantInbox = "Innboks"
	churchFile  = "church.json"
)

// LegacyTenant is the id of the church set up with Config.Root and Inbox
// (the --root and --inbox flags): a single church without a church folder,
// so its settings are not saved.
const LegacyTenant = "default"

// oldRegistry is the file earlier versions kept all churches in; it is
// imported into the church folders once and renamed.
const oldRegistry = "tenants.json"

// AppConfig sets up all churches.
type AppConfig struct {
	// Base is the setup shared by every church (cache, layout,
	// ffmpeg...). Its Root and Inbox, if set, make the legacy church.
	Base Config
	// DataDir holds a folder per church. "" = no churches can be added.
	DataDir string
}

// churchConfig is church.json. Everything is optional: a church is a folder
// with a Taler folder in it, named after the folder until it is renamed.
type churchConfig struct {
	Name  string    `json:"name,omitempty"`
	Meta  *Meta     `json:"meta,omitempty"`  // default tags; nil = DefaultMeta
	Names *NameList `json:"names,omitempty"` // remembered sermon names; nil = defaultNames
}

// TenantInfo is a church as listed in the UI.
type TenantInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type tenant struct {
	id  string
	dir string // the church folder; "" for the legacy church
	cfg churchConfig
	srv *Server
	api http.Handler
}

func (t *tenant) info() TenantInfo {
	name := t.cfg.Name
	if name == "" {
		name = t.id
		if t.id == LegacyTenant {
			name = "Default"
		}
	}
	return TenantInfo{ID: t.id, Name: name}
}

// App serves several churches (tenants), each with its own date folders,
// inbox and settings. Which one a request is for is the "tenant" query
// parameter or the X-Tenant header; without either it is the first church.
// For now this is only a switch in the UI; access control can be added later
// where the tenant is chosen (App.tenantFor).
type App struct {
	cfg    AppConfig
	limits *limits

	createMu sync.Mutex // one church is added at a time
	mu       sync.Mutex
	tenants  []*tenant
}

// NewApp starts the legacy church (if any) and every church folder in the
// data dir.
func NewApp(cfg AppConfig) (*App, error) {
	if cfg.Base.Logger == nil {
		cfg.Base.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	a := &App{cfg: cfg, limits: newLimits()}
	fail := func(err error) (*App, error) { a.Close(); return nil, err }
	if cfg.Base.Root != "" {
		if _, err := a.start(LegacyTenant, "", churchConfig{}); err != nil {
			return fail(fmt.Errorf("--root: %w", err))
		}
	}
	if cfg.DataDir != "" {
		a.importOldRegistry()
		ids, err := churchFolders(cfg.DataDir)
		if err != nil {
			return fail(err)
		}
		for _, id := range ids {
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
	return os.Rename(tmp, path)
}

// importOldRegistry moves what earlier versions kept in <data-dir>/tenants.json
// into the church folders (a church whose folder is named after its id, or,
// for the first church, after its name), then renames the file.
func (a *App) importOldRegistry() {
	path := filepath.Join(a.cfg.DataDir, oldRegistry)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var reg struct {
		Tenants []struct {
			ID    string    `json:"id"`
			Name  string    `json:"name"`
			Meta  *Meta     `json:"meta"`
			Names *NameList `json:"names"`
		} `json:"tenants"`
	}
	logger := a.cfg.Base.Logger
	if err := json.Unmarshal(b, &reg); err != nil {
		logger.Printf("%s: %v (not imported)", oldRegistry, err)
		return
	}
	imported := true
	for _, t := range reg.Tenants {
		id := t.ID
		if id == LegacyTenant {
			id = tenantID(t.Name)
		}
		dir := filepath.Join(a.cfg.DataDir, id)
		if st, err := os.Stat(dir); id == "" || err != nil || !st.IsDir() {
			logger.Printf("%s: no folder for church %q (%s) yet; its settings are not imported (the file is kept)", oldRegistry, t.Name, id)
			imported = false
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, churchFile)); err == nil {
			continue
		}
		if err := writeChurch(dir, churchConfig{Name: t.Name, Meta: t.Meta, Names: t.Names}); err != nil {
			logger.Printf("%s: church %s: %v", oldRegistry, id, err)
			return // keep the file, try again next start
		}
		logger.Printf("imported the settings of %s from %s into %s/%s", t.Name, oldRegistry, id, churchFile)
	}
	if !imported {
		return
	}
	if err := os.Rename(path, path+".migrated"); err != nil {
		logger.Printf("%s: %v", oldRegistry, err)
	}
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
		cfg.Meta, cfg.Names = cc.Meta, cc.Names
		cfg.SaveMeta = func(m Meta) error { return a.update(t, func(c *churchConfig) { c.Meta = &m }) }
		cfg.SaveNames = func(n NameList) error { return a.update(t, func(c *churchConfig) { c.Names = &n }) }
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
// fails they are left as they were. The legacy church has no folder: its
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

// Close stops every church's background work.
func (a *App) Close() {
	for _, t := range a.tenants {
		if t.srv != nil {
			t.srv.Close()
		}
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
	mux.HandleFunc("GET /api/tenants", a.handleTenants)
	mux.HandleFunc("POST /api/tenants", a.handleTenantCreate)
	mux.HandleFunc("PUT /api/church", a.handleChurchRename)
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
		list = append(list, t.info())
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
	a.createMu.Lock()
	defer a.createMu.Unlock()
	name, err := churchName(q.Name)
	id := tenantID(name)
	if err == nil && id == "" {
		err = errors.New("the name needs letters or digits")
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
