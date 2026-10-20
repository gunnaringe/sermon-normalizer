package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

type testServer struct {
	*Server
	root, inbox string
	h           http.Handler
}

func newTestServer(t *testing.T, ff normalizer.FFmpeg) *testServer {
	return newTestServerWith(t, ff, nil)
}

func newTestServerWith(t *testing.T, ff normalizer.FFmpeg, configure func(*Config)) *testServer {
	t.Helper()
	root, inbox := t.TempDir(), t.TempDir()
	if ff.Bin == "" {
		ff.Bin = filepath.Join(t.TempDir(), "no-ffmpeg") // tag reading just fails
	}
	cfg := Config{Root: root, Inbox: inbox, CacheDir: t.TempDir(), FFmpeg: ff, Logger: log.New(testLog{t}, "", 0)}
	if configure != nil {
		configure(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return &testServer{Server: s, root: s.root, inbox: s.cfg.Inbox, h: s.Handler()}
}

// testLog shows the server's log with the test's output (on failure or -v).
type testLog struct{ t *testing.T }

func (l testLog) Write(b []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(b)))
	return len(b), nil
}

// file creates a file under the root (slash path) with content.
func (ts *testServer) file(t *testing.T, rel, content string) string {
	t.Helper()
	p := filepath.Join(ts.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (ts *testServer) do(t *testing.T, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, url, rd)
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestResolve(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/x.flac", "x")
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(ts.root, "escape"))
	os.Symlink(filepath.Join(ts.root, "2026"), filepath.Join(ts.root, "inside"))

	for _, ok := range []string{"", "2026", "2026/2026-10-04", "/2026", "inside", "inside/new/folder", "not-yet"} {
		if _, err := ts.resolve(ok); err != nil {
			t.Errorf("resolve(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"escape", "escape/x", "escape/new/folder", "..", "../../etc", "2026/../2026",
		".cache", "2026/.2026-10-04.master.flac", "2026/x.partial/.y"} {
		if _, err := ts.resolve(bad); err == nil {
			t.Errorf("resolve(%q) should fail", bad)
		}
	}
}

func TestFolders(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.wav", "x")
	ts.file(t, "2026/2026-09-27/original/2026-09-27_raw.flac", "x")
	ts.file(t, "2026/2026-09-27/2026-09-27.mp3", "x")
	os.MkdirAll(filepath.Join(ts.root, "2026", "notes"), 0o755)
	os.MkdirAll(filepath.Join(ts.root, "2026", ".hidden"), 0o755)

	rec := ts.do(t, "GET", "/api/folders?path=2026", nil)
	got := decode[struct {
		Path    string
		Parent  *string
		Entries []folderEntry
	}](t, rec)
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "2026-10-04,2026-09-27,notes" || got.Parent == nil || *got.Parent != "" {
		t.Fatalf("got %+v", got)
	}
	e := got.Entries[1]
	if !e.Raw || strings.Join(e.Outputs, ",") != "mp3" || !e.Pending || e.Path != "2026/2026-09-27" {
		t.Errorf("entry %+v", e)
	}
	if rec := ts.do(t, "GET", "/api/folders?path=nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing folder: %d", rec.Code)
	}
	if rec := ts.do(t, "GET", "/api/folders?path=.hidden", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("hidden folder: %d", rec.Code)
	}
}

func TestFolderInfo(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026-10-04/2026-10-04_raw.flac", "raw")
	ts.file(t, "2026-10-04/2026-10-04.opus", "opus")
	info := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-04", nil))
	if info.Date != "2026-10-04" || info.Raw == nil || info.Raw.Name != "2026-10-04_raw.flac" ||
		len(info.Outputs) != 1 || info.Outputs[0].Format != "opus" || info.Outputs[0].Path != "2026-10-04/2026-10-04.opus" {
		t.Errorf("info %+v", info)
	}
}

func defaultRequest() runRequest { return requestFrom(normalizer.DefaultOptions()) }

func TestCheck(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "d/2026-10-04/2026-10-04_raw.wav", "x")
	ts.file(t, "d/2026-10-04/2026-10-04.mp3", "x")
	url := "/api/check?path=d/2026-10-04"

	q := defaultRequest()
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "select where the sermon") {
		t.Errorf("no selection: %+v", pl)
	}
	q.Sections, q.FadeIn = []span{{21, 79}}, 1
	pl := decode[plan](t, ts.do(t, "POST", url, q))
	if parsed := ts.check(filepath.Join(ts.root, "d/2026-10-04"), q).opt; parsed.Start != 21 || parsed.End != 79 || parsed.FadeIn != 1 {
		t.Errorf("parsed options %+v", parsed)
	}
	if pl.Error != "" || len(pl.Notices) != 0 ||
		pl.Outputs["mp3"] != (outputState{Exists: true}) || pl.Outputs["flac"].Exists {
		t.Errorf("plan %+v", pl)
	}

	// A file made by a run is known; it is out of date once the page would
	// make it differently, and up to date again when it would not.
	ts.recordOutputs(ts.check(filepath.Join(ts.root, "d/2026-10-04"), q), time.Now().Add(-time.Hour))
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); pl.Outputs["mp3"] != (outputState{Exists: true, Known: true}) {
		t.Errorf("just made: %+v", pl.Outputs)
	}
	for name, change := range map[string]func(*runRequest){
		"a cut":     func(r *runRequest) { r.Cuts = []span{{40, 45}} },
		"stereo":    func(r *runRequest) { r.Stereo = true },
		"a tag":     func(r *runRequest) { r.Title = ptr("Another") },
		"a bitrate": func(r *runRequest) { r.Mp3Bitrate = "192k" },
	} {
		r := q
		change(&r)
		if pl := decode[plan](t, ts.do(t, "POST", url, r)); !pl.Outputs["mp3"].Stale {
			t.Errorf("%s: want out of date, got %+v", name, pl.Outputs)
		}
	}
	r := q
	r.Flac, r.OpusBitrate, r.Force = true, "32k", true // other outputs and Force do not matter to the MP3
	if pl := decode[plan](t, ts.do(t, "POST", url, r)); pl.Outputs["mp3"].Stale {
		t.Errorf("unrelated changes: %+v", pl.Outputs)
	}
	q.Flac, q.Mp3, q.Opus = false, false, false
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "at least one output") {
		t.Errorf("no outputs: %+v", pl)
	}
	// A folder without a recording cannot be run; one whose recording is in original/ can.
	q = defaultRequest()
	q.Sections = []span{{30, 3600}}
	if pl := decode[plan](t, ts.do(t, "POST", "/api/check?path=d/2026-10-11", q)); pl.Error == "" {
		t.Errorf("no folder: %+v", pl)
	}
	ts.file(t, "d/2026-10-11/original/2026-10-11_raw.flac", "x")
	if pl := decode[plan](t, ts.do(t, "POST", "/api/check?path=d/2026-10-11", q)); pl.Error != "" {
		t.Errorf("recording in original/: %+v", pl)
	}
	if rec := ts.do(t, "POST", url, map[string]any{"bogus": 1}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
}

func TestCheckCuts(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "d/2026-10-04/2026-10-04_raw.flac", "x")
	url := "/api/check?path=d/2026-10-04"
	folder := filepath.Join(ts.root, "d/2026-10-04")

	q := defaultRequest()
	q.Sections = []span{{20, 80}}
	q.Cuts = []span{{60, 65}, {30, 40}}
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); pl.Error != "" || len(pl.Notices) != 0 {
		t.Errorf("cuts inside the sermon: %+v", pl)
	}
	if opt := ts.check(folder, q).opt; opt.Start != 20 || opt.End != 80 || !reflect.DeepEqual(opt.Cuts, []normalizer.Span{{Start: 30, End: 40}, {Start: 60, End: 65}}) {
		t.Errorf("parsed cuts %+v", opt)
	}
	q.Cuts = []span{{70, 90}}
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "inside the sermon") {
		t.Errorf("cut past the end: %+v", pl)
	}

	// Several sections: the gaps between them are left out like cuts, and
	// sections that overlap are one.
	q.Sections = []span{{60, 90}, {10, 30}, {25, 40}}
	q.Cuts = []span{{70, 75}}
	pl := ts.check(folder, q)
	want := []normalizer.Span{{Start: 40, End: 60}, {Start: 70, End: 75}}
	if pl.Error != "" || pl.opt.Start != 10 || pl.opt.End != 90 || !reflect.DeepEqual(pl.opt.Cuts, want) {
		t.Errorf("sections: %+v %+v", pl, pl.opt)
	}
	// A cut between sections leaves nothing out: allowed, with a notice.
	q.Cuts = []span{{45, 50}}
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); pl.Error != "" || len(pl.Notices) != 1 || !strings.Contains(pl.Notices[0], "between two sections") {
		t.Errorf("cut in a gap: %+v", pl)
	}
	q.Sections = []span{{50, 40}}
	q.Cuts = nil
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "section must end after it starts") {
		t.Errorf("backwards section: %+v", pl)
	}
	q.Sections = []span{{20, 80}}
	q.Cuts = []span{{40, 30}}
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "must end after it starts") {
		t.Errorf("backwards cut: %+v", pl)
	}
}

// chunk sends one upload chunk straight to a date folder.
func chunk(t *testing.T, ts *testServer, date, name string, size, offset int64, data string, modified time.Time) *httptest.ResponseRecorder {
	t.Helper()
	url := fmt.Sprintf("/api/upload?date=%s&name=%s&size=%d&offset=%d", date, url.QueryEscape(name), size, offset)
	if !modified.IsZero() {
		url += "&modified=" + strconv.FormatInt(modified.UnixMilli(), 10)
	}
	req := httptest.NewRequest("POST", url, strings.NewReader(data))
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	return rec
}

// chunkNamed uploads a whole (small) recording for a named sermon in one chunk.
func chunkNamed(t *testing.T, ts *testServer, date, sermon, file, data string) *httptest.ResponseRecorder {
	t.Helper()
	u := fmt.Sprintf("/api/upload?date=%s&sermon=%s&name=%s&size=%d&offset=0", date, url.QueryEscape(sermon), url.QueryEscape(file), len(data))
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, httptest.NewRequest("POST", u, strings.NewReader(data)))
	return rec
}

func TestUploadInChunks(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	recorded := time.Date(2026, 10, 4, 11, 30, 0, 0, time.Local)
	if rec := chunk(t, ts, "2026-10-04", "ZOOM0001.WAV", 8, 0, "abc", recorded); rec.Code != http.StatusOK || decode[map[string]int64](t, rec)["offset"] != 3 {
		t.Fatalf("first chunk: %d %s", rec.Code, rec.Body)
	}
	// A chunk at the wrong offset is told where to continue; a second
	// upload to the same date waits for the first.
	if rec := chunk(t, ts, "2026-10-04", "ZOOM0001.WAV", 8, 5, "fgh", recorded); rec.Code != http.StatusConflict || decode[map[string]any](t, rec)["offset"] != 3.0 {
		t.Errorf("wrong offset: %d %s", rec.Code, rec.Body)
	}
	if rec := chunk(t, ts, "2026-10-04", "other.wav", 8, 0, "xyz", recorded); rec.Code != http.StatusConflict {
		t.Errorf("parallel upload: %d %s", rec.Code, rec.Body)
	}
	if rec := chunk(t, ts, "2026-10-04", "ZOOM0001.WAV", 8, 3, "defghij", recorded); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("past the size: %d %s", rec.Code, rec.Body)
	}
	rec := chunk(t, ts, "2026-10-04", "ZOOM0001.WAV", 8, 3, "defgh", recorded)
	if rec.Code != http.StatusCreated || decode[map[string]any](t, rec)["path"] != "2026/2026-10-04" {
		t.Fatalf("last chunk: %d %s", rec.Code, rec.Body)
	}
	raw := filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04_raw.wav")
	if b, _ := os.ReadFile(raw); string(b) != "abcdefgh" {
		t.Errorf("uploaded %q", b)
	}
	if st, _ := os.Stat(raw); !st.ModTime().Equal(recorded) {
		t.Errorf("mtime %v, want the browser's lastModified", st.ModTime())
	}
	entries, _ := os.ReadDir(filepath.Dir(raw))
	if len(entries) != 1 {
		t.Errorf("only the recording should be left: %v", entries)
	}
	if rec := chunk(t, ts, "2026-10-04", "again.flac", 3, 0, "abc", time.Time{}); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "already has a raw recording") {
		t.Errorf("second raw: %d %s", rec.Code, rec.Body)
	}
	for _, url := range []string{
		"/api/upload?date=2026-10-05&name=talk.mp3&size=3&offset=0",
		"/api/upload?date=05.10.2026&name=a.wav&size=3&offset=0",
		"/api/upload?date=2026-10-05&name=a.wav&size=0&offset=0",
		"/api/upload?date=2026-10-05&name=..%2Fa.wav&size=3&offset=0",
	} {
		if rec := ts.do(t, "POST", url, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", url, rec.Code)
		}
	}
	// A broken upload continues where the server has it.
	chunk(t, ts, "2026-10-06", "b.flac", 6, 0, "abc", time.Time{})
	if rec := chunk(t, ts, "2026-10-06", "b.flac", 6, 3, "def", time.Time{}); rec.Code != http.StatusCreated {
		t.Errorf("resume: %d %s", rec.Code, rec.Body)
	}
}

func TestInboxIngest(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ts.inbox, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Gudstjeneste 2026-09-27.flac", "audio")
	write("again 2026-09-27.flac", "audio2")
	write("ZOOM0001.flac", "audio3")
	recorded := time.Date(2026, 10, 4, 11, 30, 0, 0, time.Local)
	os.Chtimes(filepath.Join(ts.inbox, "ZOOM0001.flac"), recorded, recorded)

	// The date comes from the name, else from the file's own date.
	for _, name := range []string{"Gudstjeneste 2026-09-27.flac", "ZOOM0001.flac"} {
		if err := ts.ingest(name); err != nil {
			t.Fatal(err)
		}
		if fileExists(filepath.Join(ts.inbox, name)) {
			t.Errorf("%s should have left the inbox", name)
		}
	}
	for rel, want := range map[string]string{"2026/2026-09-27/2026-09-27_raw.flac": "audio", "2026/2026-10-04/2026-10-04_raw.flac": "audio3"} {
		if b, _ := os.ReadFile(filepath.Join(ts.root, filepath.FromSlash(rel))); string(b) != want {
			t.Errorf("%s: %q", rel, b)
		}
	}
	// A second recording for the same date gets its own folder.
	if err := ts.ingest("again 2026-09-27.flac"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(ts.root, "2026", "2026-09-27 2", "2026-09-27 2_raw.flac")); string(b) != "audio2" {
		t.Error("second recording of the day missing")
	}
	entry := ts.entry(filepath.Join(ts.root, "2026", "2026-09-27 2"))
	if !entry.Date || !entry.Raw {
		t.Errorf("entry %+v", entry)
	}
	if err := ts.ingest("gone.flac"); err != nil {
		t.Errorf("a file taken meanwhile is not an error: %v", err)
	}
}

func TestDateGuesses(t *testing.T) {
	for name, want := range map[string]string{"2026-10-04_raw.wav": "2026-10-04", "rec_20260927.flac": "2026-09-27",
		"ZOOM0001.WAV": "", "20261399.wav": "", "x 2026.10.04.wav": "2026-10-04"} {
		if got := dateFromName(name); got != want {
			t.Errorf("dateFromName(%q) = %q, want %q", name, got, want)
		}
	}
	header := "Input #0, wav, from 'a.wav':\n  Metadata:\n    date            : 2026-10-04\n    creation_time   : 10:59:00\n"
	if got := dateFromMetadata(header); got != "2026-10-04" {
		t.Errorf("BWF: %q", got)
	}
	if got := dateFromMetadata("  Metadata:\n    creation_time   : 2026-09-20T10:00:00Z\n"); got != "2026-09-20" {
		t.Errorf("creation_time: %q", got)
	}
}

func TestCrossOriginPostRefused(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	req := httptest.NewRequest("POST", "/api/check?path=", strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", rec.Code)
	}
}

func TestFile(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026-10-04/2026-10-04.mp3", "0123456789")
	ts.file(t, "2026-10-04/notes.txt", "secret")
	req := httptest.NewRequest("GET", "/api/file?path=2026-10-04/2026-10-04.mp3", nil)
	req.Header.Set("Range", "bytes=2-4")
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("range: %d %q", rec.Code, rec.Body)
	}
	if rec := ts.do(t, "GET", "/api/file?path=2026-10-04/notes.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("non-audio: %d", rec.Code)
	}
	if rec := ts.do(t, "GET", "/api/file?path=2026-10-04/2026-10-04.mp3&download=1", nil); !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Error("download should be an attachment")
	}
}

// One run per folder at a time; a queued run can be cancelled.
func TestRunQueueAndCancel(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026-10-04/2026-10-04_raw.wav", "x")
	ts.jobs.sem <- struct{}{} // another encode is running: this one stays queued
	defer func() { <-ts.jobs.sem }()

	q := defaultRequest()
	q.Sections = []span{{30, 3600}}
	rec := ts.do(t, "POST", "/api/run?path=2026-10-04", q)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", rec.Code, rec.Body)
	}
	snap := decode[jobSnapshot](t, rec)
	if snap.State != "queued" || snap.Folder != "2026-10-04" {
		t.Errorf("snapshot %+v", snap)
	}
	if rec := ts.do(t, "POST", "/api/run?path=2026-10-04", q); rec.Code != http.StatusConflict {
		t.Errorf("second run: %d", rec.Code)
	}
	if e := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-04", nil)); e.Job == nil || e.Job.ID != snap.ID {
		t.Errorf("folder should show the job: %+v", e.Job)
	}
	if got := decode[jobSnapshot](t, ts.do(t, "POST", "/api/jobs/"+snap.ID+"/cancel", nil)); got.State != "cancelled" {
		t.Errorf("cancel: %+v", got)
	}
	// The event stream of a finished job sends the final state and ends.
	rec = ts.do(t, "GET", "/api/jobs/"+snap.ID+"/events", nil)
	if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(rec.Body.String(), `"state":"cancelled"`) {
		t.Errorf("events: %q", rec.Body)
	}
	if rec := ts.do(t, "GET", "/api/jobs/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown job: %d", rec.Code)
	}
	// Bad requests never start anything.
	if rec := ts.do(t, "POST", "/api/run?path=2026-10-04", defaultRequest()); rec.Code != http.StatusBadRequest {
		t.Errorf("no selection: %d", rec.Code)
	}
}

func TestStaticAndConfig(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	if rec := ts.do(t, "GET", "/", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Errorf("index: %d", rec.Code)
	}
	cfg := decode[struct {
		Inbox    bool
		Defaults struct{ Mono, Stereo runRequest }
	}](t, ts.do(t, "GET", "/api/config", nil))
	if !cfg.Inbox || cfg.Defaults.Mono.TargetLUFS != -19 || cfg.Defaults.Stereo.TargetLUFS != -16 || cfg.Defaults.Stereo.Mp3Bitrate != "128k" {
		t.Errorf("config %+v", cfg)
	}
}

// Make builds one missing format from the saved version, and only that.
func TestMakeFormat(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026-10-04/2026-10-04_raw.wav", "x")
	mk := func(format string) *httptest.ResponseRecorder {
		return ts.do(t, "POST", "/api/make?path=2026-10-04&format="+format, nil)
	}
	if rec := mk("wav"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown format: %d", rec.Code)
	}
	if rec := mk("mp3"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not saved") {
		t.Errorf("nothing saved: %d %s", rec.Code, rec.Body)
	}
	saved := sermonSettings{cutting: cutting{Sections: []span{{30, 3600}}, Cuts: []span{}}}
	if _, err := ts.updateState(filepath.Join(ts.root, "2026-10-04"), func(st *folderState) error { st.Saved = &saved; return nil }); err != nil {
		t.Fatal(err)
	}
	ts.jobs.sem <- struct{}{} // another encode is running: this one stays queued
	defer func() { <-ts.jobs.sem }()
	rec := mk("mp3")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("make: %d %s", rec.Code, rec.Body)
	}
	snap := decode[jobSnapshot](t, rec)
	if rec := mk("opus"); rec.Code != http.StatusConflict {
		t.Errorf("second make while one runs: %d", rec.Code)
	}
	ts.do(t, "POST", "/api/jobs/"+snap.ID+"/cancel", nil)
}

func ptr[T any](v T) *T { return &v }
