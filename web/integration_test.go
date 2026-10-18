package web

// Integration tests with real ffmpeg; skipped without it (run in Docker).

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

func requireFFmpeg(t *testing.T) normalizer.FFmpeg {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test (-short)")
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not in PATH")
	}
	return normalizer.FFmpeg{Bin: bin}
}

// service writes a fake service: music, 2 s silence at 19-21 s, speech,
// silence at 79-81 s, music (100 s, stereo).
func service(t *testing.T, ff normalizer.FFmpeg, path string) {
	t.Helper()
	expr := "if(between(t,19,21)+between(t,79,81), 0, if(between(t,21,79), 0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.5*sin(2*PI*440*t)))"
	if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i",
		"aevalsrc=exprs='"+expr+"|"+expr+"':s=48000:d=100", "-c:a", "pcm_s24le", path); err != nil {
		t.Fatal(err)
	}
}

func duration(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

func waitAnalysis(t *testing.T, ts *testServer, path string) analysisState {
	t.Helper()
	st := decode[analysisState](t, ts.do(t, "POST", "/api/analysis?path="+path, nil))
	for i := 0; st.State == "running" && i < 600; i++ {
		time.Sleep(100 * time.Millisecond)
		st = decode[analysisState](t, ts.do(t, "GET", "/api/analysis?path="+path, nil))
	}
	return st
}

func TestIntegrationAnalysis(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	ts.file(t, "2026-10-04/x", "")
	service(t, ff, filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.wav"))

	if st := decode[analysisState](t, ts.do(t, "GET", "/api/analysis?path=2026-10-04", nil)); st.State != "none" {
		t.Errorf("before: %+v", st)
	}
	if rec := ts.do(t, "GET", "/api/peaks?path=2026-10-04", nil); rec.Code != http.StatusConflict {
		t.Errorf("peaks before analysis: %d", rec.Code)
	}
	st := waitAnalysis(t, ts, "2026-10-04")
	if st.State != "done" || st.Key == "" {
		t.Fatalf("analysis: %+v", st)
	}

	req := httptest.NewRequest("GET", "/api/peaks?path=2026-10-04", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	zr, err := gzip.NewReader(rec.Body)
	if err != nil || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("peaks not gzipped: %v", err)
	}
	var peaks struct {
		Duration float64
		Window   float64
		Silences []normalizer.Span
		Peaks    []float64
	}
	if err := json.NewDecoder(zr).Decode(&peaks); err != nil {
		t.Fatal(err)
	}
	if len(peaks.Peaks) != 10000 || peaks.Duration != 100 || peaks.Peaks[1000] < 0.4 || peaks.Peaks[2000] != 0 || peaks.Peaks[5000] > 0.1 {
		t.Errorf("peaks: %d values, duration %v, music %.2f gap %.2f speech %.2f",
			len(peaks.Peaks), peaks.Duration, peaks.Peaks[1000], peaks.Peaks[2000], peaks.Peaks[5000])
	}
	if len(peaks.Silences) != 2 || peaks.Silences[0].Start < 18.9 || peaks.Silences[0].End > 21.1 {
		t.Errorf("silences %v", peaks.Silences)
	}

	// The preview is a seekable MP3 of the whole recording.
	req = httptest.NewRequest("GET", "/api/preview?path=2026-10-04", nil)
	req.Header.Set("Range", "bytes=1000-1999")
	rec = httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 1000 || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("preview range: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	preview := filepath.Join(ts.analyses.dir(st.Key), "preview.mp3")
	if d := duration(t, preview); d < 99.9 || d > 100.2 {
		t.Errorf("preview is %.2f s", d)
	}
	// A second request is served from the cache; a changed recording is analysed again.
	if again := decode[analysisState](t, ts.do(t, "POST", "/api/analysis?path=2026-10-04", nil)); again.State != "done" || again.Key != st.Key {
		t.Errorf("cached: %+v", again)
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.wav"), future, future)
	if changed := decode[analysisState](t, ts.do(t, "GET", "/api/analysis?path=2026-10-04", nil)); changed.State != "none" || changed.Key == st.Key {
		t.Errorf("after a change: %+v", changed)
	}
}

// events reads server-sent events until the stream ends.
func events(t *testing.T, url string) []jobSnapshot {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snaps []jobSnapshot
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var s jobSnapshot
			if err := json.Unmarshal([]byte(data), &s); err != nil {
				t.Fatal(err)
			}
			snaps = append(snaps, s)
		}
	}
	return snaps
}

func TestIntegrationRun(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	hs := httptest.NewServer(ts.h)
	defer hs.Close()
	ts.file(t, "2026-10-04/x", "")
	raw := filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.wav")
	service(t, ff, raw)

	q := defaultRequest()
	q.Start, q.End, q.Flac, q.Opus, q.FadeIn = 21, 79, false, false, 1
	rec := ts.do(t, "POST", "/api/run?path=2026-10-04", q)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", rec.Code, rec.Body)
	}
	id := decode[jobSnapshot](t, rec).ID
	snaps := events(t, hs.URL+"/api/jobs/"+id+"/events")
	last := snaps[len(snaps)-1]
	if last.State != "done" || len(last.Outputs) != 1 || last.Outputs[0].Format != "mp3" || len(snaps) < 3 {
		t.Fatalf("%d events, last %+v", len(snaps), last)
	}
	var sawProgress bool
	for _, s := range snaps {
		sawProgress = sawProgress || (s.State == "running" && s.Progress > 0 && s.Progress < 1)
	}
	if !sawProgress {
		t.Error("no progress seen in the events")
	}
	mp3 := filepath.Join(ts.root, "2026-10-04", "2026-10-04.mp3")
	if d := duration(t, mp3); d < 57.9 || d > 58.2 {
		t.Errorf("mp3 is %.2f s, want 58", d)
	}
	entries, _ := os.ReadDir(filepath.Join(ts.root, "2026-10-04"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "2026-10-04.mp3,2026-10-04_raw.wav,x" {
		t.Errorf("files %v", names)
	}
	// The output can be fetched for listening.
	resp, err := http.Get(hs.URL + "/api/file?path=" + last.Outputs[0].Path)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("file: %v %v", err, resp.Status)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if st, _ := os.Stat(mp3); n != st.Size() {
		t.Errorf("served %d bytes of %d", n, st.Size())
	}
}

// Cancelling a running job, or closing the server, leaves nothing half-written.
func TestIntegrationCancelAndClose(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	ts.file(t, "2026-10-04/x", "")
	service(t, ff, filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.wav"))
	q := defaultRequest()
	q.Start = 1
	id := decode[jobSnapshot](t, ts.do(t, "POST", "/api/run?path=2026-10-04", q)).ID
	for i := 0; i < 100; i++ {
		if s := ts.jobs.get(id).snapshot(); s.State == "running" && len(s.Steps) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := decode[jobSnapshot](t, ts.do(t, "POST", "/api/jobs/"+id+"/cancel", nil)); got.State != "cancelled" {
		t.Errorf("cancel: %+v", got)
	}
	// A new run, then the server closes under it.
	q.Force = true
	ts.do(t, "POST", "/api/run?path=2026-10-04", q)
	time.Sleep(300 * time.Millisecond)
	ts.Close()
	entries, _ := os.ReadDir(filepath.Join(ts.root, "2026-10-04"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".partial") || strings.HasPrefix(e.Name(), ".") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}
