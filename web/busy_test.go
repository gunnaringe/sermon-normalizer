package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

func TestBusy(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	if ts.Busy() {
		t.Fatal("busy at the start")
	}
	release := make(chan struct{})
	ts.background(func() { <-release })
	if !ts.Busy() {
		t.Error("not busy while background work runs")
	}
	close(release)
	for deadline := time.Now().Add(time.Second); ts.Busy(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("still busy after the background work ended")
		}
	}

	// Between two chunks of an upload no request is open, but stopping would break it.
	if rec := chunk(t, ts, "2026-10-04", "x.flac", 6, 0, "abc", time.Time{}); rec.Code != http.StatusOK {
		t.Fatalf("chunk: %d %s", rec.Code, rec.Body)
	}
	if !ts.Busy() {
		t.Error("not busy between two chunks of an upload")
	}
	ts.lastChunk.Store(time.Now().Add(-uploadStale).UnixNano())
	if ts.Busy() {
		t.Error("busy with an upload that stopped long ago")
	}
	// The last chunk ends it.
	if rec := chunk(t, ts, "2026-10-04", "x.flac", 6, 3, "def", time.Time{}); rec.Code >= 300 {
		t.Fatalf("last chunk: %d %s", rec.Code, rec.Body)
	}
	for deadline := time.Now().Add(2 * time.Second); ts.Busy(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("still busy after the upload finished")
		}
	}
}

func TestAppBusy(t *testing.T) {
	a, _, _, _ := newTestApp(t)
	if a.Busy() {
		t.Fatal("busy at the start")
	}
	release := make(chan struct{})
	defer close(release)
	a.tenants[0].srv.background(func() { <-release })
	if !a.Busy() {
		t.Error("not busy while a church works")
	}
}
