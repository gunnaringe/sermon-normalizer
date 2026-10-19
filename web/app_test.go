package web

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newTestApp(t *testing.T) (*App, http.Handler, string, string) {
	t.Helper()
	root, data := t.TempDir(), t.TempDir()
	a, err := NewApp(AppConfig{
		Base:    Config{Root: root, CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)},
		DataDir: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, a.Handler(), root, data
}

func do(h http.Handler, method, url, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTenantsAddAndIsolate(t *testing.T) {
	_, h, root, data := newTestApp(t)
	os.MkdirAll(filepath.Join(root, "2026", "2026-10-04"), 0o755)

	list := decode[struct {
		Tenants []TenantInfo
		Default string
		CanAdd  bool
	}](t, do(h, "GET", "/api/tenants", ""))
	if len(list.Tenants) != 1 || list.Default != RootTenant || list.Tenants[0].Name != "Default" || !list.CanAdd {
		t.Fatalf("tenants %+v", list)
	}

	if w := do(h, "POST", "/api/tenants", `{"name":"Frelsesarmeen Ål"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/tenants", `{"name":"frelsesarmeen ål"}`); w.Code != http.StatusConflict {
		t.Errorf("same name twice: %d", w.Code)
	}
	if w := do(h, "POST", "/api/tenants", `{"name":"  "}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty name: %d", w.Code)
	}
	for _, d := range []string{"frelsesarmeen-al/Taler", "frelsesarmeen-al/Innboks", "frelsesarmeen-al/church.json"} {
		if _, err := os.Stat(filepath.Join(data, d)); err != nil {
			t.Errorf("missing %s", d)
		}
	}

	type entries struct{ Entries []folderEntry }
	if e := decode[entries](t, do(h, "GET", "/api/folders?path=", "")); len(e.Entries) != 1 {
		t.Errorf("no tenant = the first church: %+v", e)
	}
	if e := decode[entries](t, do(h, "GET", "/api/folders?path=&tenant=frelsesarmeen-al", "")); len(e.Entries) != 0 {
		t.Errorf("the new church must not see the first one's folders: %+v", e)
	}
	r := httptest.NewRequest("GET", "/api/folders?path=2026", nil)
	r.Header.Set("X-Tenant", "frelsesarmeen-al")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("a folder of another church: %d", w.Code)
	}
	if w := do(h, "GET", "/api/folders?path=&tenant=nope", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown church: %d", w.Code)
	}
	if w := do(h, "GET", "/api/folders?path=../frelsesarmeen-al&tenant=default", ""); w.Code != http.StatusBadRequest {
		t.Errorf("reaching into another church: %d", w.Code)
	}
}

func TestTenantsSurviveRestart(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	cfg := AppConfig{Base: Config{Root: root, CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}
	a, err := NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	do(a.Handler(), "POST", "/api/tenants", `{"name":"Kirke B"}`)
	a.Close()
	a, err = NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if len(a.tenants) != 2 || a.tenants[1].id != "kirke-b" {
		t.Errorf("after restart: %+v", a.tenants)
	}
}

func TestTenantID(t *testing.T) {
	for in, want := range map[string]string{"Norkirken Melhus": "norkirken-melhus", " Å/ø – Test ": "a-o-test", "default": "default", "Admin": "admin", "!!": ""} {
		if got := tenantID(in); got != want {
			t.Errorf("tenantID(%q) = %q, want %q", in, got, want)
		}
	}
}

// Churches are the folders of the data dir with a Taler folder or a
// church.json; the name is in church.json, else the folder's.
func TestChurchFolders(t *testing.T) {
	data := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(data, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	mk("norkirken-melhus/Taler/.keep", "")
	mk("norkirken-melhus/church.json", `{"name":"Norkirken Melhus"}`)
	mk("kirke-b/Taler/.keep", "") // no church.json: named after its folder
	mk("cache/x", "")             // not a church
	mk(".hidden/Taler/.keep", "") // not a church
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	list := decode[struct{ Tenants []TenantInfo }](t, do(a.Handler(), "GET", "/api/tenants", "")).Tenants
	want := []TenantInfo{{ID: "kirke-b", Name: "kirke-b"}, {ID: "norkirken-melhus", Name: "Norkirken Melhus"}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("churches %+v, want %+v", list, want)
	}
	if _, err := os.Stat(filepath.Join(data, "kirke-b", "Innboks")); err != nil {
		t.Error("the missing Innboks should be made")
	}

	// A new name is saved in the church's own folder and survives a restart.
	h := a.Handler()
	if w := do(h, "PUT", "/api/church?tenant=kirke-b", `{"name":"Kirke B"}`); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if w := do(h, "PUT", "/api/church?tenant=kirke-b", `{"name":"norkirken melhus"}`); w.Code != http.StatusConflict {
		t.Errorf("a name in use: %d", w.Code)
	}
	if w := do(h, "PUT", "/api/church?tenant=kirke-b", `{"name":" "}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty name: %d", w.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "kirke-b", "church.json")); !strings.Contains(string(b), `"Kirke B"`) {
		t.Errorf("church.json: %s", b)
	}
	if cc, err := readChurch(filepath.Join(data, "norkirken-melhus")); err != nil || cc.Name != "Norkirken Melhus" {
		t.Errorf("the other church was touched: %+v %v", cc, err)
	}
}

func TestBrokenChurchFileStopsStartup(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "a", "Taler"), 0o755)
	os.WriteFile(filepath.Join(data, "a", "church.json"), []byte("{oops"), 0o644)
	if a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}); err == nil {
		a.Close()
		t.Error("a church.json that cannot be read must not be ignored (it would be overwritten)")
	}
}
