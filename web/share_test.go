package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type shareTest struct {
	t      *testing.T
	app    *App
	studio http.Handler
	public http.Handler
	church string // the church folder
}

func newShareTest(t *testing.T, limits RateLimits) *shareTest {
	t.Helper()
	data := t.TempDir()
	church := filepath.Join(data, "kirke")
	os.MkdirAll(filepath.Join(church, "Taler"), 0o755)
	a, err := NewApp(AppConfig{
		Base:    Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)},
		DataDir: data,
		Share:   &ShareOptions{URL: "https://up.example/", IPHeader: "CF-Connecting-IP", Limits: limits},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return &shareTest{t: t, app: a, studio: a.Handler(), public: a.ShareHandler(), church: church}
}

// makeShare makes a share in the studio and returns its code.
func (st *shareTest) makeShare(body string) (string, shareView) {
	st.t.Helper()
	w := do(st.studio, "POST", "/api/shares", body)
	if w.Code != http.StatusCreated {
		st.t.Fatalf("create share: %d %s", w.Code, w.Body)
	}
	out := decode[struct {
		Code  string
		Share shareView
	}](st.t, w)
	return out.Code, out.Share
}

// call sends a request to the public page with a code from an address.
func (st *shareTest) call(method, url, code, ip string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, bytes.NewReader(body))
	r.Header.Set("Share-Code", code)
	r.Header.Set("CF-Connecting-IP", ip)
	w := httptest.NewRecorder()
	st.public.ServeHTTP(w, r)
	return w
}

func (st *shareTest) start(code, ip, name string, size int) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"name": name, "size": size, "modified": time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).UnixMilli()})
	return st.call("POST", "/api/share/uploads", code, ip, b)
}

func wav(size int) []byte {
	b := bytes.Repeat([]byte{1}, size)
	copy(b, "RIFF\x00\x00\x00\x00WAVE")
	return b
}

func TestShareCodes(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		c := newCode()
		if len(c) != 19 || strings.ContainsAny(c, "01IO") || seen[c] {
			t.Fatalf("bad code %q", c)
		}
		seen[c] = true
		if hashCode(c) != hashCode(strings.ToLower(strings.ReplaceAll(c, "-", " "))) {
			t.Errorf("%s: typed differently, hashed differently", c)
		}
	}
}

func TestShareFileName(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-04 Gudstjeneste.WAV": "2026-10-04 Gudstjeneste.wav",
		`C:\rec\..\..\opptak.flac`:    "opptak.flac",
		"../../etc/x.wav":             "x.wav",
		".hidden.wav":                 "hidden.wav",
		"a.partial.wav":               "a_partial.wav",
		"<script>.wav":                "script.wav",
		".wav":                        "recording.wav",
		"æøå 1.wav":                   "æøå 1.wav",
	} {
		got, err := shareFileName(in)
		if err != nil || got != want {
			t.Errorf("shareFileName(%q) = %q, %v; want %q", in, got, err, want)
		}
		if _, err := inboxName(got); err != nil {
			t.Errorf("%q: the inbox would not take %q: %v", in, got, err)
		}
	}
	for _, bad := range []string{"x.mp3", "x", "x.wav.exe", "x.wa"} {
		if _, err := shareFileName(bad); err == nil {
			t.Errorf("shareFileName(%q) accepted", bad)
		}
	}
}

func TestShareUploadToInbox(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	code, sh := st.makeShare(`{"label":"Lydbordet","types":[".wav"],"maxUploads":1}`)
	if sh.Hint != code[len(code)-4:] || sh.MaxFileSize != defaultShareFileSize {
		t.Fatalf("share %+v", sh)
	}
	// The code is not stored, only its hash.
	b, _ := os.ReadFile(filepath.Join(st.church, sharesFile))
	if bytes.Contains(b, []byte(code)) || !bytes.Contains(b, []byte(hashCode(code))) {
		t.Fatalf("shares.json: %s", b)
	}

	if w := st.call("GET", "/api/share", strings.ToLower(code), "1.2.3.4", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Lydbordet") {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	if w := st.start(code, "1.2.3.4", "x.flac", 5000); w.Code != http.StatusBadRequest {
		t.Errorf("flac on a wav share: %d", w.Code)
	}
	w := st.start(code, "1.2.3.4", "2026-10-04 Gudstjeneste.wav", 5000)
	if w.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	id := decode[struct{ ID string }](t, w).ID
	if w := st.start(code, "1.2.3.4", "other.wav", 5000); w.Code != http.StatusForbidden {
		t.Errorf("second upload while the only one is reserved: %d", w.Code)
	}

	data := wav(5000)
	url := "/api/share/uploads/" + id
	if w := st.call("PUT", url+"?offset=0", code, "1.2.3.4", data[:3000]); w.Code != 200 {
		t.Fatalf("chunk 1: %d %s", w.Code, w.Body)
	}
	if w := st.call("PUT", url+"?offset=1000", code, "1.2.3.4", data[1000:]); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"offset":3000`) {
		t.Errorf("wrong offset: %d %s", w.Code, w.Body)
	}
	if w := st.call("GET", url, code, "1.2.3.4", nil); !strings.Contains(w.Body.String(), `"offset":3000`) {
		t.Errorf("status: %s", w.Body)
	}
	if w := st.call("PUT", url+"?offset=3000", code, "1.2.3.4", append(data[3000:], 'x')); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("more than the size: %d %s", w.Code, w.Body)
	}
	if w := st.call("PUT", url+"?offset=3000", code, "1.2.3.4", data[3000:]); w.Code != http.StatusCreated {
		t.Fatalf("last chunk: %d %s", w.Code, w.Body)
	}

	// Ingested into its date folder, with nothing left behind in the inbox.
	want := filepath.Join(st.church, "Taler", "2026", "2026-10-04", "2026-10-04_raw.wav")
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(want) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got, _ := os.ReadFile(want); !bytes.Equal(got, data) {
		t.Fatalf("%s: %d bytes", want, len(got))
	}
	if des, _ := os.ReadDir(filepath.Join(st.church, "Innboks")); len(des) != 0 {
		t.Errorf("left in the inbox: %v", des)
	}

	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if s := list.Shares[0]; s.Count != 1 || s.Bytes != 5000 || s.State != "used up" || len(s.Log) != 1 || s.Log[0].IP != "1.2.3.4" {
		t.Errorf("after the upload: %+v", s)
	}
	if w := st.start(code, "1.2.3.4", "more.wav", 5000); w.Code != http.StatusForbidden {
		t.Errorf("upload past the limit: %d", w.Code)
	}
}

func TestShareRefusesWhatItShould(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	code, sh := st.makeShare(`{"label":"S","types":[".wav",".flac"],"maxFileSize":2000000,"maxTotal":3000000}`)

	if w := st.start(code, "1.1.1.1", "big.wav", 2000001); w.Code != http.StatusForbidden {
		t.Errorf("larger than the largest file: %d", w.Code)
	}
	w := st.start(code, "1.1.1.1", "a.wav", 2000000)
	id := decode[struct{ ID string }](t, w).ID
	if w := st.start(code, "1.1.1.1", "b.wav", 1000001); w.Code != http.StatusForbidden {
		t.Errorf("past the total with one in progress: %d", w.Code)
	}
	// Not a WAV inside.
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", code, "1.1.1.1", bytes.Repeat([]byte("<html>"), 100)); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("not a wav: %d %s", w.Code, w.Body)
	}
	id = decode[struct{ ID string }](t, st.start(code, "1.1.1.1", "c.wav", 2000000)).ID
	// Another share's code cannot touch this upload.
	other, _ := st.makeShare(`{"label":"T","types":[".wav"]}`)
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", other, "1.1.1.1", wav(2000)); w.Code != http.StatusNotFound {
		t.Errorf("upload of another share: %d", w.Code)
	}
	// Revoked: refused at once, also mid-upload.
	if w := do(st.studio, "POST", "/api/shares/"+sh.ID+"/revoke", ""); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", code, "1.1.1.1", wav(2000)); w.Code != http.StatusForbidden {
		t.Errorf("chunk after revoke: %d", w.Code)
	}
	// Deleting drops the uploads in progress with their files.
	os.WriteFile(filepath.Join(st.church, "Innboks", ".share-"+id+".partial"), []byte("x"), 0o644)
	if w := do(st.studio, "POST", "/api/shares/"+sh.ID+"/delete", ""); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if fileExists(filepath.Join(st.church, "Innboks", ".share-"+id+".partial")) {
		t.Error("partial left after delete")
	}
	if w := st.call("GET", "/api/share", code, "1.1.1.1", nil); w.Code != http.StatusForbidden {
		t.Errorf("deleted share: %d", w.Code)
	}
}

// A refused or cancelled upload gives its place in the limits back.
func TestShareRefusedUploadFreesItsPlace(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	code, _ := st.makeShare(`{"label":"S","types":[".wav"],"maxUploads":1}`)
	id := decode[struct{ ID string }](t, st.start(code, "1.1.1.1", "a.wav", 2000)).ID
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", code, "1.1.1.1", bytes.Repeat([]byte("x"), 2000)); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("not a wav: %d", w.Code)
	}
	w := st.start(code, "1.1.1.1", "b.wav", 2000)
	if w.Code != http.StatusCreated {
		t.Fatalf("start after a refused file: %d %s", w.Code, w.Body)
	}
	id = decode[struct{ ID string }](t, w).ID
	st.call("PUT", "/api/share/uploads/"+id+"?offset=0", code, "1.1.1.1", wav(2000)[:1000])
	if w := st.call("DELETE", "/api/share/uploads/"+id, code, "1.1.1.1", nil); w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if fileExists(filepath.Join(st.church, "Innboks", ".share-"+id+".partial")) {
		t.Error("partial left after cancel")
	}
	if w := st.start(code, "1.1.1.1", "c.wav", 2000); w.Code != http.StatusCreated {
		t.Errorf("start after a cancelled upload: %d %s", w.Code, w.Body)
	}
}

func TestShareExpiry(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	if w := do(st.studio, "POST", "/api/shares", `{"label":"S","types":[".wav"],"expires":"2020-01-01T00:00:00Z"}`); w.Code != http.StatusBadRequest {
		t.Errorf("expiry in the past: %d", w.Code)
	}
	code, _ := st.makeShare(fmt.Sprintf(`{"label":"S","types":[".wav"],"expires":%q}`, time.Now().Add(time.Hour).Format(time.RFC3339)))
	if w := st.call("GET", "/api/share", code, "1.1.1.1", nil); w.Code != 200 {
		t.Fatalf("before expiry: %d", w.Code)
	}
	st.app.shareMu.Lock()
	past := time.Now().Add(-time.Minute)
	st.app.tenants[0].shares[0].Expires = &past
	st.app.shareMu.Unlock()
	if w := st.call("GET", "/api/share", code, "1.1.1.1", nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("after expiry: %d %s", w.Code, w.Body)
	}
}

func TestShareWrongCodesAreLimited(t *testing.T) {
	st := newShareTest(t, RateLimits{FailsPerIP: 3, FailsTotal: 5})
	code, _ := st.makeShare(`{"label":"S","types":[".wav"]}`)
	for i := range 3 {
		if w := st.call("GET", "/api/share", "WRONG-CODE", "9.9.9.9", nil); w.Code != http.StatusForbidden {
			t.Fatalf("wrong code %d: %d", i, w.Code)
		}
	}
	// Blocked: even the right code is refused from that address.
	if w := st.call("GET", "/api/share", code, "9.9.9.9", nil); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("blocked address: %d", w.Code)
	}
	if w := st.call("GET", "/api/share", code, "8.8.8.8", nil); w.Code != 200 {
		t.Errorf("other address: %d", w.Code)
	}
	// Wrong codes from many addresses close codes for everyone.
	st.call("GET", "/api/share", "x", "7.7.7.1", nil)
	st.call("GET", "/api/share", "x", "7.7.7.2", nil)
	if w := st.call("GET", "/api/share", code, "8.8.8.8", nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("after many wrong codes in all: %d", w.Code)
	}
}

func TestShareRequestLimit(t *testing.T) {
	st := newShareTest(t, RateLimits{RequestsPerIP: 5, RequestsTotal: 8})
	for range 5 {
		st.call("GET", "/", "", "1.1.1.1", nil)
	}
	if w := st.call("GET", "/", "", "1.1.1.1", nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("past the limit per address: %d", w.Code)
	}
	for range 3 {
		st.call("GET", "/", "", "2.2.2.2", nil)
	}
	if w := st.call("GET", "/", "", "3.3.3.3", nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("past the limit in all: %d", w.Code)
	}
}

func TestShareLimiterUploadsAndPrune(t *testing.T) {
	l := newLimiter(RateLimits{UploadsPerIP: 1, UploadsTotal: 2})
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.acquire("a") || l.acquire("a") || !l.acquire("b") || l.acquire("c") {
		t.Fatal("upload slots")
	}
	l.release("a")
	if !l.acquire("c") {
		t.Error("slot not given back")
	}
	l.release("b")
	l.release("c")
	l.failed("d")
	now = now.Add(3 * time.Hour)
	l.prune()
	if len(l.ips) != 0 {
		t.Errorf("not forgotten: %d", len(l.ips))
	}
}

// The public listener serves the upload page and nothing of the studio.
func TestSharePublicIsSeparate(t *testing.T) {
	st := newShareTest(t, RateLimits{RequestsPerIP: 1000, RequestsTotal: 1000})
	w := st.call("GET", "/", "", "1.1.1.1", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "share.js") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("page: %d %v", w.Code, w.Header())
	}
	for _, path := range []string{"/index.html", "/app.js", "/editor.js", "/api/tenants", "/api/shares", "/api/config",
		"/api/folders", "/api/file?path=x", "/api/settings", "/vendor/wavesurfer/wavesurfer.esm.js"} {
		if w := st.call("GET", path, "", "1.1.1.1", nil); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s on the public listener: %d", path, w.Code)
		}
	}
	if w := st.call("PUT", "/api/inbox/files/x.wav", "", "1.1.1.1", wav(2000)); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("inbox PUT on the public listener: %d", w.Code)
	}
	// A cross-site POST is refused.
	r := httptest.NewRequest("POST", "/api/share/uploads", strings.NewReader("{}"))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	st.public.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", rec.Code)
	}
}

func TestSharesOff(t *testing.T) {
	a, h, _, _ := newTestApp(t)
	if a.ShareHandler() != nil {
		t.Error("public handler without shares")
	}
	if w := do(h, "GET", "/api/shares", ""); w.Code != http.StatusNotFound {
		t.Errorf("api/shares without shares: %d", w.Code)
	}
}

// An upload continues after a restart: the share keeps it in shares.json.
func TestShareSurvivesRestartAndSweep(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	code, _ := st.makeShare(`{"label":"S","types":[".wav"]}`)
	id := decode[struct{ ID string }](t, st.start(code, "1.1.1.1", "a.wav", 4000)).ID
	st.call("PUT", "/api/share/uploads/"+id+"?offset=0", code, "1.1.1.1", wav(4000)[:2000])

	shares, err := readShares(st.church)
	if err != nil || len(shares) != 1 || shares[0].Pending[id] == nil {
		t.Fatalf("shares.json: %v %+v", err, shares)
	}
	st.app.sweepShares(time.Now())
	if w := st.call("GET", "/api/share/uploads/"+id, code, "1.1.1.1", nil); !strings.Contains(w.Body.String(), `"offset":2000`) {
		t.Errorf("fresh upload swept: %s", w.Body)
	}
	st.app.sweepShares(time.Now().Add(pendingStale + time.Hour))
	if w := st.call("GET", "/api/share/uploads/"+id, code, "1.1.1.1", nil); w.Code != http.StatusNotFound {
		t.Errorf("stale upload kept: %d", w.Code)
	}
	if fileExists(filepath.Join(st.church, "Innboks", ".share-"+id+".partial")) {
		t.Error("stale partial kept")
	}
}
