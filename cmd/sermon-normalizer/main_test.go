package main

import (
	"flag"
	"reflect"
	"testing"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

func testFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Bool("keep-wav", false, "")
	fs.Bool("force", false, "")
	fs.String("mp3-bitrate", "", "")
	fs.String("archive", "", "")
	fs.Func("start", "", func(string) error { return nil })
	fs.Func("end", "", func(string) error { return nil })
	return fs
}

func TestReorder(t *testing.T) {
	cases := []struct{ in, want []string }{
		{
			[]string{"folder", "--keep-wav", "--mp3-bitrate", "128k", "--force"},
			[]string{"--keep-wav", "--mp3-bitrate", "128k", "--force", "folder"},
		},
		{
			[]string{"folder", "--start", "41:20", "--end=1:12:05"},
			[]string{"--start", "41:20", "--end=1:12:05", "folder"},
		},
		// bool flags never swallow the folder, whatever their position
		{
			[]string{"--force", "folder", "--archive", "arkiv"},
			[]string{"--force", "--archive", "arkiv", "folder"},
		},
		{
			[]string{"-force", "folder"},
			[]string{"-force", "folder"},
		},
		{
			[]string{"a", "--force", "--", "-b"},
			[]string{"--force", "--", "a", "-b"},
		},
		{
			[]string{"--force=true", "folder"},
			[]string{"--force=true", "folder"},
		},
	}
	for _, c := range cases {
		if got := reorder(testFlags(), c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("reorder(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestApplyChannels(t *testing.T) {
	parse := func(args ...string) (normalizer.Options, error) {
		opt := normalizer.DefaultOptions()
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		channels := fs.String("channels", "mono", "")
		fs.Float64Var(&opt.TargetLUFS, "target-lufs", opt.TargetLUFS, "")
		fs.StringVar(&opt.Mp3Bitrate, "mp3-bitrate", opt.Mp3Bitrate, "")
		fs.StringVar(&opt.OpusBitrate, "opus-bitrate", opt.OpusBitrate, "")
		if err := fs.Parse(args); err != nil {
			return opt, err
		}
		return opt, applyChannels(fs, &opt, *channels)
	}
	if o, _ := parse(); o.Stereo || o.TargetLUFS != -19 || o.Mp3Bitrate != "96k" {
		t.Errorf("default: %+v", o)
	}
	if o, _ := parse("--channels", "stereo"); !o.Stereo || o.TargetLUFS != -16 || o.Mp3Bitrate != "128k" || o.OpusBitrate != "96k" {
		t.Errorf("stereo: %+v", o)
	}
	// Given flags win, even when equal to the mono default.
	if o, _ := parse("--channels", "stereo", "--target-lufs", "-19", "--opus-bitrate", "64k"); o.TargetLUFS != -19 || o.OpusBitrate != "64k" || o.Mp3Bitrate != "128k" {
		t.Errorf("stereo with flags: %+v", o)
	}
	if _, err := parse("--channels", "surround"); err == nil {
		t.Error("expected an error")
	}
}
