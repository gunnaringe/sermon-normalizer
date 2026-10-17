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
)

var version = "0.1.0"

const usage = `talenorm: behandler tale-opptak (raw + edit -> master.flac, mp3, opus)

Bruk:
  talenorm run <mappe> [flagg]     kjor hele flyten for en datomappe
  talenorm measure <fil>...        mal LUFS / true peak / LRA
  talenorm version

Kjor "talenorm run -h" for flagg.
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
		fmt.Println("talenorm", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "ukjent kommando %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "feil:", err)
		os.Exit(1)
	}
}

func newFFmpeg(bin string) (FFmpeg, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return FFmpeg{}, fmt.Errorf("finner ikke %q i PATH (Linux: apt install ffmpeg)", bin)
	}
	return FFmpeg{Bin: path}, nil
}

// reorder lar flagg stå både før og etter mappenavnet.
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			// flagg med verdi (ikke bool) tar neste argument
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
	opt := defaultOptions()
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Float64Var(&opt.TargetLUFS, "target-lufs", opt.TargetLUFS, "mal-loudness (mono -19, stereo -16)")
	fs.Float64Var(&opt.TruePeak, "true-peak", opt.TruePeak, "maks true peak (dBTP) for master")
	fs.StringVar(&opt.Mp3Bitrate, "mp3-bitrate", opt.Mp3Bitrate, "mp3-bitrate")
	fs.StringVar(&opt.OpusBitrate, "opus-bitrate", opt.OpusBitrate, "opus-bitrate")
	fs.IntVar(&opt.NoiseReduction, "noise-reduction", opt.NoiseReduction, "stoyfjerning i dB (0 = av)")
	fs.BoolVar(&opt.KeepWav, "keep-wav", false, "ikke slett WAV etter verifisering")
	fs.BoolVar(&opt.Force, "force", false, "lag resultatfilene paa nytt")
	ffBin := fs.String("ffmpeg", "ffmpeg", "sti til ffmpeg")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("oppgi noyaktig en mappe: talenorm run <mappe>")
	}
	ff, err := newFFmpeg(*ffBin)
	if err != nil {
		return err
	}
	paths, err := NewPaths(fs.Arg(0))
	if err != nil {
		return err
	}
	p := &Pipeline{P: paths, Opt: opt, FF: ff, Out: os.Stdout}
	return p.Run(ctx)
}

func cmdMeasure(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("oppgi minst en fil: talenorm measure <fil>...")
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
