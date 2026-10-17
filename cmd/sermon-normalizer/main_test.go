package main

import (
	"flag"
	"reflect"
	"testing"
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
