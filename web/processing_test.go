package web

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A church's default processing: built-in until it is set, kept in
// church.json across a restart, validated, per church, and back to the
// built-in with null.
func TestProcessingDefaults(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "main", "Taler"), 0o755)
	cfg := AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}
	a, err := NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	do(h, "POST", "/api/tenants", `{"name":"Kirke B"}`)

	type config struct {
		Defaults struct {
			Start, Mono, Stereo runRequest
			Processing, Builtin processing
		}
	}
	got := decode[config](t, do(h, "GET", "/api/config", "")).Defaults
	if got.Processing != builtinProcessing() || got.Builtin != builtinProcessing() || got.Start.Stereo {
		t.Fatalf("built-in: %+v", got)
	}
	if got.Mono.TargetLUFS == got.Stereo.TargetLUFS {
		t.Errorf("mono and stereo have the same loudness: %v", got.Mono.TargetLUFS)
	}

	p := builtinProcessing()
	p.Stereo, p.Compression, p.FadeIn, p.StereoLevels.TargetLUFS = true, "strong", 3, -15
	body := func(p processing) string {
		b, _ := json.Marshal(map[string]any{"processing": p})
		return string(b)
	}
	if w := do(h, "PUT", "/api/processing?tenant=kirke-b", body(p)); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	bad := p
	bad.Mono.TargetLUFS = 3
	if w := do(h, "PUT", "/api/processing?tenant=kirke-b", body(bad)); w.Code != http.StatusBadRequest {
		t.Errorf("loudness out of range: %d", w.Code)
	}
	bad = p
	bad.FadeOut = 31
	if w := do(h, "PUT", "/api/processing?tenant=kirke-b", body(bad)); w.Code != http.StatusBadRequest {
		t.Errorf("fade too long: %d", w.Code)
	}
	if got := decode[config](t, do(h, "GET", "/api/config", "")).Defaults; got.Processing != builtinProcessing() {
		t.Errorf("the first church changed: %+v", got.Processing)
	}
	a.Close()

	a, err = NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h = a.Handler()
	got = decode[config](t, do(h, "GET", "/api/config?tenant=kirke-b", "")).Defaults
	if got.Processing != p || !got.Start.Stereo || got.Start.TargetLUFS != -15 || got.Start.Compression != "strong" || got.Start.FadeIn != 3 {
		t.Errorf("after restart: %+v", got)
	}
	if got.Mono.TargetLUFS != p.Mono.TargetLUFS || got.Mono.Stereo {
		t.Errorf("mono: %+v", got.Mono)
	}

	// A saved sermon without its own sound is made with the church's defaults.
	var srv *Server
	for _, c := range a.tenants {
		if c.id == "kirke-b" {
			srv = c.srv
		}
	}
	if q := srv.processing().start(); !q.Stereo || q.Compression != "strong" {
		t.Errorf("start: %+v", q)
	}

	if w := do(h, "PUT", "/api/processing?tenant=kirke-b", `{"processing":null}`); w.Code != http.StatusOK {
		t.Fatalf("back to built-in: %d %s", w.Code, w.Body)
	}
	if got := decode[config](t, do(h, "GET", "/api/config?tenant=kirke-b", "")).Defaults; got.Processing != builtinProcessing() {
		t.Errorf("after null: %+v", got.Processing)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "kirke-b", "church.json")); len(b) == 0 || strings.Contains(string(b), "processing") {
		t.Errorf("church.json still has processing: %s", b)
	}
}
