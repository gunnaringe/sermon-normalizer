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
	p, err := NewPaths(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Date != "2026-10-04" || p.Archive != "" ||
		filepath.Base(p.Master) != "2026-10-04.flac" ||
		filepath.Base(p.Mp3) != "2026-10-04.mp3" ||
		filepath.Base(p.Opus) != "2026-10-04.opus" {
		t.Errorf("unexpected paths: %+v", p)
	}
	if _, err := NewPaths(filepath.Join(dir, "missing"), ""); err == nil {
		t.Error("expected an error for a missing folder")
	}
}

func TestNewPathsArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	rel, _ := NewPaths(dir, "arkiv")
	if rel.Archive != filepath.Join(dir, "arkiv") ||
		rel.RawArchive() != filepath.Join(dir, "arkiv", "2026-10-04_raw.flac") ||
		rel.EditArchive() != filepath.Join(dir, "arkiv", "2026-10-04_edit.flac") {
		t.Errorf("relative archive: %+v", rel)
	}
	absDir := filepath.Join(t.TempDir(), "archive")
	abs, _ := NewPaths(dir, absDir)
	if abs.RawArchive() != filepath.Join(absDir, "2026-10-04_raw.flac") {
		t.Errorf("absolute archive: %+v", abs)
	}
}

// newTestPipeline makes a date folder with the given files and a pipeline for it.
func newTestPipeline(t *testing.T, archive string, files ...string) *Pipeline {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	paths, err := NewPaths(dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	if archive != "" {
		os.MkdirAll(paths.Archive, 0o755)
	}
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

// A FLAC source is just moved to the archive (no ffmpeg needed).
func TestIngestFlacMoves(t *testing.T) {
	p := newTestPipeline(t, "arkiv", "2026-10-04_edit.flac")
	src := p.inFolder("_edit.flac")
	ok, err := p.ingest(context.Background(), p.P.EditArchive(), editName)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if exists(src) || !exists(p.P.EditArchive()) {
		t.Error("the file should have been moved to the archive")
	}
}

// A FLAC already in the archive is not replaced without --force.
func TestIngestFlacKeepsArchived(t *testing.T) {
	p := newTestPipeline(t, "arkiv", "2026-10-04_raw.flac")
	os.WriteFile(p.P.RawArchive(), []byte("archived"), 0o644)
	if ok, err := p.ingest(context.Background(), p.P.RawArchive(), rawName); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if b, _ := os.ReadFile(p.P.RawArchive()); string(b) != "archived" {
		t.Error("the archived file was replaced")
	}
	if !exists(p.inFolder("_raw.flac")) {
		t.Error("the source should be left in the folder")
	}
}

func TestIngestNothing(t *testing.T) {
	p := newTestPipeline(t, "arkiv")
	if ok, err := p.ingest(context.Background(), p.P.RawArchive(), rawName); ok || err != nil {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}

// Without an archive the sources are used where they are (no ffmpeg needed).
func TestNoArchiveLeavesSources(t *testing.T) {
	p := newTestPipeline(t, "", "2026-10-04_raw.wav", "2026-10-04_edit.flac")
	ctx := context.Background()
	if err := p.stepRaw(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.stepEdit(ctx); err != nil {
		t.Fatal(err)
	}
	if p.raw != p.inFolder("_raw.wav") || p.edit != p.inFolder("_edit.flac") {
		t.Errorf("raw=%q edit=%q", p.raw, p.edit)
	}
	if exists(filepath.Join(p.P.Folder, "arkiv")) {
		t.Error("no archive folder should be created")
	}
}

// Without an archive, --start/--end cuts straight into the master: no edit file.
func TestNoArchiveCutWritesNoEdit(t *testing.T) {
	p := newTestPipeline(t, "", "2026-10-04_raw.flac")
	p.Opt.Start, p.Opt.End = 60, 120
	ctx := context.Background()
	if err := p.stepRaw(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.stepEdit(ctx); err != nil {
		t.Fatal(err)
	}
	if p.edit != "" {
		t.Errorf("edit=%q, want none", p.edit)
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

func TestParseTime(t *testing.T) {
	ok := map[string]float64{
		"2480": 2480, "41:20": 2480, "1:12:05": 4325, "41:20.5": 2480.5, "0:00:07": 7, "90": 90,
	}
	for in, want := range ok {
		if got, err := ParseTime(in); err != nil || got != want {
			t.Errorf("ParseTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1:2:3:4", "41:60", "-5", "1.5:00", "1:-1"} {
		if _, err := ParseTime(in); err == nil {
			t.Errorf("ParseTime(%q): expected an error", in)
		}
	}
}

func TestFormatTime(t *testing.T) {
	for in, want := range map[float64]string{2480: "0:41:20", 4325: "1:12:05", 2480.5: "0:41:20.500"} {
		if got := FormatTime(in); got != want {
			t.Errorf("FormatTime(%v) = %q, want %q", in, got, want)
		}
	}
}

// With --start/--end and a hand-made _edit file in the folder it is unclear which applies.
func TestCutEditConflictsWithManualEdit(t *testing.T) {
	for _, archive := range []string{"", "arkiv"} {
		p := newTestPipeline(t, archive, "2026-10-04_raw.flac", "2026-10-04_edit.flac")
		p.raw = p.inFolder("_raw.flac")
		p.Opt.Start = 60
		if err := p.stepEdit(context.Background()); err == nil || !strings.Contains(err.Error(), "only one of them") {
			t.Errorf("archive=%q: expected a conflict error, got %v", archive, err)
		}
	}
}

// If the edit already exists in the archive, it is not cut again without --force (no ffmpeg needed).
func TestCutEditSkipsExisting(t *testing.T) {
	p := newTestPipeline(t, "arkiv")
	os.WriteFile(p.P.RawArchive(), []byte("x"), 0o644)
	os.WriteFile(p.P.EditArchive(), []byte("x"), 0o644)
	p.raw = p.P.RawArchive()
	p.Opt.End = 60
	if err := p.stepEdit(context.Background()); err != nil {
		t.Error(err)
	}
	if p.edit != p.P.EditArchive() {
		t.Errorf("edit=%q", p.edit)
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "sub", "b")
	os.WriteFile(src, []byte("data"), 0o644)
	if err := moveFile(src, dst); err == nil {
		t.Fatal("expected an error when the target folder is missing")
	}
	if !exists(src) {
		t.Fatal("source must survive a failed move")
	}
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	if err := moveFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" || exists(src) {
		t.Error("move failed")
	}
}

func TestParseOutputs(t *testing.T) {
	ok := map[string]Outputs{
		"flac,mp3,opus": AllOutputs,
		"mp3":           {Mp3: true},
		"Opus, FLAC":    {Flac: true, Opus: true},
	}
	for in, want := range ok {
		if got, err := ParseOutputs(in); err != nil || got != want {
			t.Errorf("ParseOutputs(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", ",", "wav", "mp3,ogg"} {
		if _, err := ParseOutputs(in); err == nil {
			t.Errorf("ParseOutputs(%q): expected an error", in)
		}
	}
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
	got, err := readLevels(bytes.NewReader(pcm), 4)
	if err != nil {
		t.Fatal(err)
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

func TestSources(t *testing.T) {
	p := newTestPipeline(t, "arkiv", "2026-10-04_edit.wav")
	os.WriteFile(p.P.RawArchive(), []byte("x"), 0o644)
	s := p.P.Sources()
	if s.Raw != p.P.RawArchive() || s.Edit != p.inFolder("_edit.wav") || !s.EditInFolder {
		t.Errorf("got %+v", s)
	}
	// Without an archive, only the date folder counts.
	noArchive, _ := NewPaths(p.P.Folder, "")
	if s := noArchive.Sources(); s.Raw != "" || s.Edit != p.inFolder("_edit.wav") {
		t.Errorf("no archive: %+v", s)
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
	if o != DefaultOptions() {
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
