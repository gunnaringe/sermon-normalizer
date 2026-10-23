package normalizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Options controls the processing.
type Options struct {
	Mp3Bitrate     string
	OpusBitrate    string
	Stereo         bool    // keep two channels; default mono
	TargetLUFS     float64 // mono: -19, stereo: -16
	TruePeak       float64
	NoiseReduction int // afftdn nr in dB; 0 = off
	// RoomSound is how much of the room's own sound (reverb, ambience) is kept when noise is taken
	// out, 0 to 100: it raises the floor the noise reduction works down to.
	RoomSound int
	// Leveling evens out quiet speech and loud music: one of LevelingNames ("" = normal).
	Leveling string
	// Compression narrows the loudness range further: one of CompressionNames ("" = off).
	Compression string
	Force       bool
	// OriginalName is the name (no extension) the recording is kept under in original/: the name it
	// arrived with. "" = <date>_raw.
	OriginalName string
	Start, End   float64 // cut raw to edit (seconds); 0 = from the beginning / to the end
	Cuts         []Span  // parts of Start..End to leave out (seconds in the recording), e.g. a song
	Pauses       []Pause // silences put into the sermon, at times in the recording
	FadeIn       float64 // seconds at the start of the sermon; 0 = off
	FadeOut      float64 // seconds at the end of the sermon; 0 = off
	// Intro and Outro are clips put before and after the sermon (paths; "" = none). They are made
	// elsewhere, often with music, so only their level, rate and channels are set; ClipPause is the
	// silence (seconds) at each join. With either, a run makes two versions of each output: with the
	// clips (the main files) and the sermon only (Paths.Sermon*).
	Intro, Outro string
	ClipPause    float64
	Outputs      Outputs
	Tags         Tags // written into every output; empty fields are left out
}

// Tags are the metadata written into the outputs (FLAC, MP3, Opus).
type Tags struct {
	Title, Artist, Album string
	Date                 string // recording date, 2006-01-02
}

// args are the ffmpeg options that set the tags, for the output that follows.
// Zero Tags leave the source's tags alone; otherwise all four are set, and an
// empty one removes the tag (so a recorder's own tags do not leak through).
func (t Tags) args() []string {
	if t == (Tags{}) {
		return nil
	}
	var a []string
	for _, kv := range [][2]string{{"title", t.Title}, {"artist", t.Artist}, {"album", t.Album}, {"date", t.Date}} {
		a = append(a, "-metadata", kv[0]+"="+kv[1])
	}
	return a
}

// clips reports whether an intro or outro goes around the sermon.
func (o Options) clips() bool { return o.Intro != "" || o.Outro != "" }

// MaxClipPause is the longest pause at a join with a clip, in seconds.
const MaxClipPause = 10.0

// cutting reports whether a sermon is selected in the recording.
func (o Options) cutting() bool { return o.Start > 0 || o.End > 0 }

// Pause is silence put into the sermon: Seconds long, where the recording is at At (seconds in the
// recording). At is where a kept part ends (before a cut or a gap), or inside a part, which it
// then splits. A pause at the end of the sermon, or outside it, has no effect.
type Pause struct {
	At, Seconds float64
}

// MaxPause is the longest pause, in seconds.
const MaxPause = 30.0

// joinFade is the fade (seconds) on each side of a join where a part was cut
// out, so the join does not click.
const joinFade = 0.015

// keep returns the parts of the recording the sermon is made of: Start..end
// without the Cuts, in order. end is where the recording ends; an End past it,
// or 0, runs to the end. Cuts may be given in any order and may overlap.
func (o Options) keep(end float64) []Span {
	if o.End > 0 && o.End < end {
		end = o.End
	}
	cuts := append([]Span(nil), o.Cuts...)
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Start < cuts[j].Start })
	var kept []Span
	at := o.Start
	for _, c := range cuts {
		if c.Start > at {
			kept = append(kept, Span{at, math.Min(c.Start, end)})
		}
		at = math.Max(at, c.End)
	}
	if at < end {
		kept = append(kept, Span{at, end})
	}
	// Leave out slivers that a cut right next to the edge or another cut leaves.
	out := kept[:0]
	for _, k := range kept {
		if k.End-k.Start >= 0.05 {
			out = append(out, k)
		}
	}
	return out
}

// parts is keep with the pauses worked in: the parts of the recording the sermon is made of, cut
// where a pause falls inside one, and the silence (seconds) that follows each (the last one's is
// not made).
func (o Options) parts(end float64) (kept []Span, after []float64) {
	kept = o.keep(end)
	after = make([]float64, len(kept))
	pauses := slices.Clone(o.Pauses)
	sort.Slice(pauses, func(i, j int) bool { return pauses[i].At < pauses[j].At })
	for _, p := range pauses {
		if p.Seconds <= 0 {
			continue
		}
		for i, k := range kept {
			if math.Abs(p.At-k.End) < 0.001 {
				after[i] += p.Seconds
				break
			}
			if p.At >= k.Start+0.05 && p.At <= k.End-0.05 {
				kept = slices.Insert(kept, i+1, Span{p.At, k.End})
				kept[i].End = p.At
				after = slices.Insert(after, i+1, after[i])
				after[i] = p.Seconds
				break
			}
		}
	}
	return kept, after
}

// pauseTotal is the silence the sermon has in it, in seconds.
func pauseTotal(after []float64) float64 {
	var n float64
	for i, s := range after {
		if i < len(after)-1 {
			n += s
		}
	}
	return n
}

// ChannelDefaults are the defaults that depend on mono or stereo: the usual
// podcast loudness targets, and bitrates that sound the same per channel.
type ChannelDefaults struct {
	TargetLUFS  float64
	Mp3Bitrate  string
	OpusBitrate string
}

func DefaultsFor(stereo bool) ChannelDefaults {
	if stereo {
		return ChannelDefaults{TargetLUFS: -16, Mp3Bitrate: "128k", OpusBitrate: "96k"}
	}
	return ChannelDefaults{TargetLUFS: -19, Mp3Bitrate: "96k", OpusBitrate: "48k"}
}

// SetStereo switches between mono and stereo, moving the loudness target and
// bitrates along unless they were changed from the old defaults.
func (o *Options) SetStereo(stereo bool) {
	old, def := DefaultsFor(o.Stereo), DefaultsFor(stereo)
	if o.TargetLUFS == old.TargetLUFS {
		o.TargetLUFS = def.TargetLUFS
	}
	if o.Mp3Bitrate == old.Mp3Bitrate {
		o.Mp3Bitrate = def.Mp3Bitrate
	}
	if o.OpusBitrate == old.OpusBitrate {
		o.OpusBitrate = def.OpusBitrate
	}
	o.Stereo = stereo
}

// Channels is "mono" or "stereo".
func (o Options) Channels() string {
	if o.Stereo {
		return "stereo"
	}
	return "mono"
}

func DefaultOptions() Options {
	mono := DefaultsFor(false)
	return Options{
		Mp3Bitrate:     mono.Mp3Bitrate,
		OpusBitrate:    mono.OpusBitrate,
		TargetLUFS:     mono.TargetLUFS,
		TruePeak:       -1.5,
		NoiseReduction: 6,
		Leveling:       "normal",
		Compression:    "off",
		Outputs:        AllOutputs,
	}
}

// Paths are the files for one date folder.
type Paths struct {
	Folder   string
	Date     string
	Original string // where the whole recording is kept: <folder>/original
	Master   string
	Mp3      string
	Opus     string
	// The sermon only, without the intro and outro: made when a run has them, <date>_sermon.<ext>.
	SermonMaster, SermonMp3, SermonOpus string
}

// SermonOnly is what the names of the sermon-only outputs end in, before the extension.
const SermonOnly = "_sermon"

// OriginalDir is the folder in a date folder that holds the whole recording
// once a run has been made.
const OriginalDir = "original"

// NewPaths resolves the files for a date folder.
func NewPaths(folder string) (Paths, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return Paths{}, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return Paths{}, fmt.Errorf("%s is not a directory", abs)
	}
	date := filepath.Base(abs)
	return Paths{
		Folder:   abs,
		Date:     date,
		Original: filepath.Join(abs, OriginalDir),
		Master:   filepath.Join(abs, date+".flac"),
		Mp3:      filepath.Join(abs, date+".mp3"),
		Opus:     filepath.Join(abs, date+".opus"),

		SermonMaster: filepath.Join(abs, date+SermonOnly+".flac"),
		SermonMp3:    filepath.Join(abs, date+SermonOnly+".mp3"),
		SermonOpus:   filepath.Join(abs, date+SermonOnly+".opus"),
	}, nil
}

// RawOriginal is the whole recording kept in original/ after a run, of any
// input type, or "" if there is none: <date>_raw.<ext>, else the one file of an
// input type there is (a recording keeps the name it arrived with).
func (p Paths) RawOriginal() string {
	if f := p.findRaw(p.Original); f != "" {
		return f
	}
	des, _ := os.ReadDir(p.Original)
	best, rank := "", len(inputTypes)
	for _, de := range des {
		if !de.Type().IsRegular() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		for i, t := range inputTypes {
			if i < rank && strings.EqualFold(filepath.Ext(de.Name()), t.ext) {
				best, rank = filepath.Join(p.Original, de.Name()), i
			}
		}
	}
	return best
}

// originalFile is the name a recording being moved into original/ gets: the one it arrived
// with (ext is the extension of what is kept), else <date>_raw.<ext>.
func (p *Pipeline) originalFile(ext string) string {
	if p.Opt.OriginalName == "" {
		return p.P.Date + rawName + ext
	}
	return p.Opt.OriginalName + ext
}

// rawName is the name of the whole recording: <date>_raw.<ext>, ext one of
// InputTypes.
const rawName = "_raw"

// findRaw is the whole recording in dir, or "".
func (p Paths) findRaw(dir string) string {
	for _, ext := range InputTypes() {
		if f := filepath.Join(dir, p.Date+rawName+ext); exists(f) {
			return f
		}
	}
	return ""
}

// Pipeline runs the steps for one date folder.
type Pipeline struct {
	P   Paths
	Opt Options
	FF  FFmpeg
	Out io.Writer

	// Optional hooks for a UI: OnStep gets each step title, OnProgress the
	// fraction (0..1) done of the current ffmpeg encode.
	OnStep     func(title string)
	OnProgress func(fraction float64)

	raw     string   // the whole recording, if any
	master  string   // the master of the sermon only
	full    string   // the master with the intro and outro (p.master without them)
	cleanup []string // temporary files to remove when the run ends
}

func (p *Pipeline) logf(format string, a ...any) { fmt.Fprintf(p.Out, format+"\n", a...) }
func (p *Pipeline) step(title string) {
	p.logf("\n== %s", title)
	if p.OnStep != nil {
		p.OnStep(title)
	}
}
func (p *Pipeline) warnf(format string, a ...any) {
	p.logf("WARNING: "+format, a...)
}

// inFolder is <date><suffix> in the date folder.
func (p *Pipeline) inFolder(suffix string) string {
	return filepath.Join(p.P.Folder, p.P.Date+suffix)
}

// Source is the whole recording a run would use, found the same way as the
// pipeline: in the date folder first (a new arrival), then in original/.
func (p Paths) Source() string {
	if raw := p.findRaw(p.Folder); raw != "" {
		return raw
	}
	return p.RawOriginal()
}

// runTo runs ffmpeg into dst, reporting progress against total seconds when
// OnProgress is set and total is known.
func (p *Pipeline) runTo(ctx context.Context, dst string, total float64, args ...string) error {
	if p.OnProgress == nil || total <= 0 {
		return p.FF.RunTo(ctx, dst, args...)
	}
	p.OnProgress(0)
	return p.FF.RunToProgress(ctx, dst, func(s float64) { p.OnProgress(math.Min(s/total, 1)) }, args...)
}

// lengthFor is the length of path in seconds if progress is reported, else 0.
func (p *Pipeline) lengthFor(ctx context.Context, path string) float64 {
	if p.OnProgress == nil {
		return 0
	}
	d, _ := p.FF.Duration(ctx, path)
	return d
}

// Run runs the whole flow.
func (p *Pipeline) Run(ctx context.Context) error {
	defer func() {
		for _, f := range p.cleanup {
			Remove(f)
		}
	}()
	if !p.Opt.cutting() {
		return errors.New("select where the sermon starts and ends")
	}
	for _, c := range [][2]string{{"intro", p.Opt.Intro}, {"outro", p.Opt.Outro}} {
		if c[1] != "" && !exists(c[1]) {
			return fmt.Errorf("the %s (%s) is missing", c[0], filepath.Base(c[1]))
		}
	}
	if err := p.stepRaw(ctx); err != nil {
		return err
	}
	if err := p.stepMaster(ctx); err != nil {
		return err
	}
	if err := p.stepClips(ctx); err != nil {
		return err
	}
	if err := p.stepLossy(ctx); err != nil {
		return err
	}
	return p.report(ctx)
}

// --- 1. raw: the whole recording, of any of the InputTypes -----------------

func (p *Pipeline) stepRaw(ctx context.Context) error {
	p.step("1. Raw recording")
	if err := os.MkdirAll(p.P.Original, 0o755); err != nil {
		return err
	}
	raw, err := p.ingest(ctx)
	if err != nil {
		return err
	}
	if raw == "" {
		return errors.New("no raw recording to cut")
	}
	p.raw = raw
	p.logf("Cutting %s from %s to %s%s while making the master.", filepath.Base(p.raw), p.cutFrom(), p.cutTo(), p.cutsNote())
	return nil
}

// ingest brings the recording in the date folder into original/ and returns
// where it is there ("" if there is none).
//   - One that is kept as it is (FLAC, compressed audio) is moved.
//   - Other lossless audio (WAV, AIFF) is made into FLAC there, and a video's
//     sound copied out (both are normally done as it arrives); the source is
//     deleted only when the audio is checked to be identical.
//
// A recording already in original/ is replaced only with Force.
func (p *Pipeline) ingest(ctx context.Context) (string, error) {
	have := p.P.RawOriginal()
	src := p.P.findRaw(p.P.Folder)
	switch {
	case src == "":
		if have != "" {
			p.logf("Using %s.", filepath.Base(have))
		}
		return have, nil
	case have != "" && !p.Opt.Force:
		p.warnf("%s is already in original/; leaving %s in the folder.", filepath.Base(have), filepath.Base(src))
		return have, nil
	}
	dst := filepath.Join(p.P.Original, p.originalFile(filepath.Ext(src)))
	if to := ConvertsTo(src); to == "" {
		if err := MoveFile(src, dst); err != nil {
			return "", err
		}
		p.logf("Moved %s to %s.", filepath.Base(src), dst)
	} else {
		dst = filepath.Join(p.P.Original, p.originalFile(filepath.Ext(to)))
		var progress func(float64)
		if total := p.lengthFor(ctx, src); total > 0 {
			p.OnProgress(0)
			progress = func(s float64) { p.OnProgress(math.Min(s/total, 1)) }
		}
		if err := p.FF.Convert(ctx, src, dst, progress); err != nil {
			return "", err
		}
		if err := Remove(src); err != nil {
			return "", err
		}
		p.logf("OK: %s has exactly the audio of %s. Deleted %s.", filepath.Base(dst), filepath.Base(src), filepath.Base(src))
	}
	if have != "" && have != dst { // the one it replaces, of another type
		Remove(have)
	}
	return dst, nil
}

// cutInput is how ffmpeg reads the sermon from the raw recording: the input
// options and, when parts are cut out, a filter graph that joins what is kept
// and ends in the label [cut].
// Each kept part is its own input, so ffmpeg never has to hold the audio
// between two parts in memory.
func (p *Pipeline) cutInput(ctx context.Context) (args []string, graph string, err error) {
	if len(p.Opt.Cuts) == 0 && len(p.Opt.Pauses) == 0 {
		return append(p.cutArgs(), "-i", p.raw), "", nil
	}
	end, err := p.FF.Duration(ctx, p.raw)
	if err != nil {
		return nil, "", err
	}
	kept, after := p.Opt.parts(end)
	if len(kept) == 0 {
		return nil, "", errors.New("the cuts leave nothing of the sermon")
	}
	// With silence between them the parts must agree on rate and channels with it.
	same := ""
	for _, s := range after[:len(after)-1] {
		if s > 0 {
			same = "aresample=48000,aformat=channel_layouts=" + p.Opt.Channels()
		}
	}
	var parts []string
	var order string // the parts and the silences in the order they are joined
	n := 0
	for i, k := range kept {
		args = append(args, "-ss", secs(k.Start), "-to", secs(k.End), "-i", p.raw)
		f := []string{"asetpts=PTS-STARTPTS"}
		if same != "" {
			f = append(f, same)
		}
		if i > 0 {
			f = append(f, fmt.Sprintf("afade=t=in:d=%g", joinFade))
		}
		if i < len(kept)-1 {
			f = append(f, fmt.Sprintf("afade=t=out:st=%.3f:d=%g", math.Max(k.End-k.Start-joinFade, 0), joinFade))
		}
		parts = append(parts, fmt.Sprintf("[%d:a]%s[p%d]", i, strings.Join(f, ","), i))
		order += fmt.Sprintf("[p%d]", i)
		n++
		if i < len(kept)-1 && after[i] > 0 {
			parts = append(parts, fmt.Sprintf("anullsrc=r=48000:cl=%s:d=%g[s%d]", p.Opt.Channels(), after[i], i))
			order += fmt.Sprintf("[s%d]", i)
			n++
		}
	}
	return args, strings.Join(parts, ";") + fmt.Sprintf(";%sconcat=n=%d:v=0:a=1[cut]", order, n), nil
}

// cutsNote is " without 2 cuts" for the log, or nothing.
func (p *Pipeline) cutsNote() string {
	switch n := len(p.Opt.Cuts); n {
	case 0:
		return ""
	case 1:
		return ", leaving out 1 part"
	default:
		return fmt.Sprintf(", leaving out %d parts", n)
	}
}

// cutArgs are the input options for the cut. Placed before -i, ffmpeg
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

// EvenLevels is the dynamic normalization in the master chain: it brings
// quiet speech and loud music to a similar level without shifting time.
const EvenLevels = "dynaudnorm=f=250:g=15:p=0.9"

// ListenLevels is EvenLevels for listening while cutting (previews, players):
// up to 40 dB of gain instead of 20, as raw recordings can be very quiet.
// The master does not need it: its own gain step sets the final level.
const ListenLevels = EvenLevels + ":m=100"

// LevelingNames and CompressionNames are the strengths of the two, weakest first.
var (
	LevelingNames    = []string{"off", "gentle", "normal", "strong"}
	CompressionNames = []string{"off", "light", "normal", "strong"}
)

var levelingFilters = map[string]string{
	"off":    "",
	"gentle": "dynaudnorm=f=500:g=31:p=0.8:m=5",
	"normal": EvenLevels,
	"strong": "dynaudnorm=f=150:g=9:p=0.95:m=20",
}

// The compressor runs on a recording already brought near the target loudness, so its
// thresholds (linear) are about the level of speech there: light takes the loudest
// parts down a little, strong more of the range.
var compressionFilters = map[string]string{
	"off":    "",
	"light":  "acompressor=threshold=0.125:ratio=2:attack=20:release=250",
	"normal": "acompressor=threshold=0.0631:ratio=2:attack=20:release=250",
	"strong": "acompressor=threshold=0.0794:ratio=3:attack=20:release=250",
}

// LevelingName is the leveling, "normal" when none was chosen.
func (o Options) LevelingName() string {
	if o.Leveling == "" {
		return "normal"
	}
	return o.Leveling
}

// CompressionName is the compression, "off" when none was chosen.
func (o Options) CompressionName() string {
	if o.Compression == "" {
		return "off"
	}
	return o.Compression
}

// tidyFilter is the first part of the master chain: the channels that get published, the noise
// taken out, the levels evened out. It does not set the level: that is done by measuring (finish).
func (p *Pipeline) tidyFilter() string {
	// Convert first, whatever the input has (mono, stereo, more), so the rest
	// of the chain works on the channels that get published. A mono recording
	// made stereo is the same signal on both sides.
	f := []string{"aformat=channel_layouts=" + p.Opt.Channels()}
	if p.Opt.NoiseReduction > 0 {
		nr := fmt.Sprintf("afftdn=nr=%d", p.Opt.NoiseReduction)
		if p.Opt.RoomSound > 0 {
			nr += fmt.Sprintf(":rf=%.1f", -38+0.18*float64(p.Opt.RoomSound)) // the default floor is -38 dB
		}
		f = append(f, nr)
	}
	if lv := levelingFilters[p.Opt.LevelingName()]; lv != "" {
		f = append(f, lv)
	}
	return strings.Join(f, ",")
}

// finalFilter sets the level and ends the chain: gain (dB), a limiter that keeps the peaks under the
// true-peak ceiling (with half a dB to spare for what resampling adds), and the fades, last, so nothing
// lifts them again. length (seconds) is only needed for a fade-out.
func (p *Pipeline) finalFilter(gain, length float64) string {
	f := []string{fmt.Sprintf("volume=%.2fdB", gain), p.limiter(), "aresample=48000"}
	if p.Opt.FadeIn > 0 {
		f = append(f, fmt.Sprintf("afade=t=in:d=%g", p.Opt.FadeIn))
	}
	if p.Opt.FadeOut > 0 {
		f = append(f, fmt.Sprintf("afade=t=out:st=%.3f:d=%g", length-p.Opt.FadeOut, p.Opt.FadeOut))
	}
	return strings.Join(f, ",")
}

// limiter keeps the peaks under the true-peak ceiling, with half a dB to spare for what resampling adds.
func (p *Pipeline) limiter() string {
	return fmt.Sprintf("alimiter=limit=%.4f:attack=5:release=50:level=0", math.Pow(10, (p.Opt.TruePeak-0.5)/20))
}

// tidyName is the hidden temporary file a master is made from (in the folder of dst).
func tidyName(dst, stage string) string {
	// Short, so it fits Windows' paths wherever the outputs do (folderFits in web).
	name := filepath.Base(dst)
	return filepath.Join(filepath.Dir(dst), "."+strings.TrimSuffix(name, filepath.Ext(name))+".t"+stage+".flac")
}

// renderMaster makes dst, the sermon with the processing of Opt, in three steps: the cut and the noise
// and levels (the slow part) into a temporary file; the loudness of that, measured, to find the gain that
// brings it to the target (with the compressor in between, if it is on); and the gain, the limiter and
// the fades into dst, measured again, with a correction if the limiter took some of it back.
// codec is for dst; length (seconds) is the sermon's, for the fade out and progress.
func (p *Pipeline) renderMaster(ctx context.Context, dst string, length float64, codec []string) error {
	tidy := tidyName(dst, "1")
	p.cleanup = append(p.cleanup, tidy)
	args, graph, err := p.cutInput(ctx)
	if err != nil {
		return err
	}
	if graph == "" {
		args = append(args, "-map", "0:a:0", "-af", p.tidyFilter()) // not a cover picture or video
	} else {
		args = append(args, "-filter_complex", graph+";[cut]"+p.tidyFilter()+"[out]", "-map", "[out]")
	}
	args = append(args, "-c:a", "flac", "-sample_fmt", "s32")
	if err := p.runTo(ctx, tidy, length, args...); err != nil {
		return err
	}

	src := tidy
	m, err := p.FF.Measure(ctx, tidy)
	if err != nil {
		return err
	}
	if m.LUFS < -70 {
		return errors.New("the sermon is silent: there is nothing to bring to the target loudness")
	}
	gain := math.Min(p.Opt.TargetLUFS-m.LUFS, 70)
	p.logf("Level after noise reduction and leveling: %.1f LUFS; gain %+.1f dB to reach %g.", m.LUFS, gain, p.Opt.TargetLUFS)
	if comp := compressionFilters[p.Opt.CompressionName()]; comp != "" {
		squeezed := tidyName(dst, "2")
		p.cleanup = append(p.cleanup, squeezed)
		if err := p.runTo(ctx, squeezed, length, "-i", tidy, "-af", fmt.Sprintf("volume=%.2fdB,%s", gain, comp), "-c:a", "flac", "-sample_fmt", "s32"); err != nil {
			return err
		}
		if m, err = p.FF.Measure(ctx, squeezed); err != nil {
			return err
		}
		src = squeezed
		gain = math.Min(p.Opt.TargetLUFS-m.LUFS, 70)
		p.logf("%s compression: %.1f LUFS; gain %+.1f dB to reach %g.", p.Opt.CompressionName(), m.LUFS, gain, p.Opt.TargetLUFS)
	}
	for try := 0; ; try++ {
		out := append([]string{"-i", src, "-af", p.finalFilter(gain, length)}, codec...)
		out = append(out, p.Opt.Tags.args()...)
		if err := p.runTo(ctx, dst, length, out...); err != nil {
			return err
		}
		got, err := p.FF.Measure(ctx, dst)
		if err != nil {
			return err
		}
		miss := p.Opt.TargetLUFS - got.LUFS
		if math.Abs(miss) <= 0.3 || try == 2 {
			return nil
		}
		// The limiter took some of the gain back: add it, and again.
		p.logf("%.1f LUFS after the limiter; %+.1f dB more.", got.LUFS, miss)
		gain += miss
	}
}

// Sample renders seconds of the recording from start with the processing in o (no cuts, pauses or fades) as an
// MP3 at dst, to hear what the settings do.
func (f FFmpeg) Sample(ctx context.Context, raw string, o Options, start, seconds float64, dst string) error {
	o.Start, o.End, o.Cuts, o.Pauses, o.FadeIn, o.FadeOut = start, start+seconds, nil, nil, 0, 0
	p := &Pipeline{FF: f, Opt: o, Out: io.Discard, raw: raw}
	defer func() {
		for _, name := range p.cleanup {
			Remove(name)
		}
	}()
	return p.renderMaster(ctx, dst, seconds, []string{"-c:a", "libmp3lame", "-b:a", "96k"})
}

// JoinClipSeconds and JoinSermonSeconds are how much of the clip and of the sermon a join sample has.
const (
	JoinClipSeconds   = 6.0
	JoinSermonSeconds = 10.0
)

// JoinSample makes an MP3 at dst to hear how o's sermon joins its intro (which = "intro") or outro: the end of
// the intro, the pause and the start of the sermon, or the end of the sermon, the pause and the start of the
// outro. That part of the sermon is made as the files are (its processing, and its fade at that end), from its
// first or last kept part.
func (f FFmpeg) JoinSample(ctx context.Context, raw string, o Options, which, dst string) error {
	clip := map[string]string{"intro": o.Intro, "outro": o.Outro}[which]
	if clip == "" {
		return fmt.Errorf("this sermon has no %s", which)
	}
	end, err := f.Duration(ctx, raw)
	if err != nil {
		return err
	}
	kept := o.keep(end)
	if len(kept) == 0 {
		return errors.New("the cuts leave nothing of the sermon")
	}
	w := o
	w.Cuts, w.Pauses = nil, nil
	if which == "intro" {
		w.Start, w.End, w.Outro, w.FadeOut = kept[0].Start, math.Min(kept[0].End, kept[0].Start+JoinSermonSeconds), "", 0
	} else {
		last := kept[len(kept)-1]
		w.Start, w.End, w.Intro, w.FadeIn = math.Max(last.Start, last.End-JoinSermonSeconds), last.End, "", 0
	}
	length := w.End - w.Start
	w.FadeIn, w.FadeOut = math.Min(w.FadeIn, length), math.Min(w.FadeOut, length)
	p := &Pipeline{FF: f, Opt: w, Out: io.Discard, raw: raw}
	defer func() {
		for _, name := range p.cleanup {
			Remove(name)
		}
	}()
	stem := filepath.Join(filepath.Dir(dst), "."+strings.TrimSuffix(filepath.Base(dst), filepath.Ext(dst)))
	p.master = stem + ".m.flac"
	joined := stem + ".j.flac"
	p.cleanup = append(p.cleanup, p.master, joined)
	if err := p.renderMaster(ctx, p.master, length, []string{"-c:a", "flac", "-sample_fmt", "s32"}); err != nil {
		return err
	}
	if err := p.join(ctx, joined); err != nil {
		return err
	}
	clipLength, err := f.Duration(ctx, clip)
	if err != nil {
		return err
	}
	from, to := math.Max(0, clipLength-JoinClipSeconds), clipLength+w.ClipPause+length
	if which == "outro" {
		from, to = 0, length+w.ClipPause+math.Min(clipLength, JoinClipSeconds)
	}
	return f.RunTo(ctx, dst, "-ss", secs(from), "-to", secs(to), "-i", joined, "-c:a", "libmp3lame", "-b:a", "128k")
}

// pending reports whether an output file needs to be made.
func (p *Pipeline) pending(path string) bool { return p.Opt.Force || !exists(path) }

// version is one set of outputs: the main files, or with an intro or outro also the sermon only.
type version struct {
	label             string // for the log: "" for the main files
	master, mp3, opus string
	from              string // the master MP3 and Opus are made from: p.full or p.master
}

// versions are the sets of outputs a run makes.
func (p *Pipeline) versions() []version {
	main := version{"", p.P.Master, p.P.Mp3, p.P.Opus, p.full}
	if !p.Opt.clips() {
		return []version{main}
	}
	return []version{main, {" (sermon only)", p.P.SermonMaster, p.P.SermonMp3, p.P.SermonOpus, p.master}}
}

// lossyPending reports whether an MP3 or Opus of v needs to be made.
func (p *Pipeline) lossyPending(v version) bool {
	o := p.Opt.Outputs
	return (o.Mp3 && p.pending(v.mp3)) || (o.Opus && p.pending(v.opus))
}

// stepMaster makes the master of the sermon: <date>.flac, or with an intro or outro <date>_sermon.flac,
// which the one with them is made from.
func (p *Pipeline) stepMaster(ctx context.Context) error {
	o := p.Opt.Outputs
	vs := p.versions()
	p.master = vs[len(vs)-1].master
	needed := false // by an output made from it
	for _, v := range vs {
		needed = needed || p.lossyPending(v)
	}
	if p.Opt.clips() {
		needed = needed || (o.Flac && p.pending(p.P.Master))
	}
	switch {
	case o.Flac:
		p.step(fmt.Sprintf("3. Master (%s, %g LUFS)", p.Opt.Channels(), p.Opt.TargetLUFS))
		if !p.pending(p.master) {
			p.logf("Already exists, skipping (Save and generate redoes it).")
			return nil
		}
	case !needed:
		return nil // no master wanted, and nothing to make from one
	case !p.pending(p.master):
		p.step("3. Master")
		p.logf("Using the existing %s for MP3/Opus.", filepath.Base(p.master))
		return nil
	default:
		// Not wanted as output, but MP3/Opus are made from it. Kept in the
		// date folder (it can be large) under a hidden name, removed after the run.
		p.step(fmt.Sprintf("3. Master (temporary, for MP3/Opus; %s, %g LUFS)", p.Opt.Channels(), p.Opt.TargetLUFS))
		p.master = filepath.Join(p.P.Folder, "."+p.P.Date+".master.flac")
		p.cleanup = append(p.cleanup, p.master)
	}
	if p.Opt.Stereo {
		p.checkStereo(ctx)
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
	if length == 0 && p.OnProgress != nil {
		length, _ = p.sermonLength(ctx)
	}
	return p.renderMaster(ctx, p.master, length, []string{"-c:a", "flac", "-sample_fmt", "s32"})
}

// --- 4. intro and outro ---------------------------------------------------

// stepClips makes the master with the intro and outro (<date>.flac) from the sermon's.
func (p *Pipeline) stepClips(ctx context.Context) error {
	p.full = p.master
	if !p.Opt.clips() {
		return nil
	}
	o := p.Opt.Outputs
	p.full = p.P.Master
	switch {
	case o.Flac:
		p.step("4. Intro and outro")
		if !p.pending(p.full) {
			p.logf("Already exists, skipping (Save and generate redoes it).")
			return nil
		}
	case !p.lossyPending(p.versions()[0]):
		return nil
	case !p.pending(p.full):
		p.step("4. Intro and outro")
		p.logf("Using the existing %s for MP3/Opus.", filepath.Base(p.full))
		return nil
	default:
		p.step("4. Intro and outro")
		p.full = filepath.Join(p.P.Folder, "."+p.P.Date+".full.flac")
		p.cleanup = append(p.cleanup, p.full)
	}
	return p.join(ctx, p.full)
}

// join makes dst: the intro, the sermon's master and the outro, with ClipPause of silence at each join.
// Each clip is brought to the target loudness (gain and the limiter, no other processing) and to the
// sermon's rate and channels; the sermon is used as it is, with its own fades. The tags are the sermon's.
func (p *Pipeline) join(ctx context.Context, dst string) error {
	same := "aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=" + p.Opt.Channels()
	var args, graph []string
	var order string // the parts in the order they are joined
	var total float64
	n, sermonIn := 0, 0
	add := func(filter string, seconds float64) {
		graph = append(graph, fmt.Sprintf("%s,%s[j%d]", filter, same, n))
		order += fmt.Sprintf("[j%d]", n)
		total += seconds
		n++
	}
	pause := func() {
		if p.Opt.ClipPause > 0 {
			add(fmt.Sprintf("anullsrc=r=48000:cl=%s:d=%g", p.Opt.Channels(), p.Opt.ClipPause), p.Opt.ClipPause)
		}
	}
	clip := func(what, path string) error {
		m, err := p.FF.Measure(ctx, path)
		if err != nil {
			return err
		}
		if m.LUFS <= -70 {
			return fmt.Errorf("the %s is silent", what)
		}
		length, err := p.FF.Duration(ctx, path)
		if err != nil {
			return err
		}
		gain := math.Min(p.Opt.TargetLUFS-m.LUFS, 70)
		p.logf("%s %s: %s, %.1f LUFS; gain %+.1f dB.", strings.ToUpper(what[:1])+what[1:], filepath.Base(path), FormatTime(math.Round(length)), m.LUFS, gain)
		add(fmt.Sprintf("[%d:a:0]aformat=channel_layouts=%s,aresample=48000,volume=%.2fdB,%s", len(args)/2, p.Opt.Channels(), gain, p.limiter()), length)
		args = append(args, "-i", path)
		return nil
	}
	if p.Opt.Intro != "" {
		if err := clip("intro", p.Opt.Intro); err != nil {
			return err
		}
		pause()
	}
	length, err := p.FF.Duration(ctx, p.master)
	if err != nil {
		return err
	}
	sermonIn = len(args) / 2
	add(fmt.Sprintf("[%d:a:0]anull", sermonIn), length)
	args = append(args, "-i", p.master)
	if p.Opt.Outro != "" {
		pause()
		if err := clip("outro", p.Opt.Outro); err != nil {
			return err
		}
	}
	graph = append(graph, fmt.Sprintf("%sconcat=n=%d:v=0:a=1[out]", order, n))
	args = append(args, "-filter_complex", strings.Join(graph, ";"), "-map", "[out]", "-map_metadata", strconv.Itoa(sermonIn),
		"-c:a", "flac", "-sample_fmt", "s32")
	progress := total
	if p.OnProgress == nil {
		progress = 0
	}
	if err := p.runTo(ctx, dst, progress, append(args, p.Opt.Tags.args()...)...); err != nil {
		return err
	}
	m, err := p.FF.Measure(ctx, dst)
	if err != nil {
		return err
	}
	p.logf("With intro and outro: %s, %.1f LUFS.", FormatTime(math.Round(total)), m.LUFS)
	return nil
}

// checkStereo warns when stereo output would add nothing: the part that
// becomes the sermon is mono, or its two channels are (nearly) the same.
func (p *Pipeline) checkStereo(ctx context.Context) {
	c, err := p.FF.CheckChannels(ctx, p.raw, p.cutArgs()...)
	switch {
	case err != nil:
		p.warnf("could not check the channels: %v", err)
	case c.Channels == 1:
		p.warnf("the source is mono, so stereo output is the same signal on both sides: " +
			"same sound, bigger files. Choose mono.")
	case c.Channels == 2 && math.IsInf(c.Side, -1):
		p.warnf("the two channels of the source are identical, so stereo output adds nothing: " +
			"same sound, bigger files. Choose mono.")
	case c.EffectivelyMono():
		p.warnf("the two channels of the source are nearly identical (their difference is %.0f dB below the signal), "+
			"so stereo output adds next to nothing. Choose mono.", -c.Side)
	}
	if side, by := c.Quieter(); side != "" {
		how := fmt.Sprintf("%.0f dB quieter than the other", by)
		if math.IsInf(by, 1) {
			how = "silent"
		}
		p.warnf("the %s channel of the source is %s, so stereo output has the sermon on one side only. "+
			"Choose mono, which mixes both channels.", side, how)
	}
}

// sermonLength is the length in seconds of what goes into the master.
func (p *Pipeline) sermonLength(ctx context.Context) (float64, error) {
	end, err := p.FF.Duration(ctx, p.raw)
	if err != nil {
		return 0, err
	}
	var length float64
	kept, after := p.Opt.parts(end) // an end past the end of the recording just runs to the end
	for _, k := range kept {
		length += k.End - k.Start
	}
	return length + pauseTotal(after), nil
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

func (p *Pipeline) encodeLossy(ctx context.Context, name, src, dst string, codec []string) error {
	if exists(dst) && !p.Opt.Force {
		p.logf("%s exists, skipping.", name)
		return nil
	}
	total := p.lengthFor(ctx, src)
	enc := func(gain *float64) error {
		args := []string{"-i", src}
		if gain != nil {
			args = append(args, "-af", fmt.Sprintf("volume=%.2fdB", *gain))
		}
		args = append(args, codec...)
		return p.runTo(ctx, dst, total, append(args, p.Opt.Tags.args()...)...)
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
	o := p.Opt.Outputs
	var names []string
	if o.Mp3 {
		names = append(names, fmt.Sprintf("MP3 (%s)", p.Opt.Mp3Bitrate))
	}
	if o.Opus {
		names = append(names, fmt.Sprintf("Opus (%s)", p.Opt.OpusBitrate))
	}
	if len(names) == 0 {
		return nil
	}
	p.step("5. " + strings.Join(names, " and ") + ", " + p.Opt.Channels())
	ac := "1"
	if p.Opt.Stereo {
		ac = "2"
	}
	for _, v := range p.versions() {
		if o.Mp3 {
			if err := p.encodeLossy(ctx, "mp3"+v.label, v.from, v.mp3,
				[]string{"-ar", "44100", "-ac", ac, "-c:a", "libmp3lame", "-b:a", p.Opt.Mp3Bitrate, "-id3v2_version", "3"}); err != nil {
				return err
			}
		}
		if o.Opus {
			if err := p.encodeLossy(ctx, "opus"+v.label, v.from, v.opus,
				[]string{"-ar", "48000", "-ac", ac, "-c:a", "libopus", "-b:a", p.Opt.OpusBitrate, "-application", "voip"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- report ---------------------------------------------------------------

func (p *Pipeline) report(ctx context.Context) error {
	p.step(fmt.Sprintf("Result (target: %g LUFS, max %g dBTP)", p.Opt.TargetLUFS, p.Opt.TruePeak))
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "File\tLUFS\tTP\tLRA\tMB")
	var files []string
	for _, v := range p.versions() {
		for _, f := range []struct {
			on   bool
			path string
		}{{p.Opt.Outputs.Flac, v.master}, {p.Opt.Outputs.Mp3, v.mp3}, {p.Opt.Outputs.Opus, v.opus}} {
			if f.on {
				files = append(files, f.path)
			}
		}
	}
	for _, f := range files {
		m, err := p.FF.Measure(ctx, f)
		if err != nil {
			return err
		}
		st, _ := os.Stat(f)
		fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t%.1f\n", filepath.Base(f), m.LUFS, m.TruePeak, m.LRA, float64(st.Size())/1048576)
		if m.TruePeak > -1.0 {
			defer p.warnf("%s: true peak %.1f dBTP is above -1.", filepath.Base(f), m.TruePeak)
		}
		if math.Abs(m.LUFS-p.Opt.TargetLUFS) > 1.5 {
			defer p.warnf("%s: %.1f LUFS is more than 1.5 from the target (loud peaks in the recording can keep it from getting louder: try compression).", filepath.Base(f), m.LUFS)
		}
	}
	return tw.Flush()
}
