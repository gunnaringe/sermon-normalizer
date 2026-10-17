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
		t.Errorf("fikk %+v, vil ha %+v", got, want)
	}
}

func TestParseEbur128Missing(t *testing.T) {
	if _, err := parseEbur128("ingenting her"); err == nil {
		t.Error("forventet feil")
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
		{"peak for hoy", Loudness{LUFS: -18.6, TruePeak: 0.3}, -1.3, true},
		{"for lav, plass til mer", Loudness{LUFS: -19.9, TruePeak: -2.5}, 0.9, true},
		{"for lav, peak begrenser", Loudness{LUFS: -19.9, TruePeak: -1.3}, 0.3, true},
	}
	for _, c := range cases {
		gain, needed := correction(-19, c.m)
		if diff := gain - c.wantGain; diff > 0.001 || diff < -0.001 || needed != c.needed {
			t.Errorf("%s: gain=%.2f needed=%v, vil ha %.2f %v", c.name, gain, needed, c.wantGain, c.needed)
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
	if p.Date != "2026-10-04" ||
		filepath.Base(p.Master) != "2026-10-04.flac" ||
		filepath.Base(p.Mp3) != "2026-10-04.mp3" ||
		filepath.Base(p.Opus) != "2026-10-04.opus" ||
		filepath.Base(p.RawFlac) != "2026-10-04_raw.flac" ||
		filepath.Base(p.EditDst) != "2026-10-04_edit.flac" ||
		filepath.Base(filepath.Dir(p.RawFlac)) != "arkiv" {
		t.Errorf("uventede stier: %+v", p)
	}
	if _, err := NewPaths(filepath.Join(dir, "finnes-ikke")); err == nil {
		t.Error("forventet feil for manglende mappe")
	}
}

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "b")
	os.WriteFile(b, nil, 0o644)
	if got := firstExisting(filepath.Join(dir, "a"), b); got != b {
		t.Errorf("fikk %q", got)
	}
	if got := firstExisting(filepath.Join(dir, "a")); got != "" {
		t.Errorf("fikk %q", got)
	}
}

// FLAC som kilde skal bare flyttes til arkiv/ (ingen ffmpeg trengs).
func TestIngestFlacMoves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	paths, err := NewPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(paths.Arkiv, 0o755)
	src := filepath.Join(dir, "2026-10-04_2-edit.flac") // gammelt navn godtas
	os.WriteFile(src, []byte("x"), 0o644)

	p := &Pipeline{P: paths, Opt: DefaultOptions(), Out: io.Discard}
	ok, err := p.ingest(context.Background(), paths.EditDst, "{d}_edit", "{d}_2-edit")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if exists(src) || !exists(paths.EditDst) {
		t.Error("filen skulle vaere flyttet til arkiv/")
	}
}

func TestIngestNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	paths, _ := NewPaths(dir)
	p := &Pipeline{P: paths, Opt: DefaultOptions(), Out: io.Discard}
	if ok, err := p.ingest(context.Background(), paths.RawFlac, "{d}_raw"); ok || err != nil {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}

func TestMasterFilter(t *testing.T) {
	p := &Pipeline{Opt: DefaultOptions()}
	f := p.masterFilter()
	for _, want := range []string{"pan=mono", "afftdn=nr=12", "dynaudnorm", "loudnorm=I=-19:TP=-1.5", "aresample=48000"} {
		if !strings.Contains(f, want) {
			t.Errorf("%q mangler i %q", want, f)
		}
	}
	p.Opt.NoiseReduction = 0
	if strings.Contains(p.masterFilter(), "afftdn") {
		t.Error("afftdn skal vaere av")
	}
}

func TestParseTime(t *testing.T) {
	ok := map[string]float64{
		"2480": 2480, "41:20": 2480, "1:12:05": 4325, "41:20.5": 2480.5, "0:00:07": 7, "90": 90,
	}
	for in, want := range ok {
		if got, err := ParseTime(in); err != nil || got != want {
			t.Errorf("ParseTime(%q) = %v, %v; vil ha %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1:2:3:4", "41:60", "-5", "1.5:00", "1:-1"} {
		if _, err := ParseTime(in); err == nil {
			t.Errorf("ParseTime(%q): forventet feil", in)
		}
	}
}

func TestFormatTime(t *testing.T) {
	for in, want := range map[float64]string{2480: "0:41:20", 4325: "1:12:05", 2480.5: "0:41:20.500"} {
		if got := FormatTime(in); got != want {
			t.Errorf("FormatTime(%v) = %q, vil ha %q", in, got, want)
		}
	}
}

// Med --start/--end og en egen _edit-fil i mappen er det uklart hva som gjelder.
func TestCutEditConflictsWithManualEdit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	paths, _ := NewPaths(dir)
	os.MkdirAll(paths.Arkiv, 0o755)
	os.WriteFile(paths.RawFlac, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "2026-10-04_edit.flac"), []byte("x"), 0o644)

	opt := DefaultOptions()
	opt.Start = 60
	p := &Pipeline{P: paths, Opt: opt, Out: io.Discard}
	if err := p.stepEdit(context.Background()); err == nil || !strings.Contains(err.Error(), "bare det ene") {
		t.Errorf("forventet konflikt-feil, fikk %v", err)
	}
}

// Finnes edit allerede i arkiv/, klippes det ikke paa nytt uten --force (ingen ffmpeg trengs).
func TestCutEditSkipsExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-10-04")
	os.Mkdir(dir, 0o755)
	paths, _ := NewPaths(dir)
	os.MkdirAll(paths.Arkiv, 0o755)
	os.WriteFile(paths.RawFlac, []byte("x"), 0o644)
	os.WriteFile(paths.EditDst, []byte("x"), 0o644)

	opt := DefaultOptions()
	opt.End = 60
	p := &Pipeline{P: paths, Opt: opt, Out: io.Discard}
	if err := p.stepEdit(context.Background()); err != nil {
		t.Error(err)
	}
}
