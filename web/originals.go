package web

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// migrateOriginals renames, in each date folder (<year>/<date>), the folder
// older versions kept the whole recording in (the --archive name, usually
// "arkiv") to original/. Cut files from those versions stay in it, unused.
func (s *Server) migrateOriginals() {
	old := s.cfg.LegacyOriginals
	if old == "" || old == normalizer.OriginalDir || old == "." || old == ".." || strings.ContainsAny(old, `/\`) {
		return
	}
	var moved int
	years, _ := os.ReadDir(s.root)
	for _, y := range years {
		if !y.IsDir() || strings.HasPrefix(y.Name(), ".") {
			continue
		}
		dates, _ := os.ReadDir(filepath.Join(s.root, y.Name()))
		for _, d := range dates {
			dir := filepath.Join(s.root, y.Name(), d.Name())
			from, to := filepath.Join(dir, old), filepath.Join(dir, normalizer.OriginalDir)
			if st, err := os.Stat(from); !d.IsDir() || err != nil || !st.IsDir() || fileExists(to) {
				continue
			}
			if err := os.Rename(from, to); err != nil {
				s.cfg.Logger.Printf("could not rename %s to %s: %v", s.display(from), normalizer.OriginalDir, err)
				continue
			}
			moved++
		}
	}
	if moved > 0 {
		s.cfg.Logger.Printf("renamed %d %q folders to %q", moved, old, normalizer.OriginalDir)
	}
}
