package normalizer

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
	f := p.masterFilter()
	for _, want := range []string{"aformat=channel_layouts=mono", "afftdn=nr=12", "dynaudnorm", "loudnorm=I=-19:TP=-1.5", "aresample=48000"} {
		if !strings.Contains(f, want) {
			t.Errorf("%q missing from %q", want, f)
		}
	}
	p.Opt.NoiseReduction = 0
	if strings.Contains(p.masterFilter(), "afftdn") {
		t.Error("afftdn should be off")
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
