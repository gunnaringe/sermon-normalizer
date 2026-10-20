package normalizer

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleEbur128 = `[Parsed_ebur128_0 @ 000001] t: 0.1  TARGET:-23 LUFS    M: -120.7 S: -120.7     I: -70.0 LUFS       LRA:   0.0 LU  FTPK: -inf -inf dBFS  TPK: -inf -inf dBFS
[Parsed_ebur128_0 @ 000001] Summary:

  Integrated loudness:
    I:         -19.5 LUFS
    Threshold: -30.8 LUFS

  Loudness range:
    LRA:         5.7 LU
    Threshold: -40.8 LUFS
    LRA low:   -23.0 LUFS
    LRA high:  -17.3 LUFS

  True peak:
    Peak:       -1.5 dBFS
`

func TestParseEbur128(t *testing.T) {
	got, err := parseEbur128(sampleEbur128)
	if err != nil {
		t.Fatal(err)
	}
	want := Loudness{LUFS: -19.5, TruePeak: -1.5, LRA: 5.7}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseEbur128Missing(t *testing.T) {
	if _, err := parseEbur128("nothing here"); err == nil {
		t.Error("expected an error")
	}
}

func TestCorrection(t *testing.T) {
	cases := []struct {
		name     string
		m        Loudness
		wantGain float64
		needed   bool
	}{
		{"ok", Loudness{LUFS: -19.1, TruePeak: -1.7}, 0.1, false},
		{"peak too high", Loudness{LUFS: -18.6, TruePeak: 0.3}, -1.3, true},
		{"too low, room for more", Loudness{LUFS: -19.9, TruePeak: -2.5}, 0.9, true},
		{"too low, peak limits", Loudness{LUFS: -19.9, TruePeak: -1.3}, 0.3, true},
	}
	for _, c := range cases {
		gain, needed := correction(-19, c.m)
		if diff := gain - c.wantGain; diff > 0.001 || diff < -0.001 || needed != c.needed {
			t.Errorf("%s: gain=%.2f needed=%v, want %.2f %v", c.name, gain, needed, c.wantGain, c.needed)
		}
	}
}

func TestNewPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := NewPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Date != "2026-10-04" || p.Original != filepath.Join(dir, "original") || p.RawOriginal() != "" ||
		filepath.Base(p.Master) != "2026-10-04.flac" ||
		filepath.Base(p.Mp3) != "2026-10-04.mp3" ||
		filepath.Base(p.Opus) != "2026-10-04.opus" {
		t.Errorf("unexpected paths: %+v", p)
	}
	if _, err := NewPaths(filepath.Join(dir, "missing")); err == nil {
		t.Error("expected an error for a missing folder")
	}
}

// newTestPipeline makes a date folder with the given files and a pipeline for it.
func newTestPipeline(t *testing.T, files ...string) *Pipeline {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	paths, err := NewPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(paths.Original, 0o755)
	for _, f := range files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(dir, f)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Pipeline{P: paths, Opt: DefaultOptions(), Out: io.Discard}
}

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "b")
	os.WriteFile(b, nil, 0o644)
	if got := firstExisting(filepath.Join(dir, "a"), b); got != b {
		t.Errorf("got %q", got)
	}
	if got := firstExisting(filepath.Join(dir, "a")); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestMasterFilter(t *testing.T) {
	p := &Pipeline{Opt: DefaultOptions()}
	f := p.masterFilter(0)
	for _, want := range []string{"aformat=channel_layouts=mono", "afftdn=nr=12", "dynaudnorm", "loudnorm=I=-19:TP=-1.5", "aresample=48000"} {
		if !strings.Contains(f, want) {
			t.Errorf("%q missing from %q", want, f)
		}
	}
	if strings.Contains(f, "afade") {
		t.Error("no fades by default")
	}
	p.Opt.NoiseReduction = 0
	if strings.Contains(p.masterFilter(0), "afftdn") {
		t.Error("afftdn should be off")
	}
}

func TestMasterFilterFades(t *testing.T) {
	p := &Pipeline{Opt: DefaultOptions()}
	p.Opt.FadeIn, p.Opt.FadeOut = 2, 3.5
	f := p.masterFilter(600)
	if !strings.HasSuffix(f, ",aresample=48000,afade=t=in:d=2,afade=t=out:st=596.500:d=3.5") {
		t.Errorf("fades should come last, after loudnorm: %q", f)
	}
}

func TestParseDuration(t *testing.T) {
	got, err := parseDuration("  Duration: 01:12:05.50, start: 0.000000, bitrate: 1411 kb/s")
	if err != nil || got != 4325.5 {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := parseDuration("nothing"); err == nil {
		t.Error("expected an error")
	}
}

func TestFormatTime(t *testing.T) {
	for in, want := range map[float64]string{2480: "0:41:20", 4325: "1:12:05", 2480.5: "0:41:20.500"} {
		if got := FormatTime(in); got != want {
			t.Errorf("FormatTime(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "sub", "b")
	os.WriteFile(src, []byte("data"), 0o644)
	if err := MoveFile(src, dst); err == nil {
		t.Fatal("expected an error when the target folder is missing")
	}
	if !exists(src) {
		t.Fatal("source must survive a failed move")
	}
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	if err := MoveFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" || exists(src) {
		t.Error("move failed")
	}
}

func TestOutputsString(t *testing.T) {
	if s := (Outputs{Flac: true, Opus: true}).String(); s != "flac,opus" {
		t.Errorf("String() = %q", s)
	}
}

func TestReadLevels(t *testing.T) {
	// 4 samples at full scale, 4 at half, 2 of silence: windows of 4.
	var pcm []byte
	for _, v := range []int16{32767, -32767, 32767, -32767, 16384, -16384, 16384, -16384, 0, 0} {
		pcm = append(pcm, byte(v), byte(uint16(v)>>8))
	}
	data, err := ReadLevels(bytes.NewReader(pcm), 4.0/LevelRate, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := data.RMS
	if len(data.Peak) != 3 || math.Abs(data.Peak[0]-1) > 0.001 || math.Abs(data.Peak[1]-0.5) > 0.001 || data.Peak[2] != 0 {
		t.Errorf("peaks %v", data.Peak)
	}
	want := []float64{0, -6.02, SilenceFloor}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.01 {
			t.Errorf("level %d: %.2f, want %.2f", i, got[i], want[i])
		}
	}
}

func TestSilences(t *testing.T) {
	// 0.5 s windows: loud, a 1.5 s gap, quieter speech, a 0.5 s pause, a 2 s gap at the end.
	levels := []float64{-10, -10, -10, -70, -70, -70, -30, -30, -30, -30, -70, -30, -30, -30, -70, -70, -70, -70}
	got := Silences(levels, 0.5, 1)
	want := []Span{{1.5, 3}, {7, 9}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if Silences(nil, 0.5, 1) != nil {
		t.Error("no levels, no silences")
	}
}

func TestProgressWriter(t *testing.T) {
	var got []float64
	w := &progressWriter{fn: func(s float64) { got = append(got, s) }}
	w.Write([]byte("frame=1\nout_time_us=1500"))
	w.Write([]byte("000\nout_time_us=N/A\nout_time_us=3000000\nprogress=end\n"))
	if !reflect.DeepEqual(got, []float64{1.5, 3}) {
		t.Errorf("got %v", got)
	}
}

func TestSetStereo(t *testing.T) {
	o := DefaultOptions()
	o.SetStereo(true)
	if !o.Stereo || o.TargetLUFS != -16 || o.Mp3Bitrate != "128k" || o.OpusBitrate != "96k" || o.Channels() != "stereo" {
		t.Errorf("stereo defaults: %+v", o)
	}
	o.SetStereo(false)
	if !reflect.DeepEqual(o, DefaultOptions()) {
		t.Errorf("back to mono: %+v", o)
	}
	// Values changed from the defaults stay.
	o.TargetLUFS, o.Mp3Bitrate = -18, "160k"
	o.SetStereo(true)
	if o.TargetLUFS != -18 || o.Mp3Bitrate != "160k" || o.OpusBitrate != "96k" {
		t.Errorf("changed values: %+v", o)
	}
}

func TestMasterFilterStereo(t *testing.T) {
	p := &Pipeline{Opt: DefaultOptions()}
	p.Opt.SetStereo(true)
	if f := p.masterFilter(0); !strings.HasPrefix(f, "aformat=channel_layouts=stereo,") || !strings.Contains(f, "loudnorm=I=-16:") {
		t.Errorf("filter %q", f)
	}
}

func TestParseChannels(t *testing.T) {
	for header, want := range map[string]int{
		"  Stream #0:0: Audio: flac, 48000 Hz, stereo, s32 (24 bit)":                         2,
		"  Stream #0:0: Audio: pcm_s16le ([1][0][0][0] / 0x0001), 44100 Hz, 1 channels, s16": 1,
		"  Stream #0:0: Audio: flac, 48000 Hz, mono, s32":                                    1,
		"  Stream #0:0: Audio: pcm_s24le, 48000 Hz, 5.1(side), s32":                          3,
	} {
		// The output stream's header comes later and must not count.
		got, err := parseChannels(header + "\n  Stream #0:0: Audio: pcm_s16le, 48000 Hz, mono, s16\n")
		if err != nil || got != want {
			t.Errorf("%q: %d, %v; want %d", header, got, err, want)
		}
	}
	if _, err := parseChannels("no audio"); err == nil {
		t.Error("expected an error")
	}
}

func TestParseLevels(t *testing.T) {
	out := `[Parsed_astats_1 @ 0x1] Channel: 1
[Parsed_astats_1 @ 0x1] DC offset: 0.000002
[Parsed_astats_1 @ 0x1] RMS level dB: -20.5
[Parsed_astats_1 @ 0x1] Channel: 2
[Parsed_astats_1 @ 0x1] RMS level dB: -inf
[Parsed_astats_1 @ 0x1] Channel: 3
[Parsed_astats_1 @ 0x1] RMS level dB: -26.5
[Parsed_astats_1 @ 0x1] Channel: 4
[Parsed_astats_1 @ 0x1] RMS level dB: -26.5
[Parsed_astats_1 @ 0x1] Overall
[Parsed_astats_1 @ 0x1] RMS level dB: -25.998939
`
	levels, err := parseLevels(out)
	if err != nil || len(levels) != 4 || levels[1] != -20.5 || !math.IsInf(levels[2], -1) {
		t.Fatalf("levels %v, %v", levels, err)
	}
	c, err := checkFromLevels(levels)
	if err != nil || c.Side != 0 || c.EffectivelyMono() {
		t.Errorf("check %+v, %v", c, err)
	}
	if side, by := c.Quieter(); side != "right" || !math.IsInf(by, 1) {
		t.Errorf("quieter %q %v", side, by)
	}
	if _, err := parseLevels("nothing"); err == nil {
		t.Error("expected an error")
	}
	if _, err := checkFromLevels(map[int]float64{1: -20, 2: -20}); err == nil {
		t.Error("expected an error for missing channels")
	}
}

func TestQuieter(t *testing.T) {
	inf := math.Inf(-1)
	for _, c := range []struct {
		l, r float64
		side string
	}{
		{-20, -24, ""},      // panned a little
		{-20, -45, "right"}, // one-sided
		{-50, -20, "left"},  // one-sided
		{inf, -20, "left"},  // silent
		{inf, inf, ""},      // all silent
	} {
		if side, _ := (ChannelCheck{Channels: 2, Left: c.l, Right: c.r}).Quieter(); side != c.side {
			t.Errorf("%v/%v: %q, want %q", c.l, c.r, side, c.side)
		}
	}
}

func TestEffectivelyMono(t *testing.T) {
	for _, c := range []struct {
		check ChannelCheck
		want  bool
	}{
		{ChannelCheck{Channels: 1}, true},
		{ChannelCheck{Channels: 2, Side: math.Inf(-1)}, true},
		{ChannelCheck{Channels: 2, Side: -45}, true},
		{ChannelCheck{Channels: 2, Side: -12}, false},
		{ChannelCheck{Channels: 6}, false},
	} {
		if got := c.check.EffectivelyMono(); got != c.want {
			t.Errorf("%+v: %v", c.check, got)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultOptions().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Options){
		"no outputs":    func(o *Options) { o.Outputs = Outputs{} },
		"end <= start":  func(o *Options) { o.Start, o.End = 60, 30 },
		"negative fade": func(o *Options) { o.FadeOut = -1 },
		"noise":         func(o *Options) { o.NoiseReduction = 98 },
		"lufs":          func(o *Options) { o.TargetLUFS = 3 },
		"peak":          func(o *Options) { o.TruePeak = 1 },
		"bitrate":       func(o *Options) { o.Mp3Bitrate = " " },
	} {
		o := DefaultOptions()
		change(&o)
		if o.Validate() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestKeep(t *testing.T) {
	for _, c := range []struct {
		name  string
		opt   Options
		end   float64
		wants []Span
	}{
		{"no cuts", Options{Start: 10, End: 50}, 100, []Span{{10, 50}}},
		{"to the end of the recording", Options{Start: 10}, 100, []Span{{10, 100}}},
		{"an end past the recording", Options{Start: 10, End: 500}, 100, []Span{{10, 100}}},
		{"one cut", Options{Start: 10, End: 50, Cuts: []Span{{20, 30}}}, 100, []Span{{10, 20}, {30, 50}}},
		{"unsorted and overlapping", Options{Start: 0, End: 100, Cuts: []Span{{60, 70}, {20, 40}, {30, 45}}}, 100, []Span{{0, 20}, {45, 60}, {70, 100}}},
		{"a cut at the start and one at the end", Options{Start: 10, End: 50, Cuts: []Span{{10, 15}, {45, 50}}}, 100, []Span{{15, 45}}},
		{"everything cut", Options{Start: 10, End: 50, Cuts: []Span{{10, 50}}}, 100, nil},
		{"a sliver is dropped", Options{Start: 10, End: 50, Cuts: []Span{{10.01, 20}}}, 100, []Span{{20, 50}}},
	} {
		if got := c.opt.keep(c.end); !reflect.DeepEqual(got, c.wants) {
			t.Errorf("%s: keep = %v, want %v", c.name, got, c.wants)
		}
	}
}

// Pauses fall where a part ends or inside one, which they split; one at the end of the sermon or
// outside it has no effect.
func TestParts(t *testing.T) {
	for _, c := range []struct {
		name   string
		opt    Options
		kept   []Span
		after  []float64
		length float64
	}{
		{"none", Options{Start: 10, End: 50}, []Span{{10, 50}}, []float64{0}, 40},
		{"at the end of a part", Options{Start: 10, End: 50, Cuts: []Span{{20, 30}}, Pauses: []Pause{{20, 2}}}, []Span{{10, 20}, {30, 50}}, []float64{2, 0}, 32},
		{"inside a part: it is split", Options{Start: 10, End: 50, Pauses: []Pause{{30, 3}}}, []Span{{10, 30}, {30, 50}}, []float64{3, 0}, 43},
		{"two in one part", Options{Start: 10, End: 50, Pauses: []Pause{{40, 1}, {20, 2}}}, []Span{{10, 20}, {20, 40}, {40, 50}}, []float64{2, 1, 0}, 43},
		{"added at one place", Options{Start: 10, End: 50, Cuts: []Span{{20, 30}}, Pauses: []Pause{{20, 2}, {20, 3}}}, []Span{{10, 20}, {30, 50}}, []float64{5, 0}, 35},
		{"at the end of the sermon", Options{Start: 10, End: 50, Pauses: []Pause{{50, 4}}}, []Span{{10, 50}}, []float64{4}, 40},
		{"at the start and outside", Options{Start: 10, End: 50, Pauses: []Pause{{10, 4}, {5, 1}, {70, 2}}}, []Span{{10, 50}}, []float64{0}, 40},
		{"in a cut", Options{Start: 10, End: 50, Cuts: []Span{{20, 30}}, Pauses: []Pause{{25, 4}}}, []Span{{10, 20}, {30, 50}}, []float64{0, 0}, 30},
		{"none for zero", Options{Start: 10, End: 50, Pauses: []Pause{{30, 0}}}, []Span{{10, 50}}, []float64{0}, 40},
	} {
		kept, after := c.opt.parts(100)
		var length float64
		for _, k := range kept {
			length += k.End - k.Start
		}
		length += pauseTotal(after)
		if !reflect.DeepEqual(kept, c.kept) || !reflect.DeepEqual(after, c.after) || length != c.length {
			t.Errorf("%s: parts = %v %v (%v s), want %v %v (%v s)", c.name, kept, after, length, c.kept, c.after, c.length)
		}
	}
}

func TestValidateCuts(t *testing.T) {
	ok := DefaultOptions()
	ok.Start, ok.End = 10, 50
	ok.Cuts = []Span{{20, 30}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid cuts: %v", err)
	}
	for name, cuts := range map[string][]Span{
		"backwards":        {{30, 20}},
		"empty":            {{30, 30}},
		"before the start": {{5, 20}},
		"after the end":    {{40, 60}},
	} {
		o := ok
		o.Cuts = cuts
		if o.Validate() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	none := DefaultOptions()
	none.Cuts = []Span{{20, 30}}
	if none.Validate() == nil {
		t.Error("cuts without a selected sermon: want an error")
	}
}

// A FLAC recording is just moved into original/ (no ffmpeg needed), and so is
// a compressed one.
func TestIngestMovesWhatIsKept(t *testing.T) {
	for _, ext := range []string{".flac", ".mp3", ".m4a"} {
		p := newTestPipeline(t, "2026-10-04_raw"+ext)
		src := p.inFolder("_raw" + ext)
		raw, err := p.ingest(context.Background())
		if err != nil || raw != filepath.Join(p.P.Original, "2026-10-04_raw"+ext) {
			t.Fatalf("%s: raw=%q err=%v", ext, raw, err)
		}
		if exists(src) || !exists(raw) {
			t.Errorf("%s: the file should have been moved to original/", ext)
		}
	}
}

// A recording already in original/ is not replaced without Force; with it,
// also one of another type.
func TestIngestKeepsOriginal(t *testing.T) {
	p := newTestPipeline(t, "2026-10-04_raw.mp3")
	old := filepath.Join(p.P.Original, "2026-10-04_raw.flac")
	os.WriteFile(old, []byte("original"), 0o644)
	if raw, err := p.ingest(context.Background()); raw != old || err != nil {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	if b, _ := os.ReadFile(old); string(b) != "original" {
		t.Error("the file in original/ was replaced")
	}
	if !exists(p.inFolder("_raw.mp3")) {
		t.Error("the new file should be left in the folder")
	}
	p.Opt.Force = true
	if raw, err := p.ingest(context.Background()); raw != filepath.Join(p.P.Original, "2026-10-04_raw.mp3") || err != nil {
		t.Fatalf("forced: raw=%q err=%v", raw, err)
	}
	if exists(old) {
		t.Error("the replaced FLAC is still in original/")
	}
}

func TestIngestNothing(t *testing.T) {
	p := newTestPipeline(t)
	if raw, err := p.ingest(context.Background()); raw != "" || err != nil {
		t.Errorf("raw=%q err=%v", raw, err)
	}
}

// The recording is found in the folder (a new arrival) first, then in
// original/, of any type; what a conversion made before its source.
func TestSource(t *testing.T) {
	p := newTestPipeline(t)
	if got := p.P.Source(); got != "" {
		t.Errorf("empty folder: %q", got)
	}
	orig := filepath.Join(p.P.Original, "2026-10-04_raw.m4a")
	os.WriteFile(orig, []byte("x"), 0o644)
	if got := p.P.Source(); got != orig || p.P.RawOriginal() != orig {
		t.Errorf("in original/: %q", got)
	}
	os.WriteFile(p.inFolder("_raw.mp4"), []byte("x"), 0o644)
	if got := p.P.Source(); got != p.inFolder("_raw.mp4") {
		t.Errorf("a new arrival: %q", got)
	}
	os.WriteFile(p.inFolder("_raw.mka"), []byte("x"), 0o644)
	if got := p.P.Source(); got != p.inFolder("_raw.mka") {
		t.Errorf("the video's sound before the video: %q", got)
	}
}

// A recording keeps the name it arrived with in original/: it is moved there under it, and found there.
func TestIngestKeepsName(t *testing.T) {
	p := newTestPipeline(t)
	p.Opt.OriginalName = "R_20261018-104506"
	os.WriteFile(p.inFolder("_raw.m4a"), []byte("audio"), 0o644)
	want := filepath.Join(p.P.Original, "R_20261018-104506.m4a")
	if raw, err := p.ingest(context.Background()); raw != want || err != nil {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	if got := p.P.Source(); got != want || p.P.RawOriginal() != want {
		t.Errorf("found %q", got)
	}
	// Of two in original/ (a conversion's FLAC and a leftover), the FLAC; hidden files are not recordings.
	os.WriteFile(filepath.Join(p.P.Original, "R_20261018-104506.flac"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(p.P.Original, ".hidden.flac"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(p.P.Original, "notes.txt"), []byte("x"), 0o644)
	if got := p.P.RawOriginal(); got != filepath.Join(p.P.Original, "R_20261018-104506.flac") {
		t.Errorf("preferred %q", got)
	}
}

func TestKinds(t *testing.T) {
	for name, want := range map[string]string{"a.WAV": "a.flac", "a.aif": "a.flac", "a.MOV": "a.mka", "a.flac": "", "a.mp3": "", "a.mka": ""} {
		if got := ConvertsTo(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if _, ok := KindOf("a.txt"); ok {
		t.Error("a.txt is taken")
	}
}

// A run needs a selected sermon, and a recording to cut it from.
func TestRunNeedsSelectionAndRecording(t *testing.T) {
	p := newTestPipeline(t, "2026-10-04_raw.flac")
	if err := p.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "select where the sermon") {
		t.Errorf("no selection: %v", err)
	}
	p = newTestPipeline(t)
	p.Opt.End = 60
	if err := p.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "no raw recording") {
		t.Errorf("no recording: %v", err)
	}
}
