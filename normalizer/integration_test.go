package normalizer

// Integration tests: run the whole pipeline with real ffmpeg on synthetic
// recordings. They are skipped when ffmpeg/ffprobe are missing; the Docker
// build installs both, so `docker build .` always runs them.

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const date = "2026-10-04"

func requireFFmpeg(t *testing.T) FFmpeg {
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
	return FFmpeg{Bin: bin}
}

// service writes a fake service recording: loud music, 60 s of quieter
// "speech" from 20 s to 80 s, then loud music again (100 s in all).
func service(t *testing.T, ff FFmpeg, path string, channels int) {
	t.Helper()
	expr := "if(between(t,20,80), 0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.5*sin(2*PI*440*t))"
	if channels == 2 {
		expr += "|" + expr
	}
	codec := "flac"
	if filepath.Ext(path) == ".wav" {
		codec = "pcm_s24le"
	}
	if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i",
		"aevalsrc=exprs='"+expr+"':s=48000:d=100", "-c:a", codec, path); err != nil {
		t.Fatal(err)
	}
}

// sermon writes a hand-cut edit: 60 s of "speech" only.
func sermon(t *testing.T, ff FFmpeg, path string) {
	t.Helper()
	if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i",
		"aevalsrc=0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)):s=48000:d=60", "-c:a", "flac", path); err != nil {
		t.Fatal(err)
	}
}

type probe struct {
	duration   float64
	channels   int
	sampleRate int
}

func probeFile(t *testing.T, path string) probe {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=channels,sample_rate:format=duration", "-of", "default=nw=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var p probe
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "duration":
			p.duration, _ = strconv.ParseFloat(v, 64)
		case "channels":
			p.channels, _ = strconv.Atoi(v)
		case "sample_rate":
			p.sampleRate, _ = strconv.Atoi(v)
		}
	}
	return p
}

func fileMD5(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", md5.Sum(b))
}

func dateFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), date)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, ctx context.Context, ff FFmpeg, dir, archive string, opt Options) error {
	t.Helper()
	paths, err := NewPaths(dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	p := &Pipeline{P: paths, Opt: opt, FF: ff, Out: &out}
	err = p.Run(ctx)
	t.Logf("output:\n%s", out.String())
	return err
}

// listFiles returns the file names in dir (recursively, relative to dir).
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	return names
}

func wantFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := listFiles(t, dir)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("files in %s:\n got  %v\n want %v", dir, got, want)
	}
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// checkOutputs checks the master, MP3 and Opus against the targets.
func checkOutputs(t *testing.T, ff FFmpeg, dir string, wantDuration float64) {
	t.Helper()
	master := probeFile(t, filepath.Join(dir, date+".flac"))
	if master.channels != 1 || master.sampleRate != 48000 || !near(master.duration, wantDuration, 0.1) {
		t.Errorf("master: %+v, want mono 48 kHz %.1f s", master, wantDuration)
	}
	for _, ext := range []string{".flac", ".mp3", ".opus"} {
		f := filepath.Join(dir, date+ext)
		if pr := probeFile(t, f); pr.channels != 1 {
			t.Errorf("%s: %d channels, want mono", ext, pr.channels)
		}
		m, err := ff.Measure(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if !near(m.LUFS, -19, 1) || m.TruePeak > -1.0+0.05 {
			t.Errorf("%s: %.1f LUFS / %.1f dBTP, want -19 ±1 LUFS and <= -1 dBTP", ext, m.LUFS, m.TruePeak)
		}
	}
}

func TestIntegrationNoArchiveCut(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	raw := filepath.Join(dir, date+"_raw.wav")
	service(t, ff, raw, 2)
	rawSum := fileMD5(t, raw)

	opt := DefaultOptions()
	opt.Start, opt.End = 20, 80
	if err := run(t, context.Background(), ff, dir, "", opt); err != nil {
		t.Fatal(err)
	}
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus", date+"_raw.wav")
	if fileMD5(t, raw) != rawSum {
		t.Error("the raw recording was changed")
	}
	checkOutputs(t, ff, dir, 60)

	// A second run skips existing outputs.
	master := filepath.Join(dir, date+".flac")
	before, _ := os.Stat(master)
	if err := run(t, context.Background(), ff, dir, "", opt); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(master); !after.ModTime().Equal(before.ModTime()) {
		t.Error("the master was rewritten without --force")
	}

	// --force with new times cuts again.
	opt.Force, opt.Start = true, 50
	if err := run(t, context.Background(), ff, dir, "", opt); err != nil {
		t.Fatal(err)
	}
	if d := probeFile(t, master).duration; !near(d, 30, 0.1) {
		t.Errorf("master after --force: %.2f s, want 30", d)
	}
}

func TestIntegrationArchiveRelativeWav(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.wav"), 2)

	opt := DefaultOptions()
	opt.Start, opt.End = 20, 80
	if err := run(t, context.Background(), ff, dir, "arkiv", opt); err != nil {
		t.Fatal(err)
	}
	// The WAV is verified and deleted; raw and the cut edit are archived as FLAC.
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus",
		"arkiv/"+date+"_edit.flac", "arkiv/"+date+"_raw.flac")
	if d := probeFile(t, filepath.Join(dir, "arkiv", date+"_edit.flac")).duration; !near(d, 60, 0.01) {
		t.Errorf("edit: %.3f s, want 60", d)
	}
	if pr := probeFile(t, filepath.Join(dir, "arkiv", date+"_raw.flac")); pr.channels != 2 || !near(pr.duration, 100, 0.01) {
		t.Errorf("archived raw: %+v, want the whole stereo recording", pr)
	}
	checkOutputs(t, ff, dir, 60)
}

// archiveDir prefers /dev/shm, usually another file system, to exercise the
// copy fallback when moving into the archive.
func archiveDir(t *testing.T) string {
	if d, err := os.MkdirTemp("/dev/shm", "archive"); err == nil {
		t.Cleanup(func() { os.RemoveAll(d) })
		return d
	}
	return t.TempDir()
}

func TestIntegrationArchiveAbsoluteManualEditMono(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.flac"), 1)
	sermon(t, ff, filepath.Join(dir, date+"_edit.flac"))
	archive := archiveDir(t)

	if err := run(t, context.Background(), ff, dir, archive, DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus")
	wantFiles(t, archive, date+"_edit.flac", date+"_raw.flac")
	checkOutputs(t, ff, dir, 60)
}

func TestIntegrationNoArchiveManualEdit(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	sermon(t, ff, filepath.Join(dir, date+"_edit.flac"))

	if err := run(t, context.Background(), ff, dir, "", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	wantFiles(t, dir, date+".flac", date+"_edit.flac", date+".mp3", date+".opus")
	checkOutputs(t, ff, dir, 60)
}

// An interrupted run must not leave half-written files that a later run would skip.
func TestIntegrationInterrupted(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.flac"), 2)

	opt := DefaultOptions()
	opt.End = 100
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := run(t, ctx, ff, dir, "", opt); err == nil {
		t.Skip("finished before the timeout; nothing to check")
	}
	wantFiles(t, dir, date+"_raw.flac")

	if err := run(t, context.Background(), ff, dir, "", opt); err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, ff, dir, 100)
}

// maxVolume is the peak level in dB of dur seconds from start.
func maxVolume(t *testing.T, ff FFmpeg, path string, start, dur float64) float64 {
	t.Helper()
	res, err := ff.Run(context.Background(), "-ss", secs(start), "-t", secs(dur), "-i", path,
		"-af", "volumedetect", "-f", "null", "-")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(res.Stderr, "max_volume: ")
	if !ok {
		t.Fatalf("no max_volume in:\n%s", res.Stderr)
	}
	v, err := strconv.ParseFloat(strings.Fields(after)[0], 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Fades with a cut that runs to the end of the recording (length from the file).
func TestIntegrationFades(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.flac"), 2)

	opt := DefaultOptions()
	opt.Start, opt.FadeIn, opt.FadeOut = 20, 3, 3
	if err := run(t, context.Background(), ff, dir, "", opt); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(dir, date+".flac")
	if d := probeFile(t, master).duration; !near(d, 80, 0.1) {
		t.Fatalf("master: %.2f s, want 80", d)
	}
	head := maxVolume(t, ff, master, 0, 0.5)
	speech := maxVolume(t, ff, master, 10, 1)
	music := maxVolume(t, ff, master, 70, 1)
	tail := maxVolume(t, ff, master, 79.5, 0.5)
	t.Logf("head %.1f dB, speech %.1f dB, music %.1f dB, tail %.1f dB", head, speech, music, tail)
	// The first and last 0.5 s of a 3 s linear fade are at most 1/6 gain (-15.6 dB).
	if head > speech-10 {
		t.Errorf("fade in: head %.1f dB vs speech %.1f dB", head, speech)
	}
	if tail > music-10 {
		t.Errorf("fade out: tail %.1f dB vs music %.1f dB", tail, music)
	}

	// Fades longer than the sermon are an error.
	opt.Force, opt.FadeIn, opt.FadeOut = true, 50, 50
	if err := run(t, context.Background(), ff, dir, "", opt); err == nil || !strings.Contains(err.Error(), "longer than the sermon") {
		t.Errorf("expected a fade length error, got %v", err)
	}
}
