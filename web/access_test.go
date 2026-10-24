package web

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newOpenApp(t *testing.T) http.Handler {
	t.Helper()
	data := t.TempDir()
	for _, id := range []string{"kirke-a", "kirke-b"} {
		os.MkdirAll(filepath.Join(data, id, "Taler"), 0o755)
	}
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a.Handler()
}

func call(h http.Handler, method, url, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Without the login, everyone sees every church and can add churches.
func TestAccessOpen(t *testing.T) {
	h := newOpenApp(t)
	type list struct {
		Tenants []TenantInfo
		Default string
		CanAdd  bool
	}
	l := decode[list](t, call(h, "GET", "/api/tenants", ""))
	if len(l.Tenants) != 2 || l.Default != "kirke-a" || !l.CanAdd {
		t.Errorf("churches: %+v", l)
	}
	if w := call(h, "GET", "/api/config?tenant=kirke-b", ""); w.Code != http.StatusOK {
		t.Errorf("a church: %d", w.Code)
	}
	if w := call(h, "POST", "/api/tenants", `{"name":"Kirke C"}`); w.Code != http.StatusCreated {
		t.Errorf("adding: %d %s", w.Code, w.Body.String())
	}
}

// With the login, churches are not added in the studio (sermon-studio admin
// does that), whoever asks.
func TestLoginAddsNoChurches(t *testing.T) {
	l := newLoginApp(t)
	c := l.session()
	token, _ := CreateInvite(l.data, "kirke-a", RoleAdmin, time.Hour, "admin cli")
	l.do("POST", "/api/invite", `{"token":"`+token+`"}`, c)
	if got := decode[map[string]any](t, l.do("GET", "/api/tenants", "", c)); got["canAdd"] != false {
		t.Errorf("canAdd: %v", got)
	}
	if w := l.do("POST", "/api/tenants", `{"name":"Kirke C"}`, c); w.Code != http.StatusForbidden {
		t.Errorf("adding: %d", w.Code)
	}
}

// "default" and the first words of the page addresses cannot be churches.
func TestReservedIDs(t *testing.T) {
	h := newOpenApp(t)
	for _, name := range []string{"default", "Settings", "recording", "Folders", "nul"} {
		w := call(h, "POST", "/api/tenants", `{"name":"`+name+`"}`)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "reserved") {
			t.Errorf("%q: %d %s", name, w.Code, w.Body.String())
		}
	}
	// ... also when someone puts the folder there by hand.
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "settings", "Taler"), 0o755)
	if a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}); err == nil {
		a.Close()
		t.Error("a church folder named settings started")
	}
}
