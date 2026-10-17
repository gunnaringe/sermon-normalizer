package main

import (
	"reflect"
	"testing"
)

func TestReorder(t *testing.T) {
	got := reorder([]string{"mappe", "--keep-wav", "--mp3-bitrate", "128k", "--force"})
	want := []string{"--keep-wav", "--mp3-bitrate", "128k", "--force", "mappe"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fikk %v", got)
	}
}

func TestReorderStartEnd(t *testing.T) {
	got := reorder([]string{"mappe", "--start", "41:20", "--end=1:12:05"})
	want := []string{"--start", "41:20", "--end=1:12:05", "mappe"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fikk %v", got)
	}
}
