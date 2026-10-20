package web

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const groupsHeader = "X-Forwarded-Groups"

func newAccessApp(t *testing.T) http.Handler {
	t.Helper()
	data := t.TempDir()
	for _, id := range []string{"kirke-a", "kirke-b"} {
		os.MkdirAll(filepath.Join(data, id, "Taler"), 0o755)
	}
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data, GroupsHeader: groupsHeader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a.Handler()
}

func as(h http.Handler, groups, method, url, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if groups != "" {
		r.Header.Set(groupsHeader, groups)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAccessChurchesFollowGroups(t *testing.T) {
	h := newAccessApp(t)
	type list struct {
		Tenants []TenantInfo
		Default string
		CanAdd  bool
	}
	ids := func(l list) (s []string) {
		for _, c := range l.Tenants {
			s = append(s, c.ID)
		}
		return
	}

	// A member sees only their church, and it is their default.
	l := decode[list](t, as(h, "other sermon_studio_kirke_b", "GET", "/api/tenants", ""))
	if got := ids(l); len(got) != 1 || got[0] != "kirke-b" || l.Default != "kirke-b" || l.CanAdd || l.Tenants[0].Group != "" {
		t.Errorf("member: %+v", l)
	}
	// An administrator sees all, learns the groups and can add churches.
	l = decode[list](t, as(h, "sermon_studio_admin", "GET", "/api/tenants", ""))
	if got := ids(l); len(got) != 2 || !l.CanAdd || l.Tenants[1].Group != "sermon_studio_kirke_b" {
		t.Errorf("admin: %+v", l)
	}
	// Other groups give nothing, and neither does a missing header.
	for _, g := range []string{"", "kirke-a", "sermon_studio_kirke", "sermon_studio_default", "sermon-studio-kirke-a", "sermon_studio_kirke-a"} {
		if w := as(h, g, "GET", "/api/tenants", ""); w.Code != http.StatusForbidden {
			t.Errorf("groups %q: status %d", g, w.Code)
		}
	}
}

func TestAccessEveryRouteChecksChurch(t *testing.T) {
	h := newAccessApp(t)
	member := "sermon_studio_kirke_a"
	for _, url := range []string{"/api/folders?tenant=kirke-b", "/api/config?tenant=kirke-b", "/api/names?tenant=kirke-b"} {
		if w := as(h, member, "GET", url, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", url, w.Code)
		}
		if w := as(h, member, "GET", strings.Replace(url, "kirke-b", "kirke-a", 1), ""); w.Code != http.StatusOK {
			t.Errorf("own church %s: status %d", url, w.Code)
		}
	}
	if w := as(h, member, "PUT", "/api/church?tenant=kirke-b", `{"name":"Mine now"}`); w.Code != http.StatusNotFound {
		t.Errorf("rename of another church: status %d", w.Code)
	}
	if w := as(h, "", "GET", "/api/folders", ""); w.Code != http.StatusNotFound {
		t.Errorf("no groups: status %d, want 404", w.Code)
	}
}

func TestAccessOnlyAdminsAddChurches(t *testing.T) {
	h := newAccessApp(t)
	if w := as(h, "sermon_studio_kirke_a", "POST", "/api/tenants", `{"name":"Kirke C"}`); w.Code != http.StatusForbidden {
		t.Errorf("member adding: status %d", w.Code)
	}
	w := as(h, "sermon_studio_admin", "POST", "/api/tenants", `{"name":"Kirke C"}`)
	if got := decode[TenantInfo](t, w); w.Code != http.StatusCreated || got.Group != "sermon_studio_kirke_c" {
		t.Errorf("admin adding: %d %+v", w.Code, got)
	}
	// The new church is not visible to the other churches' members.
	if w := as(h, "sermon_studio_kirke_a", "GET", "/api/folders?tenant=kirke-c", ""); w.Code != http.StatusNotFound {
		t.Errorf("new church for another member: status %d", w.Code)
	}
}

// "admin" is the administrators' group, so no church can have that id; the
// same goes for "default".
func TestAdminIsReserved(t *testing.T) {
	h := newAccessApp(t)
	for _, name := range []string{"admin", "Admin", " ADMIN ", "default", "Settings", "recording", "Folders"} {
		w := as(h, "sermon_studio_admin", "POST", "/api/tenants", `{"name":"`+name+`"}`)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "reserved") {
			t.Errorf("%q: %d %s", name, w.Code, w.Body.String())
		}
	}
	// ... also when someone puts the folder there by hand.
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "admin", "Taler"), 0o755)
	if a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}); err == nil {
		a.Close()
		t.Error("a church folder named admin started")
	}
}

// Two churches can never share a group: a-b and a_b would both be
// sermon_studio_a_b.
func TestGroupsAreUnique(t *testing.T) {
	if g := groupOf("norkirken-melhus"); g != "sermon_studio_norkirken_melhus" {
		t.Errorf("groupOf = %q", g)
	}
	data := t.TempDir()
	for _, id := range []string{"kirke-a", "kirke_a"} {
		os.MkdirAll(filepath.Join(data, id, "Taler"), 0o755)
	}
	if a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}); err == nil {
		a.Close()
		t.Error("kirke-a and kirke_a both started")
	}
	os.RemoveAll(filepath.Join(data, "kirke-a"))
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data, GroupsHeader: groupsHeader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if w := as(a.Handler(), "sermon_studio_admin", "POST", "/api/tenants", `{"name":"Kirke A"}`); w.Code != http.StatusConflict {
		t.Errorf("Kirke A next to kirke_a: %d %s", w.Code, w.Body.String())
	}
}

// The page learns where to log out before it knows whether the user has a
// church, so a user without access can still log out.
func TestSessionLogoutURL(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "kirke-a", "Taler"), 0o755)
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data,
		GroupsHeader: groupsHeader, LogoutURL: "https://auth.example/portals/main/logout"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	got := decode[map[string]string](t, as(a.Handler(), "", "GET", "/api/session", ""))
	if got["logoutUrl"] != "https://auth.example/portals/main/logout" {
		t.Errorf("session: %v", got)
	}
}
