package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGuard(t *testing.T) {
	h := Guard(41917, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for host, want := range map[string]int{
		"127.0.0.1:41917":        http.StatusNoContent,
		"localhost:41917":        http.StatusNoContent,
		"127.0.0.1:8080":         http.StatusMisdirectedRequest, // another port
		"evil.example:41917":     http.StatusMisdirectedRequest, // DNS rebinding
		"127.0.0.1.nip.io:41917": http.StatusMisdirectedRequest,
		"":                       http.StatusMisdirectedRequest,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: %d, want %d", host, w.Code, want)
		}
	}
}

// page is a stand-in for the studio: a page, a script and a 404.
var page = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		body := "<html><head><title>x</title></head></html>"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write([]byte(body))
	case "/app.js":
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte("</head>"))
	default:
		http.NotFound(w, r)
	}
})

func TestScriptInPage(t *testing.T) {
	h := NewPresence().Handler("token", page)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	w := get("/")
	if body := w.Body.String(); !strings.Contains(body, `<script src="/_desktop/alive.js"></script>`+"\n</head>") {
		t.Errorf("page without the script: %s", body)
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Errorf("Content-Length %s for %d bytes", w.Header().Get("Content-Length"), w.Body.Len())
	}
	if body := get("/app.js").Body.String(); body != "</head>" {
		t.Errorf("a script was changed: %q", body)
	}
	if w := get("/nothing"); w.Code != http.StatusNotFound {
		t.Errorf("404 became %d", w.Code)
	}
	if w := get("/_desktop/alive.js"); !strings.Contains(w.Body.String(), "EventSource('/_desktop/alive')") {
		t.Errorf("alive.js: %s", w.Body)
	}
}

func TestHello(t *testing.T) {
	h := NewPresence().Handler("right", page)
	for token, want := range map[string]int{"right": http.StatusNoContent, "wrong": http.StatusForbidden, "": http.StatusForbidden} {
		r := httptest.NewRequest("GET", "/_desktop/hello", nil)
		r.Header.Set(tokenHeader, token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("token %q: %d, want %d", token, w.Code, want)
		}
	}
}

func TestIdle(t *testing.T) {
	p := NewPresence()
	now := time.Now()
	p.now = func() time.Time { return now }
	p.since = now
	srv := httptest.NewServer(p.Handler("token", page))
	defer srv.Close()

	now = now.Add(time.Minute)
	if got := p.IdleFor(); got != time.Minute {
		t.Errorf("idle for %s before any window, want 1m", got)
	}
	// A window opens: not idle while it is open, however long.
	ctx, closeWindow := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/_desktop/alive", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("alive: %s", ct)
	}
	now = now.Add(time.Hour)
	if got := p.IdleFor(); got != 0 {
		t.Errorf("idle for %s with a window open", got)
	}
	// It closes: idle counts from then.
	closeWindow()
	resp.Body.Close()
	waitFor(t, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.windows == 0 })
	now = now.Add(2 * time.Minute)
	if got := p.IdleFor(); got != 2*time.Minute {
		t.Errorf("idle for %s 2 minutes after the window closed", got)
	}
	// Any request is use too.
	if _, err := http.Get(srv.URL + "/"); err != nil {
		t.Fatal(err)
	}
	if got := p.IdleFor(); got != 0 {
		t.Errorf("idle for %s right after a request", got)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

func TestSettings(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != DefaultPort || s.DataDir == "" || len(s.Token) != 32 {
		t.Errorf("defaults: %+v", s)
	}
	again, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if *again != *s {
		t.Errorf("not kept: %+v, then %+v", s, again)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{"), 0o600)
	if _, err := LoadSettings(dir); err == nil {
		t.Error("a broken settings file was taken")
	}
}

func TestFirstChurch(t *testing.T) {
	data := filepath.Join(t.TempDir(), "Sermon Studio")
	made, err := FirstChurch(data)
	if err != nil || !made {
		t.Fatalf("made %v, %v", made, err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "my-church", "church.json")); err != nil || !strings.Contains(string(b), `"name": "My church"`) {
		t.Errorf("church.json: %s %v", b, err)
	}
	if st, err := os.Stat(filepath.Join(data, "my-church", "Taler")); err != nil || !st.IsDir() {
		t.Errorf("no Taler folder: %v", err)
	}
	if made, err := FirstChurch(data); err != nil || made {
		t.Errorf("made another: %v, %v", made, err)
	}
}
