package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	t.Helper()
	root, inbox := t.TempDir(), t.TempDir()
	if ff.Bin == "" {
		ff.Bin = filepath.Join(t.TempDir(), "no-ffmpeg") // tag reading just fails
	}
	s, err := New(Config{Root: root, Inbox: inbox, CacheDir: t.TempDir(), FFmpeg: ff, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return &testServer{Server: s, root: s.root, inbox: s.cfg.Inbox, h: s.Handler()}
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
	if pl.Error != "" || !strings.HasSuffix(pl.Command, "--start 0:00:21 --end 0:01:19 --fade-in 1") ||
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
	if pl := decode[plan](t, ts.do(t, "POST", url, q)); pl.Error != "" || strings.Contains(pl.Command, "--start") {
		t.Errorf("use edit: %+v", pl)
	}
	if rec := ts.do(t, "POST", url, map[string]any{"bogus": 1}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
}

func upload(t *testing.T, ts *testServer, name, content string, modified time.Time) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if !modified.IsZero() {
		mw.WriteField("lastModified", strconv.FormatInt(modified.UnixMilli(), 10))
	}
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write([]byte(content))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/inbox/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	return rec
}

func TestInboxUploadAndImport(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	recorded := time.Date(2026, 10, 4, 11, 30, 0, 0, time.Local)
	rec := upload(t, ts, "ZOOM0001.WAV", "audio", recorded)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	f := decode[inboxFile](t, rec)
	if f.SuggestedDate != "2026-10-04" || f.DateSource != "modified" {
		t.Errorf("suggested %+v (from the browser's lastModified)", f)
	}
	if st, _ := os.Stat(filepath.Join(ts.inbox, "ZOOM0001.WAV")); !st.ModTime().Equal(recorded) {
		t.Errorf("mtime %v", st.ModTime())
	}
	for name, want := range map[string]int{"ZOOM0001.WAV": http.StatusConflict, "talk.mp3": http.StatusBadRequest, ".x.wav": http.StatusBadRequest} {
		if rec := upload(t, ts, name, "x", time.Time{}); rec.Code != want {
			t.Errorf("upload %s: %d, want %d", name, rec.Code, want)
		}
	}
	entries, _ := os.ReadDir(ts.inbox)
	if len(entries) != 1 {
		t.Errorf("inbox should hold just the one upload, no .partial files: %v", entries)
	}
	upload(t, ts, "Gudstjeneste 2026-09-27.flac", "audio2", time.Time{})

	list := decode[struct{ Files []inboxFile }](t, ts.do(t, "GET", "/api/inbox", nil))
	if len(list.Files) != 2 {
		t.Fatalf("files %+v", list.Files)
	}
	for _, f := range list.Files {
		if f.Name == "Gudstjeneste 2026-09-27.flac" && (f.SuggestedDate != "2026-09-27" || f.DateSource != "name") {
			t.Errorf("from name: %+v", f)
		}
	}

	rec = ts.do(t, "POST", "/api/inbox/import", importRequest{Name: "ZOOM0001.WAV", Date: "2026-10-04"})
	if rec.Code != http.StatusOK || decode[map[string]string](t, rec)["path"] != "2026/2026-10-04" {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04_raw.wav")); string(b) != "audio" {
		t.Error("imported file missing")
	}
	if fileExists(filepath.Join(ts.inbox, "ZOOM0001.WAV")) {
		t.Error("the inbox copy should be gone")
	}
	// A date that already has a raw recording is refused.
	if rec := ts.do(t, "POST", "/api/inbox/import", importRequest{Name: "Gudstjeneste 2026-09-27.flac", Date: "2026-10-04"}); rec.Code != http.StatusConflict {
		t.Errorf("second raw: %d", rec.Code)
	}
	for _, q := range []importRequest{{"../x.wav", "2026-10-04"}, {"missing.wav", "2026-10-04"}, {"Gudstjeneste 2026-09-27.flac", "04.10.2026"}} {
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
	if snap.State != "queued" || !strings.Contains(snap.Command, "--start 0:00:30") {
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
