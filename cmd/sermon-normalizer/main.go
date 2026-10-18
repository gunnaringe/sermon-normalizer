package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
	"github.com/gunnaringe/sermon-normalizer/tui"
)

var version = "0.1.0"

const usage = `sermon-normalizer: processes sermon recordings (raw + edit -> master.flac, mp3, opus)

Usage:
  sermon-normalizer tui [folder] [flags]   interactive: pick a folder, cut, set options, run
  sermon-normalizer run <folder> [flags]   run the whole flow for a date folder
  sermon-normalizer measure <file>...      measure LUFS / true peak / LRA
  sermon-normalizer version

Run "sermon-normalizer run -h" or "sermon-normalizer tui -h" for flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(ctx, os.Args[2:])
	case "tui":
		err = cmdTUI(ctx, os.Args[2:])
	case "measure":
		err = cmdMeasure(ctx, os.Args[2:])
	case "version", "--version":
		fmt.Println("sermon-normalizer", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newFFmpeg(bin string) (normalizer.FFmpeg, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return normalizer.FFmpeg{}, fmt.Errorf("cannot find %q in PATH (Linux: apt install ffmpeg)", bin)
	}
	return normalizer.FFmpeg{Bin: path}, nil
}

// reorder lets flags go both before and after the positional arguments.
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) < 2 || a[0] != '-' {
			rest = append(rest, a)
			continue
		}
		if a == "--" {
			flags = append(flags, "--")
			return append(append(flags, rest...), args[i+1:]...)
		}
		flags = append(flags, a)
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !hasValue && !isBoolFlag(fs, name) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}

func isBoolFlag(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func cmdRun(ctx context.Context, args []string) error {
	opt := normalizer.DefaultOptions()
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	channels := fs.String("channels", "mono", "mono or stereo output")
	fs.Float64Var(&opt.TargetLUFS, "target-lufs", opt.TargetLUFS, "target loudness; -16 with --channels stereo")
	fs.Float64Var(&opt.TruePeak, "true-peak", opt.TruePeak, "max true peak (dBTP) for the master")
	fs.StringVar(&opt.Mp3Bitrate, "mp3-bitrate", opt.Mp3Bitrate, "mp3 bitrate; 128k with --channels stereo")
	fs.StringVar(&opt.OpusBitrate, "opus-bitrate", opt.OpusBitrate, "opus bitrate; 96k with --channels stereo")
	fs.IntVar(&opt.NoiseReduction, "noise-reduction", opt.NoiseReduction, "noise reduction in dB (0 = off)")
	fs.BoolVar(&opt.KeepWav, "keep-wav", false, "do not delete the WAV after verification")
	fs.BoolVar(&opt.Force, "force", false, "recreate output files")
	fs.Func("start", "cut the sermon from raw: where it starts (h:mm:ss, mm:ss or seconds)", timeFlag(&opt.Start))
	fs.Func("end", "cut the sermon from raw: where it ends (h:mm:ss, mm:ss or seconds)", timeFlag(&opt.End))
	fs.Float64Var(&opt.FadeIn, "fade-in", 0, "fade in at the start of the sermon, in seconds (0 = off)")
	fs.Float64Var(&opt.FadeOut, "fade-out", 0, "fade out at the end of the sermon, in seconds (0 = off)")
	formats := fs.String("formats", opt.Outputs.String(), "comma-separated outputs to make: flac (master), mp3, opus")
	archive := fs.String("archive", "", "archive the sources (raw, edit) as FLAC in this folder, relative to the date folder\n(default: no archive, sources are left in place)")
	ffBin := fs.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return err
	}
	var err error
	if opt.Outputs, err = normalizer.ParseOutputs(*formats); err != nil {
		return err
	}
	if err := applyChannels(fs, &opt, *channels); err != nil {
		return err
	}
	if opt.FadeIn < 0 || opt.FadeOut < 0 {
		return errors.New("--fade-in and --fade-out cannot be negative")
	}
	if opt.End > 0 && opt.End <= opt.Start {
		return errors.New("--end must be after --start")
	}
	if fs.NArg() != 1 {
		return errors.New("give exactly one folder: sermon-normalizer run <folder>")
	}
	ff, err := newFFmpeg(*ffBin)
	if err != nil {
		return err
	}
	paths, err := normalizer.NewPaths(fs.Arg(0), *archive)
	if err != nil {
		return err
	}
	p := &normalizer.Pipeline{P: paths, Opt: opt, FF: ff, Out: os.Stdout}
	return p.Run(ctx)
}

// applyChannels sets mono/stereo; the loudness target and bitrates follow
// unless they were given on the command line.
func applyChannels(fs *flag.FlagSet, opt *normalizer.Options, channels string) error {
	var stereo bool
	switch channels {
	case "mono":
	case "stereo":
		stereo = true
	default:
		return fmt.Errorf("--channels must be mono or stereo, not %q", channels)
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	explicit := *opt
	opt.SetStereo(stereo)
	if given["target-lufs"] {
		opt.TargetLUFS = explicit.TargetLUFS
	}
	if given["mp3-bitrate"] {
		opt.Mp3Bitrate = explicit.Mp3Bitrate
	}
	if given["opus-bitrate"] {
		opt.OpusBitrate = explicit.OpusBitrate
	}
	return nil
}

func cmdTUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	archive := fs.String("archive", "", "default archive folder (can be changed in the options)")
	ffBin := fs.String("ffmpeg", "ffmpeg", "path to ffmpeg (ffplay is looked for next to it, for playback)")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("give at most one folder: sermon-normalizer tui [folder]")
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	ff, err := newFFmpeg(*ffBin)
	if err != nil {
		return err
	}
	return tui.Run(ctx, tui.Config{Dir: dir, Archive: *archive, FFmpeg: ff, Options: normalizer.DefaultOptions()})
}

func timeFlag(dst *float64) func(string) error {
	return func(s string) error {
		v, err := normalizer.ParseTime(s)
		*dst = v
		return err
	}
}

func cmdMeasure(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("measure", flag.ContinueOnError)
	ffBin := fs.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("give at least one file: sermon-normalizer measure <file>...")
	}
	ff, err := newFFmpeg(*ffBin)
	if err != nil {
		return err
	}
	for _, f := range fs.Args() {
		m, err := ff.Measure(ctx, f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("%s\t%.1f LUFS\t%.1f dBTP\t%.1f LU\n", f, m.LUFS, m.TruePeak, m.LRA)
	}
	return nil
}
