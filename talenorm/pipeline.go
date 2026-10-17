package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
)

// Options styrer prosesseringen.
type Options struct {
	Mp3Bitrate     string
	OpusBitrate    string
	TargetLUFS     float64 // mono: -19, stereo: -16
	TruePeak       float64
	NoiseReduction int // afftdn nr i dB; 0 = av
	KeepWav        bool
	Force          bool
}

func defaultOptions() Options {
	return Options{
		Mp3Bitrate:     "96k",
		OpusBitrate:    "48k",
		TargetLUFS:     -19,
		TruePeak:       -1.5,
		NoiseReduction: 12,
	}
}

// Paths er filene i en datomappe.
type Paths struct {
	Folder  string
	Date    string
	Arkiv   string
	RawFlac string
	EditDst string
	Master  string
	Mp3     string
	Opus    string
}

func NewPaths(folder string) (Paths, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return Paths{}, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return Paths{}, fmt.Errorf("%s er ikke en mappe", abs)
	}
	date := filepath.Base(abs)
	arkiv := filepath.Join(abs, "arkiv")
	return Paths{
		Folder:  abs,
		Date:    date,
		Arkiv:   arkiv,
		RawFlac: filepath.Join(arkiv, date+"_raw.flac"),
		EditDst: filepath.Join(arkiv, date+"_edit.flac"),
		Master:  filepath.Join(abs, date+".flac"),
		Mp3:     filepath.Join(abs, date+".mp3"),
		Opus:    filepath.Join(abs, date+".opus"),
	}, nil
}

// Pipeline kjører stegene for én datomappe.
type Pipeline struct {
	P   Paths
	Opt Options
	FF  FFmpeg
	Out io.Writer
}

func (p *Pipeline) logf(format string, a ...any) { fmt.Fprintf(p.Out, format+"\n", a...) }
func (p *Pipeline) step(title string)            { p.logf("\n== %s", title) }
func (p *Pipeline) warnf(format string, a ...any) {
	p.logf("ADVARSEL: "+format, a...)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func firstExisting(paths ...string) string {
	for _, s := range paths {
		if exists(s) {
			return s
		}
	}
	return ""
}

func (p *Pipeline) inFolder(names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(p.P.Folder, strings.ReplaceAll(n, "{d}", p.P.Date))
	}
	return out
}

// Run kjører hele flyten.
func (p *Pipeline) Run(ctx context.Context) error {
	if err := os.MkdirAll(p.P.Arkiv, 0o755); err != nil {
		return err
	}
	if err := p.stepRaw(ctx); err != nil {
		return err
	}
	if err := p.stepEdit(ctx); err != nil {
		return err
	}
	if err := p.stepMaster(ctx); err != nil {
		return err
	}
	if err := p.stepLossy(ctx); err != nil {
		return err
	}
	return p.report(ctx)
}

// --- 1. raw og 2. edit: begge kan komme som WAV eller FLAC ---------------

func (p *Pipeline) stepRaw(ctx context.Context) error {
	p.step("1. Raatt opptak")
	ok, err := p.ingest(ctx, p.P.RawFlac, "{d}_raw", "{d}_1-raw")
	if err != nil {
		return err
	}
	if !ok {
		p.warnf("fant ingen raw-fil.")
	}
	return nil
}

// ingest henter en kilde (WAV eller FLAC) i datomappen inn i arkiv/ som FLAC.
//   - FLAC i mappen flyttes til dst.
//   - WAV konverteres til dst (hvis dst mangler eller --force), lyden verifiseres
//     med MD5, og WAV slettes bare hvis lyden er identisk (og ikke --keep-wav).
//
// Returnerer om dst finnes etterpaa.
func (p *Pipeline) ingest(ctx context.Context, dst string, bases ...string) (bool, error) {
	var wavs, flacs []string
	for _, b := range bases {
		wavs = append(wavs, p.inFolder(b+".wav")...)
		flacs = append(flacs, p.inFolder(b+".flac")...)
	}
	wav, flac := firstExisting(wavs...), firstExisting(flacs...)

	if flac != "" {
		if err := os.Rename(flac, dst); err != nil {
			return false, err
		}
		p.logf("Flyttet %s til arkiv/", filepath.Base(flac))
	}
	if wav == "" {
		if exists(dst) && flac == "" {
			p.logf("Ingen WAV; bruker %s.", filepath.Base(dst))
		}
		return exists(dst), nil
	}

	if !exists(dst) || p.Opt.Force {
		if _, err := p.FF.Run(ctx, "-y", "-i", wav, "-c:a", "flac", dst); err != nil {
			return false, err
		}
	}
	same, err := p.sameAudio(ctx, wav, dst)
	if err != nil {
		return false, err
	}
	switch {
	case !same:
		p.warnf("%s og %s er IKKE identiske. WAV beholdes.", filepath.Base(dst), filepath.Base(wav))
	case p.Opt.KeepWav:
		p.logf("OK: %s er identisk med WAV. Beholder WAV (--keep-wav).", filepath.Base(dst))
	default:
		if err := os.Remove(wav); err != nil {
			return false, err
		}
		p.logf("OK: %s er identisk med WAV. Slettet %s.", filepath.Base(dst), filepath.Base(wav))
	}
	return true, nil
}

var reMD5 = regexp.MustCompile(`MD5=(\w+)`)

func (p *Pipeline) audioMD5(ctx context.Context, path string) (string, error) {
	res, err := p.FF.Run(ctx, "-v", "error", "-i", path, "-map", "0:a", "-f", "md5", "-")
	if err != nil {
		return "", err
	}
	m := reMD5.FindStringSubmatch(res.Stdout + res.Stderr)
	if m == nil {
		return "", errors.New("fant ikke MD5 i ffmpeg-utdata")
	}
	return m[1], nil
}

func (p *Pipeline) sameAudio(ctx context.Context, a, b string) (bool, error) {
	ha, err := p.audioMD5(ctx, a)
	if err != nil {
		return false, err
	}
	hb, err := p.audioMD5(ctx, b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

func (p *Pipeline) stepEdit(ctx context.Context) error {
	p.step("2. Klippet versjon")
	ok, err := p.ingest(ctx, p.P.EditDst, "{d}_edit", "{d}_2-edit")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("mangler %s_edit.flac (eller .wav): klipp opptaket og lagre det i %s", p.P.Date, p.P.Folder)
	}
	return nil
}

// --- 3. master ------------------------------------------------------------

func (p *Pipeline) masterFilter() string {
	f := []string{"pan=mono|c0=0.5*c0+0.5*c1"}
	if p.Opt.NoiseReduction > 0 {
		f = append(f, fmt.Sprintf("afftdn=nr=%d", p.Opt.NoiseReduction))
	}
	f = append(f,
		"dynaudnorm=f=250:g=15:p=0.9",
		fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=11", p.Opt.TargetLUFS, p.Opt.TruePeak),
		"aresample=48000",
	)
	return strings.Join(f, ",")
}

func (p *Pipeline) stepMaster(ctx context.Context) error {
	p.step(fmt.Sprintf("3. Master (mono, %g LUFS)", p.Opt.TargetLUFS))
	if exists(p.P.Master) && !p.Opt.Force {
		p.logf("Finnes allerede, hopper over (bruk --force).")
		return nil
	}
	_, err := p.FF.Run(ctx, "-y", "-i", p.P.EditDst, "-af", p.masterFilter(),
		"-c:a", "flac", "-sample_fmt", "s32", p.P.Master)
	return err
}

// --- 4. mp3 + opus --------------------------------------------------------

const lossyPeakCeiling = -1.0 // dBTP

// correction regner ut gain som trengs etter koding til et tapsbelagt format.
// Gain begrenses slik at true peak havner på maks lossyPeakCeiling.
func correction(target float64, m Loudness) (gain float64, needed bool) {
	gain = math.Min(target-m.LUFS, lossyPeakCeiling-m.TruePeak)
	needed = math.Abs(gain) >= 0.3 || m.TruePeak > lossyPeakCeiling
	return gain, needed
}

func (p *Pipeline) encodeLossy(ctx context.Context, name, dst string, codec []string) error {
	if exists(dst) && !p.Opt.Force {
		p.logf("%s finnes, hopper over.", name)
		return nil
	}
	enc := func(gain *float64) error {
		args := []string{"-y", "-i", p.P.Master}
		if gain != nil {
			args = append(args, "-af", fmt.Sprintf("volume=%.2fdB", *gain))
		}
		args = append(args, codec...)
		args = append(args, dst)
		_, err := p.FF.Run(ctx, args...)
		return err
	}
	if err := enc(nil); err != nil {
		return err
	}
	m, err := p.FF.Measure(ctx, dst)
	if err != nil {
		return err
	}
	if gain, needed := correction(p.Opt.TargetLUFS, m); needed {
		p.logf("%s: %.1f LUFS / %.1f dBTP -> justerer %.1f dB og koder paa nytt", name, m.LUFS, m.TruePeak, gain)
		return enc(&gain)
	}
	return nil
}

func (p *Pipeline) stepLossy(ctx context.Context) error {
	p.step(fmt.Sprintf("4. MP3 (%s) og Opus (%s), mono", p.Opt.Mp3Bitrate, p.Opt.OpusBitrate))
	if err := p.encodeLossy(ctx, "mp3", p.P.Mp3,
		[]string{"-ar", "44100", "-ac", "1", "-c:a", "libmp3lame", "-b:a", p.Opt.Mp3Bitrate}); err != nil {
		return err
	}
	return p.encodeLossy(ctx, "opus", p.P.Opus,
		[]string{"-ar", "48000", "-ac", "1", "-c:a", "libopus", "-b:a", p.Opt.OpusBitrate, "-application", "voip"})
}

// --- rapport --------------------------------------------------------------

func (p *Pipeline) report(ctx context.Context) error {
	p.step(fmt.Sprintf("Resultat (maal: %g LUFS, maks %g dBTP)", p.Opt.TargetLUFS, p.Opt.TruePeak))
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Fil\tLUFS\tTP\tLRA\tMB")
	for _, f := range []string{p.P.EditDst, p.P.Master, p.P.Mp3, p.P.Opus} {
		m, err := p.FF.Measure(ctx, f)
		if err != nil {
			return err
		}
		st, _ := os.Stat(f)
		fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t%.1f\n", filepath.Base(f), m.LUFS, m.TruePeak, m.LRA, float64(st.Size())/1048576)
		if f == p.P.EditDst {
			continue
		}
		if m.TruePeak > -1.0 {
			defer p.warnf("%s: true peak %.1f dBTP er over -1.", filepath.Base(f), m.TruePeak)
		}
		if math.Abs(m.LUFS-p.Opt.TargetLUFS) > 1.5 {
			defer p.warnf("%s: %.1f LUFS er mer enn 1,5 fra maalet.", filepath.Base(f), m.LUFS)
		}
	}
	return tw.Flush()
}
