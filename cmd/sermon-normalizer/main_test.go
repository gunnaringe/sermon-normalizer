package main

import (
	"reflect"
	"testing"
)

func TestReorder(t *testing.T) {
	got := reorder([]string{"folder", "--keep-wav", "--mp3-bitrate", "128k", "--force"})
	want := []string{"--keep-wav", "--mp3-bitrate", "128k", "--force", "folder"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestReorderStartEnd(t *testing.T) {
	got := reorder([]string{"folder", "--start", "41:20", "--end=1:12:05"})
	want := []string{"--start", "41:20", "--end=1:12:05", "folder"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}
