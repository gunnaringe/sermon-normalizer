package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// downloadTest is a church with one finished sermon (an MP3 and a FLAC) and a download share of it.
func downloadTest(t *testing.T, body string) (*shareTest, string) {
	t.Helper()
	st := newShareTest(t, RateLimits{RequestsPerIP: 1000, RequestsTotal: 1000})
	dir := filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "2026-10-18 Advent.mp3"), []byte("ID3 the mp3 file"), 0o644)
	os.WriteFile(filepath.Join(dir, "2026-10-18 Advent.flac"), []byte("fLaC the flac file"), 0o644)
	code, _ := st.makeShare(body)
	return st, code
}

type downloadInfoView struct {
	Kind, Title, Date, State string
	DownloadsLeft            *int
	Files                    []struct {
		Format string
		Size   int64
	}
}

func (st *shareTest) info(code string) downloadInfoView {
	st.t.Helper()
	w := st.call("GET", "/api/share", code, "1.1.1.1", nil)
	if w.Code != http.StatusOK {
		st.t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	return decode[downloadInfoView](st.t, w)
}

func (st *shareTest) download(code, ip, format string) (int, string) {
	b, _ := json.Marshal(map[string]string{"format": format})
	w := st.call("POST", "/api/share/downloads", code, ip, b)
	var out struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.URL
}

// fetch gets a download's address the way a browser does: no code.
func (st *shareTest) fetch(addr, rangeHeader string) *http.Response {
	r := httptest.NewRequest("GET", "/"+addr, nil)
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	w := httptest.NewRecorder()
	st.public.ServeHTTP(w, r)
	return w.Result()
}

func readAll(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

var escape = url.QueryEscape

func TestShareDownload(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3","flac"],"maxDownloads":2,"label":"For Anna"}`)
	info := st.info(code)
	if info.Kind != "download" || info.Title == "" || info.Date != "2026-10-18" || info.State != "" || *info.DownloadsLeft != 2 ||
		len(info.Files) != 2 || info.Files[0].Format != "flac" || info.Files[1].Size != int64(len("ID3 the mp3 file")) {
		t.Fatalf("info: %+v", info)
	}
	// A download code takes no uploads, and gives only its formats.
	if w := st.start(code, "1.1.1.1", "x.wav", 4096); w.Code != http.StatusForbidden {
		t.Errorf("upload with a download code: %d", w.Code)
	}
	if c, _ := st.download(code, "1.1.1.1", "opus"); c != http.StatusBadRequest {
		t.Errorf("a format it does not give: %d", c)
	}

	c, addr := st.download(code, "1.1.1.1", "mp3")
	if c != http.StatusCreated || !strings.HasPrefix(addr, "api/share/downloads/") {
		t.Fatalf("download: %d %q", c, addr)
	}
	// Pressing again (the download broke) gives the same one, and counts nothing.
	if c, again := st.download(code, "1.1.1.1", "mp3"); c != http.StatusOK || again != addr {
		t.Errorf("again: %d %q", c, again)
	}
	if left := *st.info(code).DownloadsLeft; left != 1 {
		t.Errorf("downloads left: %d", left)
	}
	resp := st.fetch(addr, "")
	if b := readAll(resp); resp.StatusCode != http.StatusOK || b != "ID3 the mp3 file" || resp.Header.Get("Content-Type") != "audio/mpeg" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="2026-10-18 Advent.mp3"` {
		t.Errorf("file: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	// Resuming, as many times as it takes, without the code.
	for range 3 {
		if resp := st.fetch(addr, "bytes=4-"); resp.StatusCode != http.StatusPartialContent || readAll(resp) != "the mp3 file" {
			t.Errorf("resume: %d", resp.StatusCode)
		}
	}

	if c, _ := st.download(code, "2.2.2.2", "flac"); c != http.StatusCreated {
		t.Errorf("second download: %d", c)
	}
	if c, _ := st.download(code, "3.3.3.3", "mp3"); c != http.StatusForbidden {
		t.Errorf("a third, of two: %d", c)
	}
	if info := st.info(code); info.State != "used up" || *info.DownloadsLeft != 0 {
		t.Errorf("used up: %+v", info)
	}
	if c, again := st.download(code, "1.1.1.1", "mp3"); c != http.StatusOK || again != addr {
		t.Errorf("one already counted still works when used up: %d", c)
	}
	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if s := list.Shares[0]; s.Kind != "download" || s.Count != 2 || len(s.Log) != 2 || s.Log[0].Name != "2026-10-18 Advent.flac" || s.Resumable != 2 || s.Downloading != 0 || s.Missing {
		t.Errorf("in the studio: %+v", s)
	}
	// A wrong ticket counts as a wrong code; an expired one has ended.
	if resp := st.fetch("api/share/downloads/nope", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("wrong ticket: %d", resp.StatusCode)
	}
	st.app.shareMu.Lock()
	for _, tk := range st.app.tenants[0].shares[0].Tickets {
		tk.Expires = time.Now().Add(-time.Second)
	}
	st.app.shareMu.Unlock()
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusGone {
		t.Errorf("expired ticket: %d", resp.StatusCode)
	}
	st.app.sweepShares(time.Now())
	if n := len(st.app.tenants[0].shares[0].Tickets); n != 0 {
		t.Errorf("the sweep left %d tickets", n)
	}
}

func TestShareDownloadFollowsTheRecording(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	_, addr := st.download(code, "1.1.1.1", "mp3")

	// Renamed: the share and the download follow it.
	if w := do(st.studio, "POST", "/api/rename?path="+escape("2026/2026-10-18 Advent"), `{"date":"2026-10-19","name":"First Advent"}`); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if info := st.info(code); info.Date != "2026-10-19" || len(info.Files) != 1 {
		t.Errorf("after the rename: %+v", info)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("download after the rename: %d", resp.StatusCode)
	}
	if rec := st.app.tenants[0].shares[0].Recording; rec != "2026/2026-10-19 First Advent" {
		t.Errorf("the share's recording: %q", rec)
	}

	// In the trash: not there. Another recording in its place is not it.
	w := do(st.studio, "POST", "/api/recordings/delete?path="+escape("2026/2026-10-19 First Advent"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	trashID := decode[struct{ ID string }](t, w).ID
	other := filepath.Join(st.church, "Taler", "2026", "2026-10-19 First Advent")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "2026-10-19 First Advent.mp3"), []byte("ID3 another sermon"), 0o644)
	if info := st.info(code); info.State != "missing" || len(info.Files) != 0 {
		t.Errorf("in the trash: %+v", info)
	}
	if c, _ := st.download(code, "2.2.2.2", "mp3"); c != http.StatusGone {
		t.Errorf("download from the trash: %d", c)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusGone {
		t.Errorf("a counted download of one in the trash: %d", resp.StatusCode)
	}
	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if !list.Shares[0].Missing {
		t.Errorf("the studio does not see it is missing: %+v", list.Shares[0])
	}

	// Restored: there again.
	os.RemoveAll(other)
	if w := do(st.studio, "POST", "/api/trash/restore?id="+escape(trashID), ""); w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if info := st.info(code); info.State != "" || len(info.Files) != 1 {
		t.Errorf("restored: %+v", info)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusOK || readAll(resp) != "ID3 the mp3 file" {
		t.Errorf("download when restored: %d", resp.StatusCode)
	}
}

func TestShareDownloadRefusals(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	for _, body := range []string{
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["opus"],"label":"Not made"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["wav"],"label":"No such format"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":[],"label":"None"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":""}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Past","expires":"2020-01-01T00:00:00Z"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Negative","maxDownloads":-1}`,
		`{"kind":"download","recording":"2026/2026-11-01","formats":["mp3"],"label":"No such recording"}`,
		`{"kind":"download","recording":"","formats":["mp3"],"label":"The root"}`,
		`{"kind":"download","recording":"../kirke","formats":["mp3"],"label":"Outside"}`,
		`{"kind":"other","label":"Unknown kind"}`,
	} {
		if w := do(st.studio, "POST", "/api/shares", body); w.Code < 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	// An upload code downloads nothing.
	up, _ := st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	if c, _ := st.download(up, "1.1.1.1", "mp3"); c != http.StatusForbidden {
		t.Errorf("download with an upload code: %d", c)
	}
	if info := st.info(up); info.Kind != "upload" {
		t.Errorf("an upload code's kind: %q", info.Kind)
	}
	// Paused: no downloads, and the counted ones stop.
	_, addr := st.download(code, "1.1.1.1", "mp3")
	id := st.app.tenants[0].shares[0].ID
	if w := do(st.studio, "POST", "/api/shares/"+id+"/pause", ""); w.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", w.Code, w.Body)
	}
	if c, _ := st.download(code, "1.1.1.1", "mp3"); c != http.StatusForbidden {
		t.Errorf("download when paused: %d", c)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a counted download when paused: %d", resp.StatusCode)
	}
	// The recording's id is kept in its recording.json, and the share survives a restart.
	if readState(filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")).ID == "" {
		t.Error("no id in recording.json")
	}
	shares, err := readShares(st.church)
	if err != nil || len(shares) != 2 || shares[0].Kind != "download" || shares[0].RecordingID == "" || shares[0].Formats[0] != "mp3" {
		t.Errorf("shares.json: %v %+v", err, shares)
	}
}

// A download takes as long as it takes: the listener's write timeout (minutes, for the upload chunks) does not cut it off.
func TestShareDownloadOutlastsTheWriteTimeout(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	big := bytes.Repeat([]byte("sermon "), 16<<20/7)
	os.WriteFile(filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent", "2026-10-18 Advent.mp3"), big, 0o644)
	srv := httptest.NewUnstartedServer(st.public)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	// Counted in-process: only the file goes through the listener with the short timeout.
	c, addr := st.download(code, "1.1.1.1", "mp3")
	if c != http.StatusCreated {
		t.Fatalf("download: %d", c)
	}
	resp, err := http.Get(srv.URL + "/" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got int64
	buf := make([]byte, 256<<10)
	for {
		n, err := resp.Body.Read(buf)
		got += int64(n)
		if err != nil {
			break
		}
		time.Sleep(5 * time.Millisecond) // a slow reader: well past the 100 ms in all
	}
	if got != int64(len(big)) {
		t.Errorf("got %d of %d bytes", got, len(big))
	}
}

// The number on the shares icon: uploads going on (a chunk now or in the last minutes) and files being sent.
func TestSharesActive(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	active := func() int {
		return decode[struct{ Active int }](t, do(st.studio, "GET", "/api/shares/active", "")).Active
	}
	if n := active(); n != 0 {
		t.Errorf("nothing going on: %d", n)
	}
	up, _ := st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	id := decode[struct{ ID string }](t, st.start(up, "1.1.1.1", "x.wav", 8192)).ID
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", up, "1.1.1.1", wav(4096)); w.Code != http.StatusOK {
		t.Fatalf("chunk: %d %s", w.Code, w.Body)
	}
	if n := active(); n != 1 {
		t.Errorf("an upload going on: %d", n)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(st.church, "Innboks", ".share-"+id+".partial"), old, old)
	if n := active(); n != 0 {
		t.Errorf("an upload that stopped an hour ago: %d", n)
	}
	// A file being sent counts while it is.
	_, addr := st.download(code, "1.1.1.1", "mp3")
	st.app.shareMu.Lock()
	shareID := st.app.tenants[0].shares[0].ID
	st.app.share.sending[shareID]++ // as during a long response
	st.app.shareMu.Unlock()
	if n := active(); n != 1 {
		t.Errorf("a download going on: %d", n)
	}
	st.fetch(addr, "")
	st.app.shareMu.Lock()
	n := st.app.share.sending[shareID]
	st.app.shareMu.Unlock()
	if n != 1 {
		t.Errorf("a finished response is not counted down: %d", n)
	}
}
