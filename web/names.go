package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gunnaringe/sermon-studio/normalizer"
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
	case strings.HasSuffix(name, "."):
		return "", errors.New("the name cannot end with a dot") // Windows drops it
	case strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) }):
		return "", errors.New(`the name cannot contain / \ : * ? " < > | or control characters`)
	}
	return name, nil
}

// windowsDevice reports the names Windows keeps for devices (CON, NUL, COM1 ...), also with an extension
// (nul.wav): no file or folder can have them there.
func windowsDevice(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	switch b := strings.ToUpper(strings.TrimRight(base, " ")); b {
	case "CON", "PRN", "AUX", "NUL":
		return true
	default:
		return len(b) == 4 && (strings.HasPrefix(b, "COM") || strings.HasPrefix(b, "LPT")) && b[3] >= '0' && b[3] <= '9'
	}
}

// maxWindowsPath is the longest path most Windows programs open, ffmpeg among them (MAX_PATH less the closing
// zero). Go's own file functions take longer ones.
const maxWindowsPath = 259

// fitsWindows reports whether path is short enough for Windows programs; always true elsewhere.
func fitsWindows(path string) bool {
	return runtime.GOOS != "windows" || len(utf16.Encode([]rune(path))) <= maxWindowsPath
}

// folderFits refuses a sermon folder whose files would not fit (fitsWindows). The longest is the recording
// kept in original/ while it is written: <folder>_raw.aiff.partial.
func folderFits(folder string) error {
	if fitsWindows(filepath.Join(folder, "original", filepath.Base(folder)+"_raw.aiff.partial")) {
		return nil
	}
	return fmt.Errorf("the path of %s would be too long for Windows: use a shorter name, or a data folder nearer the top of the disk", filepath.Base(folder))
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
// also in original/), are renamed. Outputs keep the tags they were made with.
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
	case folderFits(dst) != nil:
		return "", http.StatusBadRequest, folderFits(dst)
	case fileExists(dst):
		return "", http.StatusConflict, fmt.Errorf("%s already exists", filepath.Base(dst))
	case s.jobs.active(s.relPath(folder)) != nil:
		return "", http.StatusConflict, errors.New("this sermon is being processed; try again when it is done")
	}
	if paths, err := normalizer.NewPaths(folder); err == nil {
		if raw := paths.Source(); raw != "" && s.analyses.stage(raw) != "" {
			return "", http.StatusConflict, errors.New("the recording is still being converted or prepared; try again in a moment")
		}
	}
	return dst, 0, nil
}

// rename moves the folder and renames what is named after it.
func (s *Server) rename(src, dst string) error {
	oldBase, newBase := filepath.Base(src), filepath.Base(dst)
	paths, err := normalizer.NewPaths(src)
	if err != nil {
		return err
	}
	var oldKey string
	if raw := paths.Source(); raw != "" {
		oldKey, _ = cacheKey(raw)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := normalizer.Rename(src, dst); err != nil {
		return err
	}
	for _, dir := range []string{dst, filepath.Join(dst, normalizer.OriginalDir)} {
		des, _ := os.ReadDir(dir)
		for _, de := range des {
			rest, ok := strings.CutPrefix(de.Name(), oldBase)
			if !ok || de.IsDir() || rest == "" || (rest[0] != '_' && rest[0] != '.') {
				continue
			}
			if err := normalizer.Rename(filepath.Join(dir, de.Name()), filepath.Join(dir, newBase+rest)); err != nil {
				return fmt.Errorf("the folder was renamed, but %s was not: %w", de.Name(), err)
			}
		}
	}
	if np, err := normalizer.NewPaths(dst); err == nil && oldKey != "" {
		if raw := np.Source(); raw != "" {
			s.analyses.carry(oldKey, raw)
		}
	}
	s.cfg.Logger.Printf("renamed %s to %s", s.display(src), s.display(dst))
	return nil
}
