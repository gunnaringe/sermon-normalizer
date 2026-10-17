package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

var version = "0.1.0"

const usage = `sermon-normalizer: processes sermon recordings (raw + edit -> master.flac, mp3, opus)

Usage:
  sermon-normalizer run <folder> [flags]   run the whole flow for a date folder
  sermon-normalizer measure <file>...      measure LUFS / true peak / LRA
  sermon-normalizer version

Run "sermon-normalizer run -h" for flags.
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

// reorder lets flags go both before and after the folder name.
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			// flags with a value (not bool) take the next argument
			switch a {
			case "--keep-wav", "-keep-wav", "--force", "-force":
			default:
				if !containsEq(a) && i+1 < len(args) {
					i++
					flags = append(flags, args[i])
				}
			}
		} else {
			rest = append(rest, a)
		}
	}
	return append(flags, rest...)
}

func containsEq(s string) bool {
	for _, c := range s {
		if c == '=' {
			return true
		}
	}
	return false
}

func cmdRun(ctx context.Context, args []string) error {
	opt := normalizer.DefaultOptions()
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Float64Var(&opt.TargetLUFS, "target-lufs", opt.TargetLUFS, "target loudness (mono -19, stereo -16)")
	fs.Float64Var(&opt.TruePeak, "true-peak", opt.TruePeak, "max true peak (dBTP) for the master")
	fs.StringVar(&opt.Mp3Bitrate, "mp3-bitrate", opt.Mp3Bitrate, "mp3 bitrate")
	fs.StringVar(&opt.OpusBitrate, "opus-bitrate", opt.OpusBitrate, "opus bitrate")
	fs.IntVar(&opt.NoiseReduction, "noise-reduction", opt.NoiseReduction, "noise reduction in dB (0 = off)")
	fs.BoolVar(&opt.KeepWav, "keep-wav", false, "do not delete the WAV after verification")
	fs.BoolVar(&opt.Force, "force", false, "recreate output files")
	fs.Func("start", "cut the sermon from raw: where it starts (h:mm:ss, mm:ss or seconds)", timeFlag(&opt.Start))
	fs.Func("end", "cut the sermon from raw: where it ends (h:mm:ss, mm:ss or seconds)", timeFlag(&opt.End))
	archive := fs.String("archive", "", "archive the sources (raw, edit) as FLAC in this folder, relative to the date folder\n(default: no archive, sources are left in place)")
	ffBin := fs.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
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

func timeFlag(dst *float64) func(string) error {
	return func(s string) error {
		v, err := normalizer.ParseTime(s)
		*dst = v
		return err
	}
}

func cmdMeasure(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("give at least one file: sermon-normalizer measure <file>...")
	}
	ff, err := newFFmpeg("ffmpeg")
	if err != nil {
		return err
	}
	for _, f := range args {
		m, err := ff.Measure(ctx, f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("%s\t%.1f LUFS\t%.1f dBTP\t%.1f LU\n", f, m.LUFS, m.TruePeak, m.LRA)
	}
	return nil
}
