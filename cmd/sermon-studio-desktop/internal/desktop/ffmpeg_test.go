package desktop

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func gz(t *testing.T, data string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(data))
	w.Close()
	return b.Bytes()
}

func TestUnpackFFmpeg(t *testing.T) {
	cache := t.TempDir()
	bin, err := unpackFFmpeg(cache, gz(t, "build 1"))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "build 1" {
		t.Fatalf("unpacked %q", b)
	}
	if st, _ := os.Stat(bin); st.Mode().Perm()&0o100 == 0 && filepath.Ext(bin) != ".exe" {
		t.Errorf("not executable: %v", st.Mode())
	}

	// Changed (or half written): written anew on the next start.
	os.WriteFile(bin, []byte("changed"), 0o755)
	if again, err := unpackFFmpeg(cache, gz(t, "build 1")); err != nil || again != bin {
		t.Fatalf("again: %s %v", again, err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "build 1" {
		t.Errorf("a changed ffmpeg was kept: %q", b)
	}

	// Another build goes in a folder of its own, and the old one is removed.
	next, err := unpackFFmpeg(cache, gz(t, "build 2"))
	if err != nil || next == bin {
		t.Fatalf("next: %s %v", next, err)
	}
	if _, err := os.Stat(filepath.Dir(bin)); !os.IsNotExist(err) {
		t.Errorf("the old build is still there: %v", err)
	}
}
