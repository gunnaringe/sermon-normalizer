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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

type testServer struct {
	*Server
	root, inbox string
	h           http.Handler
}

func newTestServer(t *testing.T, ff normalizer.FFmpeg) *testServer {
	return newTestServerWith(t, ff, nil)
}

func newTestServerWith(t *testing.T, ff normalizer.FFmpeg, pingvin *PingvinConfig) *testServer {
	t.Helper()
	root, inbox := t.TempDir(), t.TempDir()
	if ff.Bin == "" {
		ff.Bin = filepath.Join(t.TempDir(), "no-ffmpeg") // tag reading just fails
	}
	s, err := New(Config{Root: root, Inbox: inbox, CacheDir: t.TempDir(), FFmpeg: ff, Pingvin: pingvin, Logger: log.New(testLog{t}, "", 0)})
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
	ts.file(t, "2026/2026-09-27/2026-09-27_edit.flac", "x")
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
	if !e.Edit || e.Raw || strings.Join(e.Outputs, ",") != "mp3" || !e.Pending || e.Path != "2026/2026-09-27" {
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
	if info.Date != "2026-10-04" || info.Raw == nil || info.Raw.Name != "2026-10-04_raw.flac" || info.Edit != nil ||
		len(info.Outputs) != 1 || info.Outputs[0].Format != "opus" || info.Outputs[0].Path != "2026-10-04/2026-10-04.opus" {
		t.Errorf("info %+v", info)
	}
}

func defaultRequest() runRequest { return requestFrom(normalizer.DefaultOptions(), "") }

func TestCheck(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "d/2026-10-04/2026-10-04_raw.wav", "x")
	ts.file(t, "d/2026-10-04/2026-10-04.mp3", "x")
	url := "/api/check?path=d/2026-10-04"

	q := defaultRequest()
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "select where the sermon") {
		t.Errorf("no selection: %+v", pl)
	}
	q.Start, q.End, q.FadeIn = 21, 79, 1
	pl := decode[plan](t, ts.do(t, "POST", url, q))
	if parsed := ts.check(filepath.Join(ts.root, "d/2026-10-04"), q).opt; parsed.Start != 21 || parsed.End != 79 || parsed.FadeIn != 1 {
		t.Errorf("parsed options %+v", parsed)
	}
	if pl.Error != "" ||
		len(pl.Notices) != 1 || !strings.Contains(pl.Notices[0], "2026-10-04.mp3") {
		t.Errorf("plan %+v", pl)
	}
	q.Flac, q.Mp3, q.Opus = false, false, false
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "at least one output") {
		t.Errorf("no outputs: %+v", pl)
	}
	// An edit file in the folder conflicts with cutting, but can be used.
	ts.file(t, "d/2026-10-04/2026-10-04_edit.flac", "x")
	q = defaultRequest()
	q.Start = 30
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); !strings.Contains(pl.Error, "would conflict") {
		t.Errorf("conflict: %+v", pl)
	}
	q.UseEdit = true
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); pl.Error != "" || ts.check(filepath.Join(ts.root, "d/2026-10-04"), q).opt.Start != 0 {
		t.Errorf("use edit: %+v", pl)
	}
	if rec := ts.do(t, "POST", url, map[string]any{"bogus": 1}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
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

func TestInboxImport(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ts.inbox, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ZOOM0001.WAV", "audio")
	write("Gudstjeneste 2026-09-27.flac", "audio2")
	write("being written.partial.flac", "x")
	write(".pingvin-seen", "x")
	write("notes.txt", "x")
	recorded := time.Date(2026, 10, 4, 11, 30, 0, 0, time.Local)
	os.Chtimes(filepath.Join(ts.inbox, "ZOOM0001.WAV"), recorded, recorded)

	list := decode[struct{ Files []inboxFile }](t, ts.do(t, "GET", "/api/inbox", nil))
	if len(list.Files) != 2 {
		t.Fatalf("files %+v", list.Files)
	}
	for _, f := range list.Files {
		switch f.Name {
		case "Gudstjeneste 2026-09-27.flac":
			if f.SuggestedDate != "2026-09-27" || f.DateSource != "name" {
				t.Errorf("from name: %+v", f)
			}
		case "ZOOM0001.WAV":
			if f.SuggestedDate != "2026-10-04" || f.DateSource != "modified" {
				t.Errorf("from mtime: %+v", f)
			}
		}
	}

	rec := ts.do(t, "POST", "/api/inbox/import", importRequest{Name: "Gudstjeneste 2026-09-27.flac", Date: "2026-09-27"})
	if rec.Code != http.StatusOK || decode[map[string]string](t, rec)["path"] != "2026/2026-09-27" {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(ts.root, "2026", "2026-09-27", "2026-09-27_raw.flac")); string(b) != "audio2" {
		t.Error("imported file missing")
	}
	if fileExists(filepath.Join(ts.inbox, "Gudstjeneste 2026-09-27.flac")) {
		t.Error("the inbox copy should be gone")
	}
	// A date that already has a raw recording is refused.
	if rec := ts.do(t, "POST", "/api/inbox/import", importRequest{Name: "ZOOM0001.WAV", Date: "2026-09-27"}); rec.Code != http.StatusConflict {
		t.Errorf("second raw: %d", rec.Code)
	}
	for _, q := range []importRequest{{"../x.wav", "2026-10-04"}, {"missing.wav", "2026-10-04"}, {"ZOOM0001.WAV", "04.10.2026"},
		{"being written.partial.flac", "2026-10-04"}} {
		if rec := ts.do(t, "POST", "/api/inbox/import", q); rec.Code < 400 {
			t.Errorf("import %+v: %d", q, rec.Code)
		}
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
	req := httptest.NewRequest("POST", "/api/inbox/import", strings.NewReader("{}"))
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
	q.Start = 30
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
