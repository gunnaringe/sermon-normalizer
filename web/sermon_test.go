package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

const sermonA = `{"sections":[{"start":20,"end":80}],"cuts":[{"start":30,"end":40},{"start":60,"end":65}],"fadeIn":1,"fadeOut":2}`
const sermonB = `{"sections":[{"start":10,"end":50},{"start":60,"end":90}],"cuts":[],"fadeIn":0,"fadeOut":0}`

// start is where the first section begins.
func start(c *sermonSettings) float64 { return c.Sections[0].Start }

// recording.json keeps the saved version and the adjustments apart: the editor
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
	if c != http.StatusOK || f.Saved == nil || f.Saved.Sections[0].End != 80 || f.Adjusted != nil || f.SavedAt == nil {
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
	if c != http.StatusOK || f.Saved == nil || start(f.Saved) != 20 || f.Adjusted == nil || start(f.Adjusted) != 10 || len(f.Adjusted.Sections) != 2 {
		t.Fatalf("adjust again: %d %+v", c, f)
	}
	data, _ := os.ReadFile(filepath.Join(ts.root, folder, "recording.json"))
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
	if c != http.StatusOK || f.Adjusted != nil || f.Saved == nil || start(f.Saved) != 20 {
		t.Errorf("revert: %d %+v", c, f)
	}

	// Nothing invalid is kept.
	for name, body := range map[string]string{
		"no sections":   `{"sections":[],"cuts":[]}`,
		"backwards cut": `{"sections":[{"start":20,"end":80}],"cuts":[{"start":40,"end":30}]}`,
		"end first":     `{"sections":[{"start":50,"end":20}],"cuts":[]}`,
		"negative fade": `{"sections":[{"start":20,"end":80}],"cuts":[],"fadeIn":-1}`,
		"long label":    `{"sections":[{"start":20,"end":80,"label":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}],"cuts":[]}`,
		"unknown field": `{"sections":[{"start":20,"end":80}],"cuts":[],"x":1}`,
		"start and end": `{"start":20,"end":80,"cuts":[]}`,
	} {
		if c, _ := call("PUT", "", body); c != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, c)
		}
	}
	if got := info(); got.Saved == nil || start(got.Saved) != 20 || got.Adjusted != nil {
		t.Errorf("invalid changes altered the file: %+v", got)
	}

	// Sections are kept sorted and merged where they overlap, so a reorder is no change.
	call("PUT", "", sermonA)
	call("POST", "/save", "")
	if _, f = call("PUT", "", `{"sections":[{"start":50,"end":80},{"start":20,"end":55}],"cuts":[{"start":60,"end":65},{"start":30,"end":40}],"fadeIn":1,"fadeOut":2}`); f.Adjusted != nil {
		t.Errorf("the saved version in another order: %+v", f.Adjusted)
	}

	// One from before there were sections is ignored: the sermon is selected again.
	os.WriteFile(filepath.Join(ts.root, folder, "recording.json"), []byte(`{"saved":{"start":20,"end":80,"cuts":[]},"adjusted":null}`), 0o644)
	if got := info(); got != nil {
		t.Errorf("old file: %+v", got)
	}
	call("PUT", "", sermonA)
	call("POST", "/save", "")

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
	os.WriteFile(filepath.Join(ts.root, "2026/2026-10-11 Kveld/recording.json"), []byte("{oops"), 0o644)
	if got := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path="+url.QueryEscape("2026/2026-10-11 Kveld"), nil)).Sermon; got != nil {
		t.Errorf("damaged file: %+v", got)
	}
}

// recording.json is changed by several things at once (the editor, the
// analysis, finished runs): none may undo another's change.
func TestStateUpdatesDoNotLoseChanges(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	folder := filepath.Join(ts.root, "2026-10-04")
	os.MkdirAll(folder, 0o755)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts.updateState(folder, func(st *folderState) error {
				st.Outputs[fmt.Sprint(i)] = outputMade{From: "x"}
				return nil
			})
		}()
	}
	wg.Wait()
	if got := len(readState(folder).Outputs); got != 20 {
		t.Errorf("%d of 20 changes kept", got)
	}
	if got := folderOfRecording(filepath.Join(folder, "original", "2026-10-04_raw.flac")); got != folder {
		t.Errorf("folder of a recording in original/: %s", got)
	}
}

// A cut that is not in a section is kept, and changes nothing; one that sticks
// out of the sermon is cut at the edge. Labels are tidied and change nothing.
func TestCuttingOutsideSections(t *testing.T) {
	c := cutting{
		Sections: []span{{Start: 20, End: 80, Label: "  The   sermon "}},
		Cuts:     []span{{Start: 0, End: 10, Label: "Song"}, {Start: 70, End: 90}, {Start: 30, End: 40}},
	}
	c.normalize()
	o := normalizer.DefaultOptions()
	if err := c.apply(&o); err != nil {
		t.Fatal(err)
	}
	want := []normalizer.Span{{Start: 30, End: 40}, {Start: 70, End: 80}}
	if !reflect.DeepEqual(o.Cuts, want) || o.Start != 20 || o.End != 80 {
		t.Errorf("start %v end %v cuts %v, want 20 80 %v", o.Start, o.End, o.Cuts, want)
	}
	if c.Sections[0].Label != "The sermon" || len(c.Cuts) != 3 || c.Cuts[0].Label != "Song" {
		t.Errorf("kept: %+v", c)
	}
	// The files do not depend on the labels or on a cut outside.
	plain := cutting{Sections: []span{{Start: 20, End: 80}}, Cuts: []span{{Start: 30, End: 40}, {Start: 70, End: 80}}}
	plain.normalize()
	o2 := normalizer.DefaultOptions()
	if err := plain.apply(&o2); err != nil || cutOf(o) != cutOf(o2) {
		t.Errorf("the cut differs: %v", err)
	}
}

// Sections that touch stay two (a pause can be between them); pauses go to the pipeline; and one makes
// the files differ, where none leaves the fingerprints as they were.
func TestCuttingPauses(t *testing.T) {
	c := cutting{
		Sections: []span{{Start: 10, End: 20, Pause: 2}, {Start: 20, End: 30, Label: "b", Pause: 9}, {Start: 40, End: 60}},
		Cuts:     []span{{Start: 25, End: 28, Pause: 1.5}, {Start: 5, End: 8, Pause: 4}},
	}
	c.normalize()
	if len(c.Sections) != 3 {
		t.Fatalf("touching sections were merged: %+v", c.Sections)
	}
	o := normalizer.DefaultOptions()
	if err := c.apply(&o); err != nil {
		t.Fatal(err)
	}
	// 20-30 touches 10-20: no gap there; the gap 30-40 and the cuts are cuts. A pause where a section ends or a
	// cut starts inside the sermon; the cut before the sermon has none.
	wantCuts := []normalizer.Span{{Start: 30, End: 40}, {Start: 25, End: 28}}
	wantPauses := []normalizer.Pause{{At: 20, Seconds: 2}, {At: 30, Seconds: 9}, {At: 25, Seconds: 1.5}}
	if o.Start != 10 || o.End != 60 || !reflect.DeepEqual(o.Cuts, wantCuts) || !reflect.DeepEqual(o.Pauses, wantPauses) {
		t.Errorf("options: %v-%v cuts %v pauses %v", o.Start, o.End, o.Cuts, o.Pauses)
	}
	if err := o.Validate(); err != nil {
		t.Error(err)
	}

	// Sections that overlap merge; the pause is the one of the section that ends last.
	m := cutting{Sections: []span{{Start: 10, End: 30, Pause: 1}, {Start: 20, End: 40, Pause: 3}, {Start: 25, End: 35, Pause: 7}}}
	m.normalize()
	if len(m.Sections) != 1 || m.Sections[0].End != 40 || m.Sections[0].Pause != 3 {
		t.Errorf("merged: %+v", m.Sections)
	}

	for _, p := range []float64{-1, 31, math.NaN()} {
		bad := cutting{Sections: []span{{Start: 10, End: 20, Pause: p}}, Cuts: []span{}}
		bad.normalize()
		if err := bad.apply(&o); err == nil {
			t.Errorf("pause %v accepted", p)
		}
	}

	// The fingerprints: the same without pauses as before they existed, different with one.
	plain := normalizer.DefaultOptions()
	plain.Start, plain.End = 10, 60
	old := func(v any) string {
		data, _ := json.Marshal(v)
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:8])
	}
	if got, want := cutOf(plain), old(struct {
		Start, End, FadeIn, FadeOut float64
		Cuts                        []normalizer.Span
	}{10, 60, 0, 0, nil}); got != want {
		t.Errorf("cutOf without pauses changed: %s, was %s", got, want)
	}
	withPause := plain
	withPause.Pauses = []normalizer.Pause{{At: 30, Seconds: 2}}
	if cutOf(plain) == cutOf(withPause) || madeFrom(plain, "mp3") == madeFrom(withPause, "mp3") {
		t.Error("a pause does not change the fingerprints")
	}
}
