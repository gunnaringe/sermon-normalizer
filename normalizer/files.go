package normalizer

import (
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

// moveFile renames src to dst, falling back to copy and delete when they are
// on different file systems (e.g. an archive on another disk).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	tmp := dst + ".partial"
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	ti, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if si.Size() != ti.Size() {
		os.Remove(tmp)
		return fmt.Errorf("copy of %s is incomplete", filepath.Base(src))
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Remove(src)
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
