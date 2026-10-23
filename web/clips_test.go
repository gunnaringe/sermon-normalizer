package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// clipServer is a church server with a folder for clips; saved is what it last stored.
func clipServer(t *testing.T, ff normalizer.FFmpeg, lib *clipLibrary) (*testServer, *clipLibrary) {
	t.Helper()
	saved := &clipLibrary{}
	ts := newTestServerWith(t, ff, func(c *Config) {
		c.ClipsDir, c.Clips = filepath.Join(t.TempDir(), clipsDir), lib
		c.SaveClips = func(l clipLibrary) error { *saved = l; return nil }
	})
	return ts, saved
}

type clipsResponse struct {
	Available bool          `json:"available"`
	Clips     []clipView    `json:"clips"`
	Pending   []pendingClip `json:"pending"`
	Intro     string        `json:"intro"`
	Outro     string        `json:"outro"`
	Pause     float64       `json:"pause"`
}

// A sermon's choice of clips is checked for its form only: a clip that is gone never loses the sermon.
func TestClipChoiceKeepsSermon(t *testing.T) {
	ts, _ := clipServer(t, normalizer.FFmpeg{}, &clipLibrary{Clips: []clipInfo{{ID: "0123456789ab", Name: "Jul"}}})
	ts.file(t, "2026-10-04/2026-10-04_raw.flac", "x")
	ts.file(t, "2026-10-11/2026-10-11_raw.flac", "x")
	saved := sermonSettings{cutting: cutting{Sections: []span{{Start: 1, End: 9}}, Cuts: []span{}}, Intro: "0123456789ab", Outro: clipDefault}
	for _, f := range []string{"2026-10-04", "2026-10-11"} {
		ts.updateState(filepath.Join(ts.root, f), func(st *folderState) error { st.Saved = &saved; return nil })
	}
	// Being edited counts too, once per sermon.
	draft := saved
	draft.Outro = "0123456789ab"
	writeDraft(filepath.Join(ts.root, "2026-10-04"), &draft)
	got := decode[clipsResponse](t, ts.do(t, "GET", "/api/clips", nil))
	if !got.Available || len(got.Clips) != 1 || got.Clips[0].Used != 2 || got.Pause != defaultClipPause {
		t.Fatalf("clips: %+v", got)
	}
	if rec := ts.do(t, "POST", "/api/clips/0123456789ab/delete", nil); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if st := readState(filepath.Join(ts.root, "2026-10-11")); st.Saved == nil || st.Saved.Intro != "0123456789ab" {
		t.Errorf("the sermon after its clip was deleted: %+v", st.Saved)
	}
	bad := saved
	bad.Intro = "../x"
	if bad.Validate() == nil {
		t.Error("a choice that is not one should not be valid")
	}
}

// The default intro or outro cannot be archived or deleted; a default must be a clip that is not archived.
func TestClipDefaults(t *testing.T) {
	a, b := "aaaaaaaaaaaa", "bbbbbbbbbbbb"
	ts, saved := clipServer(t, normalizer.FFmpeg{}, &clipLibrary{Clips: []clipInfo{{ID: a, Name: "A"}, {ID: b, Name: "B", Archived: true}}})
	if rec := ts.do(t, "PUT", "/api/clips/defaults", map[string]any{"intro": a, "outro": "", "pause": 1.5}); rec.Code != 200 {
		t.Fatalf("defaults: %d %s", rec.Code, rec.Body)
	}
	if saved.Intro != a || saved.pause() != 1.5 {
		t.Errorf("saved: %+v", saved)
	}
	for _, c := range []struct {
		method, url string
		body        any
		want        string
	}{
		{"PUT", "/api/clips/" + a, map[string]any{"archived": true}, "default intro"},
		{"POST", "/api/clips/" + a + "/delete", nil, "default intro"},
		{"PUT", "/api/clips/defaults", map[string]any{"intro": b}, "no such clip"},
		{"PUT", "/api/clips/defaults", map[string]any{"intro": a, "pause": 11}, "from 0 to 10"},
		{"PUT", "/api/clips/" + a, map[string]any{"name": " "}, "give the clip a name"},
	} {
		rec := ts.do(t, c.method, c.url, c.body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s %v: %d %s, want %q", c.method, c.url, c.body, rec.Code, rec.Body, c.want)
		}
	}
	ts.do(t, "PUT", "/api/clips/defaults", map[string]any{"intro": "", "outro": ""})
	if rec := ts.do(t, "PUT", "/api/clips/"+a, map[string]any{"archived": true, "name": "Jul  2026"}); rec.Code != 200 {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body)
	}
	if !saved.Clips[0].Archived || saved.Clips[0].Name != "Jul 2026" {
		t.Errorf("after archiving: %+v", saved.Clips[0])
	}
}

// The root church has no folder, so no clips.
func TestClipsNeedAFolder(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	if got := decode[clipsResponse](t, ts.do(t, "GET", "/api/clips", nil)); got.Available {
		t.Error("clips without a folder")
	}
	if rec := ts.do(t, "POST", "/api/clips?name=a.wav", nil); rec.Code != 400 {
		t.Errorf("upload: %d", rec.Code)
	}
}

// waitClips waits until no upload is being prepared.
func waitClips(t *testing.T, ts *testServer) clipsResponse {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		got := decode[clipsResponse](t, ts.do(t, "GET", "/api/clips", nil))
		busy := false
		for _, p := range got.Pending {
			busy = busy || p.Error == ""
		}
		if !busy {
			return got
		}
	}
	t.Fatal("the clips were not ready in time")
	return clipsResponse{}
}

func uploadClip(t *testing.T, ts *testServer, to, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, httptest.NewRequest("POST", to+"name="+url.QueryEscape(filepath.Base(path)), f))
	return rec.Code
}

// An upload becomes a clip in the background: FLAC, measured, with a version; one that is silent or not
// audio fails and can be dismissed; new audio for a clip gives it a new version.
func TestIntegrationClips(t *testing.T) {
	ff := requireFFmpeg(t)
	ts, saved := clipServer(t, ff, nil)
	src := t.TempDir()
	tone := func(name, expr string, seconds float64) string {
		p := filepath.Join(src, name)
		if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i", "aevalsrc="+expr+":s=44100:d="+strconv.FormatFloat(seconds, 'f', -1, 64), "-metadata", "title=x", p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if code := uploadClip(t, ts, "/api/clips?", tone("Jingle jul.wav", "0.3*sin(2*PI*440*t)", 3)); code != 202 {
		t.Fatalf("upload: %d", code)
	}
	uploadClip(t, ts, "/api/clips?", tone("stille.mp3", "0", 2))
	notAudio := filepath.Join(src, "tekst.mp3")
	os.WriteFile(notAudio, []byte("not audio"), 0o644)
	uploadClip(t, ts, "/api/clips?", notAudio)
	got := waitClips(t, ts)
	if len(got.Clips) != 1 || got.Clips[0].Name != "Jingle jul" || got.Clips[0].Version == "" || got.Clips[0].Seconds < 2.9 || got.Clips[0].Seconds > 3.1 {
		t.Fatalf("clips: %+v", got.Clips)
	}
	errs := map[string]string{}
	for _, p := range got.Pending {
		errs[p.Name] = p.Error
	}
	if errs["stille"] != "the clip is silent" || errs["tekst"] != "could not read the clip as audio" {
		t.Errorf("failed uploads: %v", errs)
	}
	clip := got.Clips[0]
	if len(saved.Clips) != 1 || saved.Clips[0].ID != clip.ID {
		t.Errorf("church.json: %+v", saved)
	}
	if rec := ts.do(t, "GET", "/api/clips/"+clip.ID+"/audio", nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/flac" {
		t.Errorf("audio: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, p := range got.Pending {
		ts.do(t, "POST", "/api/clips/"+p.ID+"/delete", nil)
	}
	if got := decode[clipsResponse](t, ts.do(t, "GET", "/api/clips", nil)); len(got.Pending) != 0 {
		t.Errorf("dismissed uploads are still there: %+v", got.Pending)
	}

	if code := uploadClip(t, ts, "/api/clips/"+clip.ID+"/replace?", tone("ny.flac", "0.3*sin(2*PI*330*t)", 4)); code != 202 {
		t.Fatalf("replace: %d", code)
	}
	got = waitClips(t, ts)
	if len(got.Clips) != 1 || got.Clips[0].Name != "Jingle jul" || got.Clips[0].Version == clip.Version || got.Clips[0].Seconds < 3.9 {
		t.Errorf("after replacing: %+v", got.Clips)
	}
	if des, _ := os.ReadDir(ts.clips.dir); len(des) != 1 {
		t.Errorf("left in the clips folder: %v", des)
	}
}
