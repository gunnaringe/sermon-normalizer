package normalizer

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func firstExisting(paths ...string) string {
	for _, s := range paths {
		if exists(s) {
			return s
		}
	}
	return ""
}

// MoveFile renames src to dst, falling back to copy and delete when they are
// on different file systems (e.g. an archive on another disk).
func MoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	tmp := dst + ".partial"
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	// The source may be the only copy of the recording: verify before deleting it.
	same, err := sameContent(src, tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if !same {
		os.Remove(tmp)
		return fmt.Errorf("copy of %s differs from the original; the original is kept", filepath.Base(src))
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func sameContent(a, b string) (bool, error) {
	ha, err := fileHash(a)
	if err != nil {
		return false, err
	}
	hb, err := fileHash(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func fileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
