package web

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newHostsApp is a studio with church hosts: the login on app.example.org,
// kirke-a.example.org and kirke-b.example.org for the churches.
func newHostsApp(t *testing.T) *loginApp {
	t.Helper()
	p := newFakeProvider(t)
	data := t.TempDir()
	for _, id := range []string{"kirke-a", "kirke-b"} {
		os.MkdirAll(filepath.Join(data, id, "Taler"), 0o755)
	}
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data,
		ChurchHost: "{church}.example.org",
		OIDC:       &OIDCOptions{Issuer: p.srv.URL, ClientID: "studio", PublicURL: "https://app.example.org/", CookieDomain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return &loginApp{t: t, prov: p, app: a, h: a.Handler(), data: data, host: "app.example.org"}
}

func TestChurchHostParse(t *testing.T) {
	h, err := parseChurchHost("{church}.example.org")
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		"kirke-a.example.org": "kirke-a", "kirke-a.example.org:443": "kirke-a",
		"app.example.org": "", "share.example.org": "", // the site's own hosts
		"a.b.example.org": "", "example.org": "", ".example.org": "", "kirke-a.example.org.": "", "KIRKE-A.example.org": "",
		"kirke_a.example.org": "", "kirke-a.example.org.evil.example": "", "evilexample.org": "",
	} {
		if got, _ := h.church(host); got != want {
			t.Errorf("%s: %q, want %q", host, got, want)
		}
	}
	for _, bad := range []string{"example.org", "{church}", "{church}.", "x.{church}.example.org", "{church}.Example.org", "{church}.example.org/"} {
		if _, err := parseChurchHost(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestChurchHostSettings(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "kirke-a", "Taler"), 0o755)
	base := Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}
	oidc := func(public, domain string) *OIDCOptions {
		return &OIDCOptions{Issuer: "https://id.example", ClientID: "studio", PublicURL: public, CookieDomain: domain}
	}
	for name, cfg := range map[string]AppConfig{
		"no login":            {Base: base, DataDir: data, ChurchHost: "{church}.example.org"},
		"no cookie domain":    {Base: base, DataDir: data, ChurchHost: "{church}.example.org", OIDC: oidc("https://app.example.org/", "")},
		"cookie domain apart": {Base: base, DataDir: data, ChurchHost: "{church}.example.org", OIDC: oidc("https://app.example.org/", "other.org")},
		"login elsewhere":     {Base: base, DataDir: data, ChurchHost: "{church}.example.org", OIDC: oidc("https://app.other.org/", "example.org")},
		"login on a church":   {Base: base, DataDir: data, ChurchHost: "{church}.example.org", OIDC: oidc("https://studio.example.org/", "example.org")},
	} {
		if a, err := NewApp(cfg); err == nil {
			a.Close()
			t.Errorf("%s: started", name)
		}
	}
}

// The church is the host's, and the user must be its member: a member of
// kirke-a gets nothing of kirke-b, on its host or by naming it.
func TestChurchHostAccess(t *testing.T) {
	l := newHostsApp(t)
	c := l.session()
	if c.Domain != "example.org" {
		t.Errorf("session cookie domain %q", c.Domain)
	}
	token, _ := CreateInvite(l.data, "kirke-a", RoleMember, time.Hour, "admin cli")
	if w := l.do("POST", "/api/invite", `{"token":"`+token+`"}`, c); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"url":"https://kirke-a.example.org/"`) {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	for _, req := range []struct {
		host, target string
		want         int
	}{
		{"kirke-a.example.org", "/api/config", http.StatusOK},
		{"kirke-a.example.org", "/api/config?tenant=kirke-a", http.StatusOK},
		{"kirke-a.example.org", "/api/config?tenant=kirke-b", http.StatusNotFound},
		{"kirke-b.example.org", "/api/config", http.StatusNotFound},
		{"kirke-b.example.org", "/api/config?tenant=kirke-a", http.StatusNotFound},
		{"app.example.org", "/api/config?tenant=kirke-a", http.StatusNotFound},
		{"elsewhere.example", "/api/config?tenant=kirke-a", http.StatusNotFound},
	} {
		if w := l.doOn(req.host, "GET", req.target, "", c); w.Code != req.want {
			t.Errorf("%s%s: %d, want %d", req.host, req.target, w.Code, req.want)
		}
	}
	// The church list gives each church's address, and the host's church first in line.
	list := decode[struct {
		Tenants []TenantInfo
		Default string
	}](t, l.doOn("kirke-a.example.org", "GET", "/api/tenants", "", c))
	if len(list.Tenants) != 1 || list.Tenants[0].URL != "https://kirke-a.example.org/" || list.Default != "kirke-a" {
		t.Errorf("tenants: %+v", list)
	}
	// The studio's invite links are on the login host.
	admin, _ := CreateInvite(l.data, "kirke-a", RoleAdmin, time.Hour, "admin cli")
	l.do("POST", "/api/invite", `{"token":"`+admin+`"}`, c)
	made := decode[map[string]any](t, l.doOn("kirke-a.example.org", "POST", "/api/invites", `{"role":"member","days":1}`, c))
	if link, _ := made["link"].(string); !strings.HasPrefix(link, "https://app.example.org/invite.html#kirke-a.") {
		t.Errorf("invite link: %v", made)
	}
}

func TestChurchHostPages(t *testing.T) {
	l := newHostsApp(t)
	c := l.session()
	// The login host's start is the church picker; an unknown church says so.
	if w := l.do("GET", "/", "", c); w.Code != http.StatusFound || w.Header().Get("Location") != "https://app.example.org/churches.html" {
		t.Errorf("login host: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := l.doOn("nowhere.example.org", "GET", "/", "", c); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "No such church") {
		t.Errorf("unknown church: %d %s", w.Code, w.Body.String())
	}
	// Without a session, a church host's page goes to the login on the login host, and back.
	w := l.doOn("kirke-a.example.org", "GET", "/", "")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://app.example.org/auth/login?next="+url.QueryEscape("https://kirke-a.example.org/") {
		t.Errorf("church host without session: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = l.doOn("kirke-a.example.org", "GET", "/auth/login?next="+url.QueryEscape("/#/kirke-a/settings"), "")
	if w.Header().Get("Location") != "https://app.example.org/auth/login?next="+url.QueryEscape("https://kirke-a.example.org/#/kirke-a/settings") {
		t.Errorf("login on a church host: %s", w.Header().Get("Location"))
	}
}

// After the login, the browser goes back to the login host or a church
// host, and nowhere else.
func TestChurchHostNext(t *testing.T) {
	l := newHostsApp(t)
	for next, want := range map[string]string{
		"https://kirke-a.example.org/#/kirke-a/settings": "https://kirke-a.example.org/#/kirke-a/settings",
		"https://app.example.org/invite.html#kirke-a.x":  "https://app.example.org/invite.html#kirke-a.x",
		"/churches.html": "/churches.html",
		"https://kirke-a.example.org@evil.example/": "/",
		"https://app.example.org.evil.example/":     "/",
		"https://a.b.example.org/":                  "/",
		"http://kirke-a.example.org/":               "/",
		"https://KIRKE-A.example.org/":              "/",
		"https://kirke-a.example.org./":             "/",
		"https://kirke-a.example.org:8443/":         "/",
		"https://evil.example/":                     "/",
		"//kirke-a.example.org/":                    "/",
		"https://kirke-a.example.org/\t/x":          "/",
	} {
		if got := l.login(next, "good-code").Header().Get("Location"); got != want {
			t.Errorf("next %q: went to %q, want %q", next, got, want)
		}
	}
}

// Logging out on a church host ends the session for every host, at the
// provider too, and comes back to the login host.
func TestChurchHostLogout(t *testing.T) {
	l := newHostsApp(t)
	l.prov.logout = true
	c := l.session()
	w := l.doOn("kirke-a.example.org", "POST", "/auth/logout", "", c)
	to, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(to.String(), l.prov.srv.URL+"/end-session") ||
		to.Query().Get("post_logout_redirect_uri") != "https://app.example.org/auth/logout/callback" {
		t.Fatalf("logout: %d %s", w.Code, to)
	}
	state := cookieOf(w, logoutCookie)
	if state == nil || state.Domain != "example.org" {
		t.Fatalf("logout cookie: %+v", state)
	}
	if w := l.doOn("kirke-b.example.org", "GET", "/api/session", "", c); w.Code != http.StatusUnauthorized {
		t.Errorf("session after logout: %d", w.Code)
	}
	w = l.do("GET", "/auth/logout/callback?state="+url.QueryEscape(to.Query().Get("state")), "", state)
	if w.Header().Get("Location") != "https://app.example.org/loggedout.html" {
		t.Errorf("callback: %s", w.Header().Get("Location"))
	}
}

func TestChurchHostReserved(t *testing.T) {
	for _, id := range []string{"app", "share", "www", "id", "admin"} {
		if !reservedID(id) {
			t.Errorf("%s is not reserved", id)
		}
	}
}
