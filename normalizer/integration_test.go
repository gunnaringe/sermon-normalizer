package normalizer

// Integration tests: run the whole pipeline with real ffmpeg on synthetic
// recordings. They are skipped when ffmpeg/ffprobe are missing; the Docker
// build installs both, so `docker build .` always runs them.

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
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

func run(t *testing.T, ctx context.Context, ff FFmpeg, dir string, opt Options) error {
	t.Helper()
	paths, err := NewPaths(dir)
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

// checkOutputs checks the master, MP3 and Opus against the mono targets.
func checkOutputs(t *testing.T, ff FFmpeg, dir string, wantDuration float64) {
	t.Helper()
	checkOutputsAs(t, ff, dir, wantDuration, 1, -19)
}

func checkOutputsAs(t *testing.T, ff FFmpeg, dir string, wantDuration float64, channels int, lufs float64) {
	t.Helper()
	master := probeFile(t, filepath.Join(dir, date+".flac"))
	if master.channels != channels || master.sampleRate != 48000 || !near(master.duration, wantDuration, 0.1) {
		t.Errorf("master: %+v, want %d channels, 48 kHz, %.1f s", master, channels, wantDuration)
	}
	for _, ext := range []string{".flac", ".mp3", ".opus"} {
		f := filepath.Join(dir, date+ext)
		if pr := probeFile(t, f); pr.channels != channels {
			t.Errorf("%s: %d channels, want %d", ext, pr.channels, channels)
		}
		m, err := ff.Measure(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if !near(m.LUFS, lufs, 1) || m.TruePeak > -1.0+0.05 {
			t.Errorf("%s: %.1f LUFS / %.1f dBTP, want %g ±1 LUFS and <= -1 dBTP", ext, m.LUFS, m.TruePeak, lufs)
		}
	}
}

func TestIntegrationCut(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	raw := filepath.Join(dir, date+"_raw.wav")
	service(t, ff, raw, 2)

	opt := DefaultOptions()
	opt.Start, opt.End = 20, 80
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	// The WAV was verified and replaced by a FLAC in original/; no edit file is made.
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus", "original/"+date+"_raw.flac")
	if pr := probeFile(t, filepath.Join(dir, "original", date+"_raw.flac")); pr.channels != 2 || !near(pr.duration, 100, 0.01) {
		t.Errorf("original: %+v, want the whole stereo recording", pr)
	}
	checkOutputs(t, ff, dir, 60)

	// A second run skips existing outputs.
	master := filepath.Join(dir, date+".flac")
	before, _ := os.Stat(master)
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(master); !after.ModTime().Equal(before.ModTime()) {
		t.Error("the master was rewritten without Force")
	}

	// Force with new times cuts again, from the original.
	opt.Force, opt.Start = true, 50
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	if d := probeFile(t, master).duration; !near(d, 30, 0.1) {
		t.Errorf("master after Force: %.2f s, want 30", d)
	}
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
	if err := run(t, ctx, ff, dir, opt); err == nil {
		t.Skip("finished before the timeout; nothing to check")
	}
	wantFiles(t, dir, "original/"+date+"_raw.flac")

	if err := run(t, context.Background(), ff, dir, opt); err != nil {
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
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
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
	if err := run(t, context.Background(), ff, dir, opt); err == nil || !strings.Contains(err.Error(), "longer than the sermon") {
		t.Errorf("expected a fade length error, got %v", err)
	}
}

// Only the selected outputs are made. Without FLAC, the master MP3/Opus need
// is temporary and removed afterwards.
func TestIntegrationSelectedOutputs(t *testing.T) {
	ff := requireFFmpeg(t)
	for _, c := range []struct {
		outputs Outputs
		want    []string
	}{
		{Outputs{Mp3: true}, []string{date + ".mp3"}},
		{Outputs{Opus: true, Mp3: true}, []string{date + ".mp3", date + ".opus"}},
		{Outputs{Flac: true}, []string{date + ".flac"}},
	} {
		t.Run(c.outputs.String(), func(t *testing.T) {
			dir := dateFolder(t)
			sermon(t, ff, filepath.Join(dir, date+"_raw.flac"))
			opt := DefaultOptions()
			opt.End = 60
			opt.Outputs = c.outputs
			if err := run(t, context.Background(), ff, dir, opt); err != nil {
				t.Fatal(err)
			}
			wantFiles(t, dir, append(c.want, "original/"+date+"_raw.flac")...)
			for _, f := range c.want {
				m, err := ff.Measure(context.Background(), filepath.Join(dir, f))
				if err != nil {
					t.Fatal(err)
				}
				if !near(m.LUFS, -19, 1) || m.TruePeak > -1.0+0.05 {
					t.Errorf("%s: %.1f LUFS / %.1f dBTP", f, m.LUFS, m.TruePeak)
				}
			}
		})
	}
}

// The temporary master is removed even when the run fails.
func TestIntegrationTemporaryMasterRemovedOnError(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	sermon(t, ff, filepath.Join(dir, date+"_raw.flac"))
	opt := DefaultOptions()
	opt.End = 60
	opt.Outputs = Outputs{Mp3: true}
	opt.Mp3Bitrate = "not-a-bitrate"
	if err := run(t, context.Background(), ff, dir, opt); err == nil {
		t.Fatal("expected the MP3 encode to fail")
	}
	wantFiles(t, dir, "original/"+date+"_raw.flac")
}

// serviceWithGaps is service() with 2 s of silence at both transitions,
// as between the parts of a real service.
func serviceWithGaps(t *testing.T, ff FFmpeg, path string) {
	t.Helper()
	expr := "if(between(t,19,21)+between(t,79,81), 0, if(between(t,21,79), 0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.5*sin(2*PI*440*t)))"
	if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i",
		"aevalsrc=exprs='"+expr+"|"+expr+"':s=48000:d=100", "-c:a", "flac", path); err != nil {
		t.Fatal(err)
	}
}

// levels returns the RMS level in dBFS of every window seconds of path,
// mixed to mono, as the web app reads them.
func levels(f FFmpeg, path string, window float64) ([]float64, error) {
	cmd := exec.Command(f.Bin, "-nostdin", "-hide_banner", "-v", "error",
		"-i", path, "-ac", "1", "-ar", fmt.Sprint(LevelRate), "-f", "s16le", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := ReadLevels(out, window, nil)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed (%w):\n%s", err, lastLines(stderr.String(), 15))
	}
	return data.RMS, readErr
}

func TestIntegrationLevelsAndSilences(t *testing.T) {
	ff := requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "service.flac")
	serviceWithGaps(t, ff, path)

	lv, err := levels(ff, path, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	if len(lv) != 1000 {
		t.Fatalf("%d lv, want 1000 (100 s / 0.1 s)", len(lv))
	}
	music, speech, gap := lv[100], lv[500], lv[200]
	t.Logf("music %.1f dB, speech %.1f dB, gap %.1f dB", music, speech, gap)
	if music < speech+10 || gap != SilenceFloor {
		t.Errorf("music %.1f, speech %.1f, gap %.1f: want music well above speech, gap silent", music, speech, gap)
	}
	got := Silences(lv, 0.1, 1)
	if len(got) != 2 || !near(got[0].Start, 19, 0.15) || !near(got[0].End, 21, 0.15) ||
		!near(got[1].Start, 79, 0.15) || !near(got[1].End, 81, 0.15) {
		t.Errorf("silences %v, want ~[19 21] and ~[79 81]", got)
	}
}

func TestIntegrationProgressEvents(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	sermon(t, ff, filepath.Join(dir, date+"_raw.flac"))
	paths, _ := NewPaths(dir)
	var steps []string
	var fractions []float64
	opt := DefaultOptions()
	opt.End = 60
	p := &Pipeline{P: paths, Opt: opt, FF: ff, Out: io.Discard,
		OnStep:     func(s string) { steps = append(steps, s) },
		OnProgress: func(f float64) { fractions = append(fractions, f) },
	}
	if err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 || !strings.HasPrefix(steps[1], "3. Master") || !strings.HasPrefix(steps[3], "Result") {
		t.Errorf("steps: %q", steps)
	}
	var reachedEnd bool
	for i, f := range fractions {
		if f < 0 || f > 1 {
			t.Errorf("fraction %d out of range: %v", i, f)
		}
		reachedEnd = reachedEnd || f > 0.95
	}
	if len(fractions) < 3 || !reachedEnd {
		t.Errorf("fractions %v: want several, reaching ~1", fractions)
	}
}

// Stereo keeps two channels at the stereo target, from stereo and from mono input.
func TestIntegrationStereo(t *testing.T) {
	ff := requireFFmpeg(t)
	for _, inChannels := range []int{2, 1} {
		t.Run(fmt.Sprintf("%d-channel input", inChannels), func(t *testing.T) {
			dir := dateFolder(t)
			service(t, ff, filepath.Join(dir, date+"_raw.flac"), inChannels)
			opt := DefaultOptions()
			opt.SetStereo(true)
			opt.Start, opt.End = 20, 80
			if err := run(t, context.Background(), ff, dir, opt); err != nil {
				t.Fatal(err)
			}
			checkOutputsAs(t, ff, dir, 60, 2, -16)
		})
	}
}

// Stereo output warns when the sermon is mono or its channels are (nearly) the same.
func TestIntegrationStereoWarnings(t *testing.T) {
	ff := requireFFmpeg(t)
	speech := "0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t))"
	for _, c := range []struct {
		name, exprs, want string
	}{
		{"mono", speech, "the source is mono"},
		{"identical", speech + "|" + speech, "are identical"},
		{"nearly identical", speech + "|" + speech + "+0.0005*sin(2*PI*1234*t)", "are nearly identical"},
		{"real stereo", speech + "|0.5*" + speech + "+0.05*sin(2*PI*330*t)", ""},
		{"left only", speech + "|0", "the right channel of the source is silent"},
		{"mostly right", "0.001*" + speech + "|" + speech, "the left channel of the source is 60 dB quieter"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := dateFolder(t)
			if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i",
				"aevalsrc=exprs='"+c.exprs+"':s=48000:d=20", "-c:a", "flac", filepath.Join(dir, date+"_raw.flac")); err != nil {
				t.Fatal(err)
			}
			opt := DefaultOptions()
			opt.End = 20
			opt.SetStereo(true)
			opt.Outputs = Outputs{Flac: true}
			paths, _ := NewPaths(dir)
			var out bytes.Buffer
			if err := (&Pipeline{P: paths, Opt: opt, FF: ff, Out: &out}).Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			warned := strings.Contains(out.String(), "WARNING: ") && strings.Contains(out.String(), "Choose mono")
			if c.want == "" && warned || c.want != "" && !strings.Contains(out.String(), c.want) {
				t.Errorf("output:\n%s", out.String())
			}
			// Mono output never checks.
			opt.SetStereo(false)
			opt.Force = true
			out.Reset()
			(&Pipeline{P: paths, Opt: opt, FF: ff, Out: &out}).Run(context.Background())
			if strings.Contains(out.String(), "Choose mono") {
				t.Errorf("mono output warned:\n%s", out.String())
			}
		})
	}
}

// The tags are written into all three outputs; with the sermon's own tags
// cleared, an empty field removes the tag.
func TestIntegrationTags(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	sermon(t, ff, filepath.Join(dir, date+"_raw.flac"))
	opt := DefaultOptions()
	opt.End = 60
	opt.Tags = Tags{Title: "2026-10-04 Kveld", Artist: "Norkirken Melhus", Date: "2026-10-04"}
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".flac", ".mp3", ".opus"} {
		// ffmpeg lists the tags of the file and its stream (Opus keeps them there).
		res, err := ff.Run(context.Background(), "-i", filepath.Join(dir, date+ext), "-t", "0", "-f", "null", "-")
		if err != nil {
			t.Fatal(err)
		}
		tags := strings.Join(strings.Fields(strings.ToLower(res.Stderr)), " ")
		for _, want := range []string{"title : 2026-10-04 kveld", "artist : norkirken melhus"} {
			if !strings.Contains(tags, want) {
				t.Errorf("%s: no %q in\n%s", ext, want, res.Stderr)
			}
		}
		if strings.Contains(tags, "album :") {
			t.Errorf("%s: an empty album should leave no tag:\n%s", ext, res.Stderr)
		}
	}
}

// Parts cut out of the selection are left out.
func TestIntegrationCuts(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.wav"), 2)

	opt := DefaultOptions()
	opt.Start, opt.End = 10, 90
	opt.Cuts = []Span{{60, 65}, {30, 40}} // 10-30, 40-60, 65-90 = 65 s
	opt.FadeIn, opt.FadeOut = 1, 1
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, ff, dir, 65)
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus", "original/"+date+"_raw.flac")

	// Cuts that leave nothing fail clearly.
	dir = dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.flac"), 1)
	opt = DefaultOptions()
	opt.Start, opt.End = 10, 90
	opt.Cuts = []Span{{10, 90}}
	if err := run(t, context.Background(), ff, dir, opt); err == nil || !strings.Contains(err.Error(), "leave nothing") {
		t.Errorf("all cut: err = %v", err)
	}
}

// Pauses are silence in the sermon, where a part ends and inside a part.
func TestIntegrationPauses(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.wav"), 2)

	opt := DefaultOptions()
	opt.Start, opt.End = 10, 90
	opt.Cuts = []Span{{30, 40}}
	opt.Pauses = []Pause{{30, 3}, {60, 2}} // 10-30, 3 s, 40-60, 2 s, 60-90: 70 s and 5 s of silence
	opt.FadeIn, opt.FadeOut = 1, 1
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	checkOutputs(t, ff, dir, 75)
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus", "original/"+date+"_raw.flac")
}

// The processing settings: with the compressor, stronger leveling and some room sound kept, the files
// still reach the target and stay under the peak ceiling; no file keeps a temporary one behind.
func TestIntegrationProcessing(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := dateFolder(t)
	service(t, ff, filepath.Join(dir, date+"_raw.wav"), 2)

	opt := DefaultOptions()
	opt.Start, opt.End = 10, 90
	opt.NoiseReduction, opt.RoomSound = 10, 80
	opt.Leveling, opt.Compression = "strong", "strong"
	opt.TargetLUFS = -17
	if err := run(t, context.Background(), ff, dir, opt); err != nil {
		t.Fatal(err)
	}
	checkOutputsAs(t, ff, dir, 80, 1, -17)
	wantFiles(t, dir, date+".flac", date+".mp3", date+".opus", "original/"+date+"_raw.flac")
}

// A sample is a few seconds of the recording with the processing, as an MP3 at the target loudness.
func TestIntegrationSample(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.flac")
	service(t, ff, raw, 2)
	dst := filepath.Join(dir, "sample.mp3")
	opt := DefaultOptions()
	opt.Compression = "light"
	if err := ff.Sample(context.Background(), raw, opt, 30, 12, dst); err != nil {
		t.Fatal(err)
	}
	if pr := probeFile(t, dst); pr.channels != 1 || !near(pr.duration, 12, 0.2) {
		t.Errorf("sample: %+v", pr)
	}
	m, err := ff.Measure(context.Background(), dst)
	if err != nil || !near(m.LUFS, -19, 1) {
		t.Errorf("sample: %.1f LUFS, %v", m.LUFS, err)
	}
	if files := listFiles(t, dir); len(files) != 2 { // raw.flac and sample.mp3: nothing left over
		t.Errorf("files: %v", files)
	}
}

// The verdict on a recording's channels, and its format and metadata.
func TestIntegrationVerdictAndProbe(t *testing.T) {
	ff := requireFFmpeg(t)
	speech := "0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t))"
	for _, c := range []struct {
		exprs string
		want  ChannelVerdict
	}{
		{speech, ChannelVerdict{Kind: "mono"}},
		{speech + "|" + speech, ChannelVerdict{Kind: "identical"}},
		{speech + "|0.5*" + speech + "+0.05*sin(2*PI*330*t)", ChannelVerdict{Kind: "stereo"}},
		{speech + "|0", ChannelVerdict{Kind: "one-sided", Quiet: "right"}},
		{"0.001*" + speech + "|" + speech, ChannelVerdict{Kind: "one-sided", Quiet: "left", By: 60}},
	} {
		path := filepath.Join(t.TempDir(), "x.flac")
		if _, err := ff.Run(context.Background(), "-y", "-f", "lavfi", "-i", "aevalsrc=exprs='"+c.exprs+"':s=48000:d=5",
			"-c:a", "flac", "-sample_fmt", "s32", "-metadata", "title=Gudstjeneste", path); err != nil {
			t.Fatal(err)
		}
		check, err := ff.CheckChannels(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Verdict(); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.exprs, got, c.want)
		}
		p, err := ff.ProbeFile(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if p.Container != "flac" || p.Codec != "flac" || p.SampleRate != 48000 || p.Bits != 24 || !near(p.Duration, 5, 0.01) ||
			p.Metadata["title"] != "Gudstjeneste" || p.Channels < 1 {
			t.Errorf("probe: %+v", p)
		}
	}
}
