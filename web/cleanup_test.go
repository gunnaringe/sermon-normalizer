package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Half-written files are removed once they have sat untouched a day, with
// the date folder an abandoned upload leaves empty; nothing else is.
func TestSweepLeftovers(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	old := time.Now().Add(-leftoverAge - time.Hour)
	mk := func(p string, mtime time.Time) string {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, mtime, mtime)
		return p
	}
	upload := mk(filepath.Join(ts.root, "2026", "2026-11-01", ".2026-11-01_raw.upload"), old)
	encode := mk(filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04.partial.mp3"), old)
	raw := mk(filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04_raw.flac"), old)
	share := mk(filepath.Join(ts.inbox, ".share-abc.partial"), old)
	fresh := mk(filepath.Join(ts.inbox, ".share-def.partial"), time.Now())
	busy := mk(filepath.Join(ts.root, "2026", "2026-10-11", ".2026-10-11_raw.upload"), time.Now())

	ts.sweepLeftovers(time.Now()) // the one at start may have taken some already
	for _, p := range []string{upload, encode, share, filepath.Dir(upload)} {
		if fileExists(p) {
			t.Errorf("%s is still there", p)
		}
	}
	for _, p := range []string{raw, fresh, busy} {
		if !fileExists(p) {
			t.Errorf("%s was removed", p)
		}
	}
}
