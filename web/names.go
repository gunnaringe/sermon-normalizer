package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

const maxName = 60

// cleanName checks a sermon's name, which becomes part of a folder and file
// names: spaces are tidied, and anything a file system or a path would
// choke on is refused. "" is fine (a sermon without a name).
func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case utf8.RuneCountInString(name) > maxName:
		return "", fmt.Errorf("the name is too long (at most %d characters)", maxName)
	case strings.HasPrefix(name, "."):
		return "", errors.New("the name cannot start with a dot")
	case strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) }):
		return "", errors.New(`the name cannot contain / \ : * ? " < > | or control characters`)
	}
	return name, nil
}

// folderName is the folder (and file prefix) of a sermon: <date> or <date> <name>.
func folderName(date, name string) string {
	if name == "" {
		return date
	}
	return date + " " + name
}

type renameRequest struct {
	Date string `json:"date"`
	Name string `json:"name"`
}

// handleRename gives a sermon another date or name: the folder, and the
// files in it that are named after it (<folder>_raw.flac, <folder>.mp3 ...,
// also in the archive), are renamed. Outputs keep the tags they were made with.
func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	var q renameRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name, err := cleanName(q.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	dst, status, err := s.renameTarget(abs, q.Date, name)
	if err != nil {
		writeError(w, status, err)
		return
	}
	if err := s.rename(abs, dst); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": s.relPath(dst)})
}

// renameTarget is where the sermon in folder goes, checking that it can.
func (s *Server) renameTarget(folder, date, name string) (string, int, error) {
	if _, _, ok := parseFolder(filepath.Base(folder)); !ok {
		return "", http.StatusBadRequest, errors.New("this is not a date folder")
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", http.StatusBadRequest, fmt.Errorf("invalid date %q (use YYYY-MM-DD)", date)
	}
	layout, err := s.resolve(d.Format(s.cfg.Layout))
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	dst, err := s.resolve(s.relPath(filepath.Dir(layout)) + "/" + folderName(date, name))
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	switch {
	case dst == folder:
		return "", http.StatusBadRequest, errors.New("nothing to change")
	case fileExists(dst):
		return "", http.StatusConflict, fmt.Errorf("%s already exists", filepath.Base(dst))
	case s.jobs.active(s.relPath(folder)) != nil:
		return "", http.StatusConflict, errors.New("this sermon is being processed; try again when it is done")
	}
	if paths, err := normalizer.NewPaths(folder, s.cfg.Archive); err == nil {
		if raw := paths.Sources().Raw; raw != "" && s.analyses.stage(raw) != "" {
			return "", http.StatusConflict, errors.New("the recording is still being converted or prepared; try again in a moment")
		}
	}
	return dst, 0, nil
}

// rename moves the folder and renames what is named after it.
func (s *Server) rename(src, dst string) error {
	oldBase, newBase := filepath.Base(src), filepath.Base(dst)
	paths, err := normalizer.NewPaths(src, s.cfg.Archive)
	if err != nil {
		return err
	}
	archive := paths.Archive // before the move; inside the folder when relative
	if rel, err := filepath.Rel(src, archive); err == nil && !strings.HasPrefix(rel, "..") {
		archive = filepath.Join(dst, rel)
	}
	var oldKey string
	if raw := paths.Sources().Raw; raw != "" {
		oldKey, _ = cacheKey(raw)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	dirs := []string{dst}
	if archive != "" {
		dirs = append(dirs, archive) // the archive may be outside the folder: its files are renamed too
	}
	for _, dir := range dirs {
		des, _ := os.ReadDir(dir)
		for _, de := range des {
			rest, ok := strings.CutPrefix(de.Name(), oldBase)
			if !ok || de.IsDir() || rest == "" || (rest[0] != '_' && rest[0] != '.') {
				continue
			}
			if err := os.Rename(filepath.Join(dir, de.Name()), filepath.Join(dir, newBase+rest)); err != nil {
				return fmt.Errorf("the folder was renamed, but %s was not: %w", de.Name(), err)
			}
		}
	}
	if np, err := normalizer.NewPaths(dst, s.cfg.Archive); err == nil && oldKey != "" {
		if raw := np.Sources().Raw; raw != "" {
			s.analyses.carry(oldKey, raw)
		}
	}
	s.cfg.Logger.Printf("renamed %s to %s", s.display(src), s.display(dst))
	return nil
}
