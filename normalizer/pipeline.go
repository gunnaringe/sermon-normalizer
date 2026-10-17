package normalizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Options controls the processing.
type Options struct {
	Mp3Bitrate     string
	OpusBitrate    string
	TargetLUFS     float64 // mono: -19, stereo: -16
	TruePeak       float64
	NoiseReduction int // afftdn nr in dB; 0 = off
	KeepWav        bool
	Force          bool
	Start, End     float64 // cut raw to edit (seconds); 0 = from the beginning / to the end
	FadeIn         float64 // seconds at the start of the sermon; 0 = off
	FadeOut        float64 // seconds at the end of the sermon; 0 = off
}

// cutting reports whether the edit is cut from raw rather than supplied by hand.
func (o Options) cutting() bool { return o.Start > 0 || o.End > 0 }

func DefaultOptions() Options {
	return Options{
		Mp3Bitrate:     "96k",
		OpusBitrate:    "48k",
		TargetLUFS:     -19,
		TruePeak:       -1.5,
		NoiseReduction: 12,
	}
}

// Paths are the files for one date folder.
type Paths struct {
	Folder  string
	Date    string
	Archive string // "" = no archive: sources stay where they are
	Master  string
	Mp3     string
	Opus    string
}

// NewPaths resolves the files for a date folder. A relative archive is taken
// relative to the folder; "" means no archive.
func NewPaths(folder, archive string) (Paths, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return Paths{}, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return Paths{}, fmt.Errorf("%s is not a directory", abs)
	}
	if archive != "" && !filepath.IsAbs(archive) {
		archive = filepath.Join(abs, archive)
	}
	date := filepath.Base(abs)
	return Paths{
		Folder:  abs,
		Date:    date,
		Archive: archive,
		Master:  filepath.Join(abs, date+".flac"),
		Mp3:     filepath.Join(abs, date+".mp3"),
		Opus:    filepath.Join(abs, date+".opus"),
	}, nil
}

// RawArchive and EditArchive are where the sources are kept when archiving.
func (p Paths) RawArchive() string  { return filepath.Join(p.Archive, p.Date+"_raw.flac") }
func (p Paths) EditArchive() string { return filepath.Join(p.Archive, p.Date+"_edit.flac") }

// Source names in the date folder: <date>_raw and <date>_edit, as .flac or .wav.
const (
	rawName  = "_raw"
	editName = "_edit"
)

// Pipeline runs the steps for one date folder.
type Pipeline struct {
	P   Paths
	Opt Options
	FF  FFmpeg
	Out io.Writer

	raw  string // the whole recording, if any
	edit string // the sermon as a file; "" when the master is cut straight from raw
}

func (p *Pipeline) logf(format string, a ...any) { fmt.Fprintf(p.Out, format+"\n", a...) }
func (p *Pipeline) step(title string)            { p.logf("\n== %s", title) }
func (p *Pipeline) warnf(format string, a ...any) {
	p.logf("WARNING: "+format, a...)
}

// inFolder is <date><suffix> in the date folder.
func (p *Pipeline) inFolder(suffix string) string {
	return filepath.Join(p.P.Folder, p.P.Date+suffix)
}

// findSource returns the source with the given name in the folder,
// preferring FLAC over WAV, or "".
func (p *Pipeline) findSource(name string) string {
	return firstExisting(p.inFolder(name+".flac"), p.inFolder(name+".wav"))
}

// Run runs the whole flow.
func (p *Pipeline) Run(ctx context.Context) error {
	if p.P.Archive != "" {
		if err := os.MkdirAll(p.P.Archive, 0o755); err != nil {
			return err
		}
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

// --- 1. raw and 2. edit: both may arrive as WAV or FLAC -------------------

func (p *Pipeline) stepRaw(ctx context.Context) error {
	p.step("1. Raw recording")
	if p.P.Archive == "" {
		p.raw = p.findSource(rawName)
		if p.raw != "" {
			p.logf("Using %s (no archive, left in place).", filepath.Base(p.raw))
		} else {
			p.warnf("no raw file found.")
		}
		return nil
	}
	ok, err := p.ingest(ctx, p.P.RawArchive(), rawName)
	if err != nil {
		return err
	}
	if ok {
		p.raw = p.P.RawArchive()
	} else {
		p.warnf("no raw file found.")
	}
	return nil
}

// ingest brings a source (WAV or FLAC) in the date folder into the archive as FLAC.
//   - A FLAC in the folder is moved to dst.
//   - A WAV is converted to dst (if dst is missing or --force), the audio is
//     verified with MD5, and the WAV is deleted only if the audio is identical
//     (and not --keep-wav).
//
// Returns whether dst exists afterwards.
func (p *Pipeline) ingest(ctx context.Context, dst, name string) (bool, error) {
	wav, flac := firstExisting(p.inFolder(name+".wav")), firstExisting(p.inFolder(name+".flac"))

	switch {
	case flac == "":
	case exists(dst) && !p.Opt.Force:
		p.warnf("%s is already in the archive; leaving %s in the folder (use --force to replace).", filepath.Base(dst), filepath.Base(flac))
	default:
		if err := moveFile(flac, dst); err != nil {
			return false, err
		}
		p.logf("Moved %s to %s.", filepath.Base(flac), dst)
	}
	if wav == "" {
		if exists(dst) && flac == "" {
			p.logf("No WAV; using %s.", filepath.Base(dst))
		}
		return exists(dst), nil
	}

	if !exists(dst) || p.Opt.Force {
		if err := p.FF.RunTo(ctx, dst, "-i", wav, "-c:a", "flac"); err != nil {
			return false, err
		}
	}
	same, err := p.sameAudio(ctx, wav, dst)
	if err != nil {
		return false, err
	}
	switch {
	case !same:
		p.warnf("%s and %s are NOT identical. Keeping the WAV.", filepath.Base(dst), filepath.Base(wav))
	case p.Opt.KeepWav:
		p.logf("OK: %s is identical to the WAV. Keeping the WAV (--keep-wav).", filepath.Base(dst))
	default:
		if err := os.Remove(wav); err != nil {
			return false, err
		}
		p.logf("OK: %s is identical to the WAV. Deleted %s.", filepath.Base(dst), filepath.Base(wav))
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
		return "", errors.New("no MD5 found in ffmpeg output")
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
	p.step("2. Edit")
	manual := p.findSource(editName)
	if p.Opt.cutting() {
		if manual != "" {
			return fmt.Errorf("%s exists and --start/--end is given: use only one of them", filepath.Base(manual))
		}
		if p.raw == "" {
			return errors.New("no raw file to cut with --start/--end")
		}
		if p.P.Archive == "" {
			p.logf("Cutting %s from %s to %s while making the master.", filepath.Base(p.raw), p.cutFrom(), p.cutTo())
			return nil
		}
		return p.cutEdit(ctx)
	}
	missing := fmt.Errorf("missing %s_edit.flac (or .wav): use --start/--end, or cut the recording yourself and save it in %s", p.P.Date, p.P.Folder)
	if p.P.Archive == "" {
		if manual == "" {
			return missing
		}
		p.edit = manual
		p.logf("Using %s (no archive, left in place).", filepath.Base(manual))
		return nil
	}
	ok, err := p.ingest(ctx, p.P.EditArchive(), editName)
	if err != nil {
		return err
	}
	if !ok {
		return missing
	}
	p.edit = p.P.EditArchive()
	return nil
}

// cutEdit writes the edit to the archive from raw with --start/--end (one
// continuous segment). If more than one segment is needed, cut by hand and
// supply the _edit file as before.
func (p *Pipeline) cutEdit(ctx context.Context) error {
	dst := p.P.EditArchive()
	p.edit = dst
	if exists(dst) && !p.Opt.Force {
		p.logf("%s exists, skipping (use --force to cut again).", filepath.Base(dst))
		return nil
	}
	args := append(p.cutArgs(), "-i", p.raw, "-c:a", "flac")
	if err := p.FF.RunTo(ctx, dst, args...); err != nil {
		return err
	}
	p.logf("Cut %s from %s to %s.", filepath.Base(dst), p.cutFrom(), p.cutTo())
	return nil
}

// cutArgs are the input options for --start/--end. Placed before -i, ffmpeg
// decodes and discards the audio before start, so the cut is exact.
func (p *Pipeline) cutArgs() []string {
	var args []string
	if p.Opt.Start > 0 {
		args = append(args, "-ss", secs(p.Opt.Start))
	}
	if p.Opt.End > 0 {
		args = append(args, "-to", secs(p.Opt.End))
	}
	return args
}

func (p *Pipeline) cutFrom() string {
	if p.Opt.Start > 0 {
		return FormatTime(p.Opt.Start)
	}
	return "the beginning"
}

func (p *Pipeline) cutTo() string {
	if p.Opt.End > 0 {
		return FormatTime(p.Opt.End)
	}
	return "the end"
}

func secs(s float64) string { return strconv.FormatFloat(s, 'f', 3, 64) }

// --- 3. master ------------------------------------------------------------

// masterFilter is the filter chain for the master; length (seconds) is only
// needed for a fade-out.
func (p *Pipeline) masterFilter(length float64) string {
	// Downmix first, whatever the input has (mono, stereo, more), so the
	// rest of the chain works on the mono signal that gets published.
	f := []string{"aformat=channel_layouts=mono"}
	if p.Opt.NoiseReduction > 0 {
		f = append(f, fmt.Sprintf("afftdn=nr=%d", p.Opt.NoiseReduction))
	}
	f = append(f,
		"dynaudnorm=f=250:g=15:p=0.9",
		fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=11", p.Opt.TargetLUFS, p.Opt.TruePeak),
		"aresample=48000",
	)
	// Fades come last: dynaudnorm and loudnorm would otherwise lift them again.
	if p.Opt.FadeIn > 0 {
		f = append(f, fmt.Sprintf("afade=t=in:d=%g", p.Opt.FadeIn))
	}
	if p.Opt.FadeOut > 0 {
		f = append(f, fmt.Sprintf("afade=t=out:st=%.3f:d=%g", length-p.Opt.FadeOut, p.Opt.FadeOut))
	}
	return strings.Join(f, ",")
}

func (p *Pipeline) stepMaster(ctx context.Context) error {
	p.step(fmt.Sprintf("3. Master (mono, %g LUFS)", p.Opt.TargetLUFS))
	if exists(p.P.Master) && !p.Opt.Force {
		p.logf("Already exists, skipping (use --force).")
		return nil
	}
	var length float64
	if p.Opt.FadeIn > 0 || p.Opt.FadeOut > 0 {
		var err error
		if length, err = p.sermonLength(ctx); err != nil {
			return err
		}
		if p.Opt.FadeIn+p.Opt.FadeOut > length {
			return fmt.Errorf("fades (%g s + %g s) are longer than the sermon (%.1f s)", p.Opt.FadeIn, p.Opt.FadeOut, length)
		}
		p.logf("Fade in %g s, fade out %g s (sermon is %s).", p.Opt.FadeIn, p.Opt.FadeOut, FormatTime(math.Round(length)))
	}
	var args []string
	if p.edit != "" {
		args = []string{"-i", p.edit}
	} else {
		args = append(p.cutArgs(), "-i", p.raw)
	}
	args = append(args, "-af", p.masterFilter(length), "-c:a", "flac", "-sample_fmt", "s32")
	return p.FF.RunTo(ctx, p.P.Master, args...)
}

// sermonLength is the length in seconds of what goes into the master.
func (p *Pipeline) sermonLength(ctx context.Context) (float64, error) {
	if p.edit != "" {
		return p.FF.Duration(ctx, p.edit)
	}
	end, err := p.FF.Duration(ctx, p.raw)
	if err != nil {
		return 0, err
	}
	if p.Opt.End > 0 && p.Opt.End < end {
		end = p.Opt.End // --end past the end of the recording just runs to the end
	}
	return end - p.Opt.Start, nil
}

// --- 4. mp3 + opus --------------------------------------------------------

const lossyPeakCeiling = -1.0 // dBTP

// correction computes the gain needed after encoding to a lossy format.
// The gain is limited so that the true peak ends up at most lossyPeakCeiling.
func correction(target float64, m Loudness) (gain float64, needed bool) {
	gain = math.Min(target-m.LUFS, lossyPeakCeiling-m.TruePeak)
	needed = math.Abs(gain) >= 0.3 || m.TruePeak > lossyPeakCeiling
	return gain, needed
}

func (p *Pipeline) encodeLossy(ctx context.Context, name, dst string, codec []string) error {
	if exists(dst) && !p.Opt.Force {
		p.logf("%s exists, skipping.", name)
		return nil
	}
	enc := func(gain *float64) error {
		args := []string{"-i", p.P.Master}
		if gain != nil {
			args = append(args, "-af", fmt.Sprintf("volume=%.2fdB", *gain))
		}
		return p.FF.RunTo(ctx, dst, append(args, codec...)...)
	}
	if err := enc(nil); err != nil {
		return err
	}
	m, err := p.FF.Measure(ctx, dst)
	if err != nil {
		return err
	}
	if gain, needed := correction(p.Opt.TargetLUFS, m); needed {
		p.logf("%s: %.1f LUFS / %.1f dBTP -> adjusting %.1f dB and re-encoding", name, m.LUFS, m.TruePeak, gain)
		return enc(&gain)
	}
	return nil
}

func (p *Pipeline) stepLossy(ctx context.Context) error {
	p.step(fmt.Sprintf("4. MP3 (%s) and Opus (%s), mono", p.Opt.Mp3Bitrate, p.Opt.OpusBitrate))
	if err := p.encodeLossy(ctx, "mp3", p.P.Mp3,
		[]string{"-ar", "44100", "-ac", "1", "-c:a", "libmp3lame", "-b:a", p.Opt.Mp3Bitrate}); err != nil {
		return err
	}
	return p.encodeLossy(ctx, "opus", p.P.Opus,
		[]string{"-ar", "48000", "-ac", "1", "-c:a", "libopus", "-b:a", p.Opt.OpusBitrate, "-application", "voip"})
}

// --- report ---------------------------------------------------------------

func (p *Pipeline) report(ctx context.Context) error {
	p.step(fmt.Sprintf("Result (target: %g LUFS, max %g dBTP)", p.Opt.TargetLUFS, p.Opt.TruePeak))
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "File\tLUFS\tTP\tLRA\tMB")
	files := []string{p.P.Master, p.P.Mp3, p.P.Opus}
	if p.edit != "" {
		files = append([]string{p.edit}, files...)
	}
	for _, f := range files {
		m, err := p.FF.Measure(ctx, f)
		if err != nil {
			return err
		}
		st, _ := os.Stat(f)
		fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t%.1f\n", filepath.Base(f), m.LUFS, m.TruePeak, m.LRA, float64(st.Size())/1048576)
		if f == p.edit {
			continue
		}
		if m.TruePeak > -1.0 {
			defer p.warnf("%s: true peak %.1f dBTP is above -1.", filepath.Base(f), m.TruePeak)
		}
		if math.Abs(m.LUFS-p.Opt.TargetLUFS) > 1.5 {
			defer p.warnf("%s: %.1f LUFS is more than 1.5 from the target.", filepath.Base(f), m.LUFS)
		}
	}
	return tw.Flush()
}
