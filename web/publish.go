package web

import (
	"os"
	"path/filepath"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// publish copies finished files of a date folder into the publish folder,
// under the same relative path (e.g. a folder Resilio Sync shares, so the
// recordings and work files stay out of it). Each file appears there whole:
// it is copied next to the original under a hidden name (the work area is not
// shared) and renamed into place, which needs both on one file system; on
// another, it is copied under a hidden name in the publish folder instead.
// A file already published with the same content is left alone.
func (s *Server) publish(folder string, files []string, logf func(format string, args ...any)) error {
	rel, err := filepath.Rel(s.root, folder)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.cfg.Publish, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, src := range files {
		base := filepath.Base(src)
		dst := filepath.Join(dir, base)
		if same, err := normalizer.SameContent(src, dst); err == nil && same {
			logf("%s is already published.", base)
			continue
		}
		if err := copyInto(src, dst, filepath.Join(filepath.Dir(src), "."+base+".publish")); err != nil {
			if err := copyInto(src, dst, filepath.Join(dir, "."+base+".partial")); err != nil {
				return err
			}
		}
		logf("Published %s.", filepath.Join(rel, base))
	}
	return nil
}

// copyInto copies src to tmp, checks it, and renames it to dst.
func copyInto(src, dst, tmp string) error {
	err := normalizer.CopyFile(src, tmp)
	if err == nil {
		var same bool
		if same, err = normalizer.SameContent(src, tmp); err == nil && !same {
			err = os.ErrInvalid
		}
	}
	if err == nil {
		if st, serr := os.Stat(src); serr == nil {
			os.Chtimes(tmp, st.ModTime(), st.ModTime())
		}
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
