package web

// Integration tests with real ffmpeg; skipped without it (run in Docker).

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/gunnaringe/sermon-studio/normalizer"
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
	// The WAV was converted to FLAC first, and replaced by it.
	if fileExists(filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.wav")) || !fileExists(filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.flac")) {
		t.Error("the raw WAV should have become FLAC")
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.flac"), future, future)
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
	// Two sections with a cut in the second: 19 s + 29 s - 5 s.
	q.Sections = []span{{Start: 50, End: 79}, {Start: 21, End: 40}}
	q.Cuts = []span{{Start: 60, End: 65}}
	q.Flac, q.Opus, q.FadeIn = false, false, 1
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
	if d := duration(t, mp3); d < 42.9 || d > 43.2 {
		t.Errorf("mp3 is %.2f s, want 43", d)
	}
	entries, _ := os.ReadDir(filepath.Join(ts.root, "2026-10-04"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "2026-10-04.mp3,original,recording.json,x" { // the recording moved to original/, as FLAC
		t.Errorf("files %v", names)
	}
	// recording.json knows how the MP3 was made, and nothing else was made.
	if made := readState(filepath.Join(ts.root, "2026-10-04")).Outputs; len(made) != 1 || made["mp3"].From == "" {
		t.Errorf("outputs in recording.json: %+v", made)
	}
	if !fileExists(filepath.Join(ts.root, "2026-10-04", "original", "2026-10-04_raw.flac")) {
		t.Error("the recording is not in original/")
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
	q.Sections = []span{{Start: 1, End: 3600}} // past the end: to the end
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

// maxVolume is the peak in dB of dur seconds of path from start.
func maxVolume(t *testing.T, ff normalizer.FFmpeg, path string, start, dur float64) float64 {
	t.Helper()
	res, err := ff.Run(context.Background(), "-ss", strconv.FormatFloat(start, 'f', 3, 64), "-t", strconv.FormatFloat(dur, 'f', 3, 64),
		"-i", path, "-af", "volumedetect", "-f", "null", "-")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(res.Stderr, "max_volume: ")
	if !ok {
		return -999 // digital silence: volumedetect reports nothing
	}
	v, _ := strconv.ParseFloat(strings.Fields(after)[0], 64)
	return v
}

// A quiet recording gets a preview loud enough to hear, with the silences
// still where the waveform shows them.
func TestIntegrationQuietPreview(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	ts.file(t, "2026-10-04/x", "")
	raw := filepath.Join(ts.root, "2026-10-04", "2026-10-04_raw.wav")
	quiet := "if(between(t,19,21)+between(t,79,81), 0, if(between(t,21,79), 0.004*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.03*sin(2*PI*440*t)))"
	if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i",
		"aevalsrc=exprs='"+quiet+"':s=48000:d=100", "-c:a", "pcm_s24le", raw); err != nil {
		t.Fatal(err)
	}
	st := waitAnalysis(t, ts, "2026-10-04")
	if st.State != "done" {
		t.Fatalf("analysis: %+v", st)
	}
	preview := filepath.Join(ts.analyses.dir(st.Key), "preview.mp3")
	raw = strings.TrimSuffix(raw, ".wav") + ".flac" // the WAV became FLAC
	rawSpeech, previewSpeech := maxVolume(t, ff, raw, 40, 10), maxVolume(t, ff, preview, 40, 10)
	t.Logf("speech peak: raw %.1f dB, preview %.1f dB", rawSpeech, previewSpeech)
	if previewSpeech < rawSpeech+30 || previewSpeech > -0.5 {
		t.Errorf("preview speech %.1f dB, raw %.1f dB: want at least 30 dB louder, not clipping", previewSpeech, rawSpeech)
	}
	// Still silent inside the gaps: the normalization did not shift time.
	for _, gap := range []float64{19.4, 79.4} {
		if v := maxVolume(t, ff, preview, gap, 1.2); v > -50 {
			t.Errorf("preview at %.1f-%.1f s: %.1f dB, want silence", gap, gap+1.2, v)
		}
	}
}

// Files dropped into the inbox are taken once they stop changing: a WAV
// becomes FLAC and is prepared, without anyone opening it.
func TestIntegrationInboxWatcher(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServerWith(t, ff, func(c *Config) { c.InboxEvery, c.InboxSettle = 50*time.Millisecond, 300*time.Millisecond })
	wav := filepath.Join(t.TempDir(), "a.wav")
	service(t, ff, wav)
	content, _ := os.ReadFile(wav)

	// Arriving slowly, like a sync client writing it in place.
	dst := filepath.Join(ts.inbox, "ZOOM0001 2026-10-04.WAV")
	folder := filepath.Join(ts.root, "2026", "2026-10-04")
	flac := filepath.Join(folder, "2026-10-04_raw.flac")
	if err := os.WriteFile(dst, content[:len(content)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if !fileExists(dst) || fileExists(folder) {
		t.Errorf("a file still arriving must stay in the inbox")
	}
	f, _ := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0)
	f.Write(content[len(content)/2:])
	f.Close()

	// Once it has settled it is in its date folder, converted and prepared.
	for i := 0; i < 600; i++ {
		if st := ts.analyses.state(flac, false); st.State == "done" && fileExists(flac) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st := ts.analyses.state(flac, false); st.State != "done" || fileExists(dst) {
		t.Fatalf("analysis %+v, inbox copy exists: %v", st, fileExists(dst))
	}
	if same, err := ff.SameAudio(context.Background(), wav, flac); err != nil || !same {
		t.Errorf("FLAC differs: %v %v", same, err)
	}
}

// Recordings of other types: an MP3 with a cover picture, an M4A and a CAF are kept
// as they are, an AIFF becomes FLAC and a video's sound is copied out as it
// arrives; each is cut into an MP3 that has the sound and nothing else.
func TestIntegrationInputTypes(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	hs := httptest.NewServer(ts.h)
	defer hs.Close()
	ctx := context.Background()
	tmp := t.TempDir()
	src, cover := filepath.Join(tmp, "service.wav"), filepath.Join(tmp, "cover.png")
	service(t, ff, src)
	if _, err := ff.Run(ctx, "-y", "-f", "lavfi", "-i", "color=c=red:s=64x64", "-frames:v", "1", cover); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ext, becomes string
		args         []string
	}{
		{".mp3", ".mp3", []string{"-i", src, "-i", cover, "-map", "0:a", "-map", "1:v", "-c:a", "libmp3lame", "-b:a", "96k",
			"-c:v", "png", "-disposition:v", "attached_pic"}},
		{".m4a", ".m4a", []string{"-i", src, "-c:a", "aac", "-b:a", "96k"}},
		{".caf", ".caf", []string{"-i", src, "-c:a", "alac"}},
		{".aiff", ".flac", []string{"-i", src, "-c:a", "pcm_s16be"}},
		{".mp4", ".mka", []string{"-f", "lavfi", "-i", "testsrc=s=160x120:r=5:d=100", "-i", src, "-map", "0:v", "-map", "1:a",
			"-c:v", "mpeg4", "-c:a", "aac", "-b:a", "96k"}},
	}
	for i, c := range cases {
		date := fmt.Sprintf("2026-10-%02d", 4+i)
		ts.file(t, date+"/x", "")
		raw := filepath.Join(ts.root, date, date+"_raw"+c.ext)
		if _, err := ff.Run(ctx, append(append([]string{"-y"}, c.args...), raw)...); err != nil {
			t.Fatalf("%s: making it: %v", c.ext, err)
		}
		if st := waitAnalysis(t, ts, date); st.State != "done" {
			t.Fatalf("%s: analysis %+v", c.ext, st)
		}
		paths, _ := normalizer.NewPaths(filepath.Join(ts.root, date))
		if got := paths.Source(); filepath.Ext(got) != c.becomes || (c.becomes != c.ext && fileExists(raw)) {
			t.Errorf("%s: the recording is %s now", c.ext, got)
		}
		q := defaultRequest()
		q.Sections = []span{{Start: 21, End: 79}}
		q.Flac, q.Opus = false, false
		rec := ts.do(t, "POST", "/api/run?path="+date, q)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s: run: %d %s", c.ext, rec.Code, rec.Body)
		}
		snaps := events(t, hs.URL+"/api/jobs/"+decode[jobSnapshot](t, rec).ID+"/events")
		if last := snaps[len(snaps)-1]; last.State != "done" {
			t.Fatalf("%s: run %+v", c.ext, last)
		}
		if !fileExists(filepath.Join(ts.root, date, "original", date+"_raw"+c.becomes)) {
			t.Errorf("%s: not kept in original/ as %s", c.ext, c.becomes)
		}
		mp3 := filepath.Join(ts.root, date, date+".mp3")
		if d := duration(t, mp3); d < 57.9 || d > 58.2 {
			t.Errorf("%s: the MP3 is %.2f s, want 58", c.ext, d)
		}
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "stream=index", "-of", "csv=p=0", mp3).Output()
		if err != nil || strings.TrimSpace(string(out)) != "" {
			t.Errorf("%s: the MP3 has a picture or video (%q, %v)", c.ext, out, err)
		}
	}
}

// A sample is a few seconds of the recording with the processing settings, as an MP3; it is refused for
// bad settings, a missing recording and while another conversion is running.
func TestIntegrationSample(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	os.MkdirAll(filepath.Join(ts.root, "2026", "2026-10-04"), 0o755)
	service(t, ff, filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04_raw.wav"))
	if st := waitAnalysis(t, ts, "2026/2026-10-04"); st.State != "done" {
		t.Fatalf("analysis: %+v", st)
	}
	const settings = `{"noiseReduction":6,"roomSound":30,"leveling":"normal","compression":"light","targetLufs":-19,"truePeak":-1.5,"mp3Bitrate":"96k","opusBitrate":"48k"}`
	rec := ts.do(t, "POST", "/api/sample?path=2026/2026-10-04&at=25", json.RawMessage(settings))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.Len() < 10_000 {
		t.Fatalf("sample: %d %s %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	out := filepath.Join(t.TempDir(), "sample.mp3")
	os.WriteFile(out, rec.Body.Bytes(), 0o644)
	if d := duration(t, out); d < 29 || d > 31 {
		t.Errorf("sample lasts %.1f s", d)
	}
	if rec := ts.do(t, "POST", "/api/sample?path=2026/2026-10-04&at=25", json.RawMessage(`{"leveling":"loud","targetLufs":-19,"truePeak":-1.5,"mp3Bitrate":"96k","opusBitrate":"48k"}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("bad setting: %d", rec.Code)
	}
	if rec := ts.do(t, "POST", "/api/sample?path=2026/2099-01-01&at=0", json.RawMessage(settings)); rec.Code < 400 {
		t.Errorf("no recording: %d", rec.Code)
	}
	// Nothing is left in the cache folder.
	if files, _ := filepath.Glob(filepath.Join(ts.cfg.CacheDir, "sample-*")); len(files) != 0 {
		t.Errorf("left over: %v", files)
	}
}
