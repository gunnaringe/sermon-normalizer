package normalizer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A file another program has open cannot be renamed or removed on Windows; Rename and Remove wait for it to
// be closed (a preview played to the end, a virus scan done).
func TestRetryWhileInUse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct {
		name string
		do   func() error
	}{
		{"rename", func() error { return Rename(path, filepath.Join(dir, "b.mp3")) }},
		{"remove", func() error { return Remove(filepath.Join(dir, "b.mp3")) }},
	} {
		held := path
		if op.name == "remove" {
			held = filepath.Join(dir, "b.mp3")
		}
		f, err := os.Open(held) // without FILE_SHARE_DELETE, like most programs
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(held, held+".x"); err == nil || !inUse(err) {
			t.Fatalf("%s: os.Rename of an open file: %v, want in use", op.name, err)
		}
		time.AfterFunc(300*time.Millisecond, func() { f.Close() })
		if err := op.do(); err != nil {
			t.Errorf("%s: %v", op.name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "b.mp3")); !os.IsNotExist(err) {
		t.Errorf("b.mp3 is still there: %v", err)
	}
}
