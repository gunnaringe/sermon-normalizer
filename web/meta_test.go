package web

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExpandTemplate(t *testing.T) {
	d := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) // a Sunday
	for _, c := range []struct{ tpl, name, want string }{
		{"{yyyy}-{mm}-{dd}", "", "2026-10-04"},
		{"{date} {name}", "", "2026-10-04"},
		{"{date} {name}", "Kveld", "2026-10-04 Kveld"},
		{"{weekday} {dd}.{mm}.{yy}", "", "Sunday 04.10.26"},
		{"Norkirken", "", "Norkirken"},
		{"", "x", ""},
	} {
		if got, err := expandTemplate(c.tpl, d, c.name); err != nil || got != c.want {
			t.Errorf("expandTemplate(%q, %q) = %q, %v; want %q", c.tpl, c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{"{year}", "{yyyy", "yyyy}", "{}", "{{yyyy}}"} {
		if got, err := expandTemplate(bad, d, ""); err == nil {
			t.Errorf("expandTemplate(%q) = %q, want an error", bad, got)
		}
	}
}

func TestParseFolder(t *testing.T) {
	for in, want := range map[string]string{"2026-10-04": "2026-10-04|", "2026-10-04 Kveld": "2026-10-04|Kveld", "2026-10-04 2": "2026-10-04|2"} {
		d, name, ok := parseFolder(in)
		if !ok || d.Format("2006-01-02")+"|"+name != want {
			t.Errorf("parseFolder(%q) = %v %q %v", in, d, name, ok)
		}
	}
	for _, in := range []string{"2026", "notes", "2026-13-01", "2026-10-04Kveld"} {
		if _, _, ok := parseFolder(in); ok {
			t.Errorf("parseFolder(%q) should fail", in)
		}
	}
}

// A church's defaults are saved, survive a restart, and are per church; a
// run's own values win over them.
func TestSettingsPerChurch(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "main", "Taler"), 0o755)
	cfg := AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}
	a, err := NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	do(h, "POST", "/api/tenants", `{"name":"Kirke B"}`)

	type settings struct{ Meta Meta }
	if got := decode[settings](t, do(h, "GET", "/api/settings", "")); got.Meta != DefaultMeta {
		t.Fatalf("defaults: %+v", got)
	}
	if w := do(h, "PUT", "/api/settings?tenant=kirke-b", `{"meta":{"title":"{date} {name}","artist":"Kirke B","album":"{yyyy}"}}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := do(h, "PUT", "/api/settings", `{"meta":{"title":"{nope}"}}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown placeholder: %d", w.Code)
	}
	if got := decode[settings](t, do(h, "GET", "/api/settings", "")); got.Meta != DefaultMeta {
		t.Errorf("the first church changed: %+v", got)
	}
	a.Close()

	a, err = NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h = a.Handler()
	if got := decode[settings](t, do(h, "GET", "/api/settings?tenant=kirke-b", "")); got.Meta.Artist != "Kirke B" {
		t.Errorf("after restart: %+v", got)
	}

	// Tags of a run: defaults for the folder's date and name, else what was sent.
	var srv *Server
	for _, c := range a.tenants {
		if c.id == "kirke-b" {
			srv = c.srv
		}
	}
	folder := srv.root + "/2026/2026-10-04 Kveld"
	def, _, err := srv.defaultTags(folder)
	if err != nil || def != (Meta{Title: "2026-10-04 Kveld", Artist: "Kirke B", Album: "2026"}) {
		t.Errorf("defaults: %+v %v", def, err)
	}
	mine, empty := "My title", ""
	tags, note := srv.runTags(folder, runRequest{Title: &mine, Artist: &empty})
	if note != "" || tags.Title != "My title" || tags.Artist != "" || tags.Album != "2026" || tags.Date != "2026-10-04" {
		t.Errorf("run tags: %+v %q", tags, note)
	}
	if _, note := srv.runTags(srv.root+"/notes", runRequest{}); note == "" {
		t.Error("a folder without a date should say why the tags are left out")
	}
}
