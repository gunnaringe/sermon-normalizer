package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"": "", "  Kveld  ": "Kveld", "Kveld   møte": "Kveld møte", "Påske – søndag": "Påske – søndag"} {
		if got, err := cleanName(in); err != nil || got != want {
			t.Errorf("cleanName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	long := make([]rune, maxName+1)
	for i := range long {
		long[i] = 'å'
	}
	for _, bad := range []string{"a/b", `a\b`, "..", ".hidden", "a:b", "x\x07y", string(long), "a|b", `say "hi"`} {
		if got, err := cleanName(bad); err == nil {
			t.Errorf("cleanName(%q) = %q, want an error", bad, got)
		}
	}
}

// A second sermon the same day is uploaded with a name and gets its own folder.
func TestUploadNamedSermon(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	upload := func(date, sermon, data string) int {
		return chunkNamed(t, ts, date, sermon, "a.flac", data).Code
	}
	if c := upload("2026-10-04", "", "morning"); c != http.StatusCreated {
		t.Fatalf("first: %d", c)
	}
	if c := upload("2026-10-04", "", "again"); c != http.StatusConflict {
		t.Errorf("an unnamed second one: %d", c)
	}
	if c := upload("2026-10-04", "Kveld", "evening"); c != http.StatusCreated {
		t.Fatalf("named: %d", c)
	}
	if b, _ := os.ReadFile(filepath.Join(ts.root, "2026", "2026-10-04 Kveld", "2026-10-04 Kveld_raw.flac")); string(b) != "evening" {
		t.Error("named sermon missing")
	}
	if b, _ := os.ReadFile(filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04_raw.flac")); string(b) != "morning" {
		t.Error("the first sermon was touched")
	}
	if c := upload("2026-10-04", "../x", "x"); c != http.StatusBadRequest {
		t.Errorf("bad name: %d", c)
	}
	if c := upload("2026-10-04", "Kveld", "dup"); c != http.StatusConflict {
		t.Errorf("same name twice: %d", c)
	}
}

func TestRename(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	for _, f := range []string{"original/2026-10-04 2_raw.flac", "2026-10-04 2.mp3", "2026-10-04 2.flac", "notes.txt"} {
		ts.file(t, "2026/2026-10-04 2/"+f, "x")
	}
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "x")
	post := func(path, body string) int {
		return ts.do(t, "POST", "/api/rename?path="+url.QueryEscape(path), json.RawMessage(body)).Code
	}
	if c := post("2026/2026-10-04 2", `{"date":"2026-10-04","name":""}`); c != http.StatusConflict {
		t.Errorf("onto an existing sermon: %d", c)
	}
	if c := post("2026/2026-10-04 2", `{"date":"2026-10-04","name":"Kveld/x"}`); c != http.StatusBadRequest {
		t.Errorf("bad name: %d", c)
	}
	if c := post("2026/2026-10-04 2", `{"date":"nope","name":"Kveld"}`); c != http.StatusBadRequest {
		t.Errorf("bad date: %d", c)
	}
	if c := post("2026/2026-10-04 2", `{"date":"2026-10-04","name":"2"}`); c != http.StatusBadRequest {
		t.Errorf("no change: %d", c)
	}
	// New name and a new year.
	rec := ts.do(t, "POST", "/api/rename?path="+url.QueryEscape("2026/2026-10-04 2"), json.RawMessage(`{"date":"2027-01-03","name":"Nyttår"}`))
	if rec.Code != http.StatusOK || decode[map[string]string](t, rec)["path"] != "2027/2027-01-03 Nyttår" {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	dir := filepath.Join(ts.root, "2027", "2027-01-03 Nyttår")
	for _, f := range []string{"original/2027-01-03 Nyttår_raw.flac", "2027-01-03 Nyttår.mp3", "2027-01-03 Nyttår.flac", "notes.txt"} {
		if !fileExists(filepath.Join(dir, f)) {
			t.Errorf("missing %s", f)
		}
	}
	if fileExists(filepath.Join(ts.root, "2026", "2026-10-04 2")) {
		t.Error("the old folder is still there")
	}
	if e := ts.entry(dir); !e.Raw || len(e.Outputs) != 2 {
		t.Errorf("entry %+v", e)
	}
	if c := post("2026", `{"date":"2026-10-05","name":""}`); c != http.StatusBadRequest {
		t.Errorf("not a date folder: %d", c)
	}
}

func TestWindowsNames(t *testing.T) {
	for name, want := range map[string]bool{"CON": true, "nul": true, "nul.wav": true, "Com1.flac": true, "LPT9": true,
		"aux ": true, "CONSOLE": false, "com": false, "COM10": false, "2026-10-04 CON": false, "con-gress": false} {
		if got := windowsDevice(name); got != want {
			t.Errorf("windowsDevice(%q) = %v", name, got)
		}
	}
	if got, err := cleanName("Påske."); err == nil {
		t.Errorf("cleanName with a dot at the end = %q", got)
	}
	if got := cleanSourceName("nul.wav"); got != "" {
		t.Errorf("cleanSourceName(nul.wav) = %q", got)
	}
	if !reservedID("nul") || !reservedID("com3") || reservedID("norkirken") {
		t.Error("reservedID and Windows' device names")
	}

	short := filepath.Join(t.TempDir(), "2026-10-04 Kveld")
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120), "2026-10-04 "+strings.Repeat("n", 60))
	if err := folderFits(short); err != nil {
		t.Error(err)
	}
	if err := folderFits(long); (err != nil) != (runtime.GOOS == "windows") {
		t.Errorf("a folder of %d characters on %s: %v", len(long), runtime.GOOS, err)
	}
}
