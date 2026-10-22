package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Deleting a recording moves its date folder into the church's trash, <root>/.trash/<id>/, where it
// waits for DefaultTrashDays (--trash-days) before it is removed for good. The trash is hidden,
// so the recordings list, the paths the API takes and the inbox never see into it; it is
// reached only by id, through the trash routes. A folder in the trash holds a trash.json: where it came from and when.

// DefaultTrashDays is how long a deleted recording is kept.
const DefaultTrashDays = 30

const (
	trashName = ".trash"
	trashNote = "trash.json"
)

// trashID is <UTC time>_<the folder's name>: sorts by time, and is safe in a path.
var reTrashID = regexp.MustCompile(`^\d{8}-\d{6}_[^/\\]+$`)

// trashRecord is trash.json in a folder in the trash.
type trashRecord struct {
	From string    `json:"from"` // the folder's path under the root
	At   time.Time `json:"at"`
}

func (s *Server) trashDays() int {
	if s.cfg.TrashDays > 0 {
		return s.cfg.TrashDays
	}
	return DefaultTrashDays
}

func (s *Server) trashRoot() string { return filepath.Join(s.root, trashName) }

// trashPath is the folder of a trash id, or an error if the id is not one.
func (s *Server) trashPath(id string) (string, error) {
	if !reTrashID.MatchString(id) || id == "." || id == ".." {
		return "", errors.New("not in the trash")
	}
	p := filepath.Join(s.trashRoot(), id)
	if st, err := os.Lstat(p); err != nil || !st.IsDir() {
		return "", errors.New("not in the trash")
	}
	return p, nil
}

func readTrashRecord(dir string) (trashRecord, error) {
	var rec trashRecord
	b, err := os.ReadFile(filepath.Join(dir, trashNote))
	if err == nil {
		err = json.Unmarshal(b, &rec)
	}
	return rec, err
}

// handleDelete moves a recording to the trash: POST api/recordings/delete?path=<date folder>.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	if !s.entry(abs).Date || abs == s.root {
		writeError(w, http.StatusNotFound, errors.New("no recording there"))
		return
	}
	rel := s.relPath(abs)
	if s.jobs.active(rel) != nil {
		writeError(w, http.StatusConflict, errors.New("this recording is being processed; try again when it is done"))
		return
	}
	if paths, err := normalizer.NewPaths(abs); err == nil {
		if raw := paths.Source(); raw != "" && s.analyses.stage(raw) != "" {
			writeError(w, http.StatusConflict, errors.New("the recording is still being converted or prepared; try again in a moment"))
			return
		}
	}
	now := time.Now().UTC()
	id := now.Format("20060102-150405") + "_" + filepath.Base(abs)
	dst := filepath.Join(s.trashRoot(), id)
	if err := os.MkdirAll(s.trashRoot(), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := normalizer.Rename(abs, dst); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not move it to the trash: %w", err))
		return
	}
	note, _ := json.Marshal(trashRecord{From: rel, At: now})
	if err := os.WriteFile(filepath.Join(dst, trashNote), note, 0o644); err != nil {
		s.cfg.Logger.Printf("trash: no note in %s: %v", dst, err)
	}
	if parent := filepath.Dir(abs); parent != s.root {
		normalizer.Remove(parent) // the year, if it is empty now
	}
	s.cfg.Logger.Printf("trash: %s moved to the trash as %s", rel, id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "days": s.trashDays()})
}

type trashItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	From      string    `json:"from"`
	DeletedAt time.Time `json:"deletedAt"`
	Size      int64     `json:"size"`
	DaysLeft  int       `json:"daysLeft"`
}

func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && path != filepath.Join(dir, trashNote) {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// trashItems is what is in the trash, the latest first.
func (s *Server) trashItems(now time.Time) []trashItem {
	items := []trashItem{}
	des, _ := os.ReadDir(s.trashRoot())
	for _, de := range des {
		if !de.IsDir() || !reTrashID.MatchString(de.Name()) {
			continue
		}
		dir := filepath.Join(s.trashRoot(), de.Name())
		rec, err := readTrashRecord(dir)
		if err != nil { // no note (or a bad one): the folder's own time
			info, ierr := de.Info()
			if ierr != nil {
				continue
			}
			rec = trashRecord{From: de.Name()[16:], At: info.ModTime()}
		}
		left := int((time.Duration(s.trashDays())*24*time.Hour-now.Sub(rec.At)).Hours()/24 + 0.999)
		items = append(items, trashItem{ID: de.Name(), Name: filepath.Base(rec.From), From: rec.From, DeletedAt: rec.At, Size: dirSize(dir), DaysLeft: max(left, 0)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	return items
}

// handleTrash lists the trash: GET api/trash.
func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.trashItems(time.Now()), "days": s.trashDays()})
}

// handleRestore puts a recording back where it was: POST api/trash/restore?id=.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	dir, err := s.trashPath(r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	rec, err := readTrashRecord(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("it cannot be restored: its note is missing"))
		return
	}
	dst, err := s.resolve(rec.From)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if fileExists(dst) {
		writeError(w, http.StatusConflict, fmt.Errorf("%s exists again: rename or delete that recording first", filepath.Base(dst)))
		return
	}
	normalizer.Remove(filepath.Join(dir, trashNote))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
		err = normalizer.Rename(dir, dst)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not restore it: %w", err))
		return
	}
	s.cfg.Logger.Printf("trash: restored %s", rec.From)
	writeJSON(w, http.StatusOK, map[string]any{"path": rec.From})
}

// handleTrashRemove removes one for good: POST api/trash/remove?id=.
func (s *Server) handleTrashRemove(w http.ResponseWriter, r *http.Request) {
	dir, err := s.trashPath(r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := normalizer.RemoveAll(dir); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.cfg.Logger.Printf("trash: removed %s for good", filepath.Base(dir))
	writeJSON(w, http.StatusOK, map[string]any{})
}

// purgeTrash removes what has been in the trash longer than --trash-days.
func (s *Server) purgeTrash(now time.Time) {
	for _, it := range s.trashItems(now) {
		if it.DaysLeft > 0 {
			continue
		}
		if err := normalizer.RemoveAll(filepath.Join(s.trashRoot(), it.ID)); err != nil {
			s.cfg.Logger.Printf("trash: could not remove %s: %v", it.ID, err)
			continue
		}
		s.cfg.Logger.Printf("trash: removed %s, in the trash since %s", it.ID, it.DeletedAt.Format(time.DateOnly))
	}
}
