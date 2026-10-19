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

const sermonA = `{"start":20,"end":80,"cuts":[{"start":30,"end":40},{"start":60,"end":65}],"fadeIn":1,"fadeOut":2}`
const sermonB = `{"start":10,"end":90,"cuts":[],"fadeIn":0,"fadeOut":0}`

// sermon.json keeps the saved version and the adjustments apart: the editor
// keeps adjusting, Save makes them the saved version, Revert drops them.
func TestSermonSavedAndAdjusted(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "x")
	const folder = "2026/2026-10-04"
	call := func(method, endpoint, body string) (int, sermonFile) {
		var b any
		if body != "" {
			b = json.RawMessage(body)
		}
		rec := ts.do(t, method, "/api/sermon"+endpoint+"?path="+url.QueryEscape(folder), b)
		var f sermonFile
		json.Unmarshal(rec.Body.Bytes(), &f)
		return rec.Code, f
	}
	info := func() *sermonFile {
		return decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path="+url.QueryEscape(folder), nil)).Sermon
	}

	if info() != nil {
		t.Error("nothing kept yet, but the folder has a sermon")
	}
	if c, _ := call("POST", "/save", ""); c != http.StatusBadRequest {
		t.Errorf("save with nothing: %d", c)
	}
	if c, _ := call("POST", "/revert", ""); c != http.StatusBadRequest {
		t.Errorf("revert with nothing: %d", c)
	}

	// Adjusting before anything was saved keeps only the adjustments.
	c, f := call("PUT", "", sermonA)
	if c != http.StatusOK || f.Saved != nil || f.Adjusted == nil || len(f.Adjusted.Cuts) != 2 || f.Adjusted.FadeOut != 2 {
		t.Fatalf("adjust: %d %+v", c, f)
	}
	// Saving makes them the saved version.
	c, f = call("POST", "/save", "")
	if c != http.StatusOK || f.Saved == nil || f.Saved.End != 80 || f.Adjusted != nil || f.SavedAt == nil {
		t.Fatalf("save: %d %+v", c, f)
	}
	if got := info(); got == nil || got.Saved == nil || got.Adjusted != nil {
		t.Errorf("info after save: %+v", got)
	}
	// Saving again is harmless.
	if c, _ = call("POST", "/save", ""); c != http.StatusOK {
		t.Errorf("save again: %d", c)
	}

	// Adjustments sit next to the saved version, which stays as it was.
	c, f = call("PUT", "", sermonB)
	if c != http.StatusOK || f.Saved == nil || f.Saved.Start != 20 || f.Adjusted == nil || f.Adjusted.Start != 10 {
		t.Fatalf("adjust again: %d %+v", c, f)
	}
	data, _ := os.ReadFile(filepath.Join(ts.root, folder, "sermon.json"))
	if !strings.Contains(string(data), `"saved"`) || !strings.Contains(string(data), `"adjusted"`) {
		t.Errorf("file: %s", data)
	}
	// Adjusting back to the saved version leaves no adjustments.
	if _, f = call("PUT", "", sermonA); f.Adjusted != nil || f.Saved == nil {
		t.Errorf("adjusted to the saved version: %+v", f)
	}
	// Reverting drops the adjustments.
	call("PUT", "", sermonB)
	c, f = call("POST", "/revert", "")
	if c != http.StatusOK || f.Adjusted != nil || f.Saved == nil || f.Saved.Start != 20 {
		t.Errorf("revert: %d %+v", c, f)
	}

	// Nothing invalid is kept.
	for name, body := range map[string]string{
		"no end":        `{"start":0,"end":0,"cuts":[]}`,
		"cut outside":   `{"start":20,"end":80,"cuts":[{"start":70,"end":90}]}`,
		"backwards cut": `{"start":20,"end":80,"cuts":[{"start":40,"end":30}]}`,
		"end first":     `{"start":50,"end":20,"cuts":[]}`,
		"negative fade": `{"start":20,"end":80,"cuts":[],"fadeIn":-1}`,
		"unknown field": `{"start":20,"end":80,"cuts":[],"x":1}`,
	} {
		if c, _ := call("PUT", "", body); c != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, c)
		}
	}
	if got := info(); got.Saved == nil || got.Saved.Start != 20 || got.Adjusted != nil {
		t.Errorf("invalid changes altered the file: %+v", got)
	}

	// A rename takes it along; a damaged file is ignored.
	call("PUT", "", sermonB)
	rec := ts.do(t, "POST", "/api/rename?path="+url.QueryEscape(folder), json.RawMessage(`{"date":"2026-10-11","name":"Kveld"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	renamed := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path="+url.QueryEscape("2026/2026-10-11 Kveld"), nil)).Sermon
	if renamed == nil || renamed.Saved == nil || renamed.Adjusted == nil {
		t.Errorf("after rename: %+v", renamed)
	}
	os.WriteFile(filepath.Join(ts.root, "2026/2026-10-11 Kveld/sermon.json"), []byte("{oops"), 0o644)
	if got := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path="+url.QueryEscape("2026/2026-10-11 Kveld"), nil)).Sermon; got != nil {
		t.Errorf("damaged file: %+v", got)
	}
}

// The file an earlier version wrote counts as the saved version, and is replaced
// by sermon.json at the next change.
func TestSermonFromEarlierCutsFile(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "x")
	ts.file(t, "2026/2026-10-04/2026-10-04_cuts.json", `{"start":20,"end":80,"cuts":[{"start":30,"end":40}]}`)
	got := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026/2026-10-04", nil)).Sermon
	if got == nil || got.Saved == nil || got.Saved.End != 80 || len(got.Saved.Cuts) != 1 || got.Adjusted != nil {
		t.Fatalf("earlier file: %+v", got)
	}
	rec := ts.do(t, "PUT", "/api/sermon?path=2026/2026-10-04", json.RawMessage(sermonB))
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d", rec.Code)
	}
	if fileExists(filepath.Join(ts.root, "2026/2026-10-04/2026-10-04_cuts.json")) || !fileExists(filepath.Join(ts.root, "2026/2026-10-04/sermon.json")) {
		t.Error("the earlier file should be replaced by sermon.json")
	}
}
