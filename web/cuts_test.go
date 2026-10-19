package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// The selection and cuts are saved in the folder, come back with the folder
// info, and follow a rename.
func TestSavedCuts(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "x")
	put := func(body string) int {
		return ts.do(t, "PUT", "/api/cuts?path="+url.QueryEscape("2026/2026-10-04"), json.RawMessage(body)).Code
	}
	info := func(path string) folderInfo {
		return decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path="+url.QueryEscape(path), nil))
	}

	if info("2026/2026-10-04").Saved != nil {
		t.Error("nothing saved yet, but the folder has saved cuts")
	}
	if c := put(`{"start":20,"end":80,"cuts":[{"start":30,"end":40},{"start":60,"end":65}]}`); c != http.StatusOK {
		t.Fatalf("save: %d", c)
	}
	got := info("2026/2026-10-04").Saved
	if got == nil || got.Start != 20 || got.End != 80 || len(got.Cuts) != 2 || got.Cuts[1] != (cutSpan{60, 65}) {
		t.Errorf("saved %+v", got)
	}
	file := filepath.Join(ts.root, "2026/2026-10-04/2026-10-04_cuts.json")
	if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), `"end": 80`) {
		t.Errorf("file: %q, %v", data, err)
	}
	if left, _ := filepath.Glob(filepath.Join(ts.root, "2026/2026-10-04/.*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}

	// Nothing invalid is saved, and the old one stays.
	for name, body := range map[string]string{
		"no end":           `{"start":0,"end":0,"cuts":[]}`,
		"cut outside":      `{"start":20,"end":80,"cuts":[{"start":70,"end":90}]}`,
		"backwards":        `{"start":20,"end":80,"cuts":[{"start":40,"end":30}]}`,
		"unknown field":    `{"start":20,"end":80,"cuts":[],"x":1}`,
		"end before start": `{"start":50,"end":20,"cuts":[]}`,
	} {
		if c := put(body); c != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, c)
		}
	}
	if got := info("2026/2026-10-04").Saved; got == nil || got.End != 80 {
		t.Errorf("an invalid save changed the file: %+v", got)
	}
	// Saving without cuts reads back as an empty list.
	if c := put(`{"start":10,"end":90}`); c != http.StatusOK {
		t.Fatalf("no cuts: %d", c)
	}
	if got := info("2026/2026-10-04").Saved; got == nil || got.Cuts == nil || len(got.Cuts) != 0 {
		t.Errorf("no cuts: %+v", got)
	}

	// A rename takes the file along.
	if c := put(`{"start":20,"end":80,"cuts":[{"start":30,"end":40}]}`); c != http.StatusOK {
		t.Fatal(c)
	}
	rec := ts.do(t, "POST", "/api/rename?path="+url.QueryEscape("2026/2026-10-04"), json.RawMessage(`{"date":"2026-10-11","name":"Kveld"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if got := info("2026/2026-10-11 Kveld").Saved; got == nil || len(got.Cuts) != 1 {
		t.Errorf("after rename: %+v", got)
	}

	// A damaged file is ignored, and replaced by the next save.
	os.WriteFile(filepath.Join(ts.root, "2026/2026-10-11 Kveld/2026-10-11 Kveld_cuts.json"), []byte("{oops"), 0o644)
	if got := info("2026/2026-10-11 Kveld").Saved; got != nil {
		t.Errorf("damaged file: %+v", got)
	}
}
