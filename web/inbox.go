package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// inboxFile is a recording waiting in the inbox.
type inboxFile struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	Modified      time.Time `json:"modified"`
	SuggestedDate string    `json:"suggestedDate"`
	DateSource    string    `json:"dateSource"` // name, metadata, modified
	State         string    `json:"state"`      // waiting (still arriving), converting, preparing, ready, error
	Error         string    `json:"error,omitempty"`
}

var errNoInbox = errors.New("no inbox folder configured")

// inboxName checks an inbox file name: a plain .wav/.flac file, not hidden
// and not one being written.
func inboxName(name string) (string, error) {
	base := filepath.Base(name)
	if base != name || base == "" || strings.HasPrefix(base, ".") || strings.ContainsAny(base, `/\`) || normalizer.IsPartial(base) {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".wav", ".flac":
		return base, nil
	}
	return "", fmt.Errorf("%s: only .wav and .flac recordings are accepted", base)
}

var (
	reNameDate = regexp.MustCompile(`(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)`)
	reMetaDate = regexp.MustCompile(`(?mi)^\s+(?:date|creation_time|origination_date)\s*:\s*(\d{4}-\d{2}-\d{2})`)
)

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// dateFromName finds a date like 2026-10-04 or 20261004 in a file name.
func dateFromName(name string) string {
	for _, m := range reNameDate.FindAllStringSubmatch(name, -1) {
		if d := m[1] + "-" + m[2] + "-" + m[3]; validDate(d) {
			return d
		}
	}
	return ""
}

// dateFromMetadata reads a recording date from the file's tags (BWF
// origination date in WAV, DATE or creation_time in FLAC).
func dateFromMetadata(stderr string) string {
	for _, m := range reMetaDate.FindAllStringSubmatch(stderr, -1) {
		if validDate(m[1]) {
			return m[1]
		}
	}
	return ""
}

// suggestDate guesses when a recording was made: from its name, its tags,
// or its modification time (the client's, for uploads).
// Tag reads are remembered by cacheKey, as the inbox page asks every few seconds.
func (s *Server) suggestDate(ctx context.Context, path string, mod time.Time) (string, string) {
	if d := dateFromName(filepath.Base(path)); d != "" {
		return d, "name"
	}
	key, _ := cacheKey(path)
	s.tagsMu.Lock()
	d, ok := s.tagDates[key]
	s.tagsMu.Unlock()
	if !ok {
		if res, err := s.cfg.FFmpeg.Run(ctx, "-i", path, "-t", "0", "-f", "null", "-"); err == nil {
			d = dateFromMetadata(res.Stderr)
		}
		if key != "" && ctx.Err() == nil {
			s.tagsMu.Lock()
			s.tagDates[key] = d
			s.tagsMu.Unlock()
		}
	}
	if d != "" {
		return d, "metadata"
	}
	return mod.Format("2006-01-02"), "modified"
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Inbox == "" {
		writeError(w, http.StatusNotFound, errNoInbox)
		return
	}
	des, err := os.ReadDir(s.cfg.Inbox)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	files := []inboxFile{}
	for _, de := range des {
		name, err := inboxName(de.Name())
		info, ierr := de.Info()
		if err != nil || ierr != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(s.cfg.Inbox, name)
		f := inboxFile{Name: name, Size: info.Size(), Modified: info.ModTime()}
		f.SuggestedDate, f.DateSource = s.suggestDate(r.Context(), path, info.ModTime())
		switch st := s.analyses.state(path, false); {
		case st.State == "done":
			f.State = "ready"
		case st.State == "error":
			f.State, f.Error = "error", st.Error
		case st.State == "running" && st.Stage == "convert":
			f.State = "converting"
		case st.State == "running":
			f.State = "preparing"
		default:
			f.State = "waiting"
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "layout": s.cfg.Layout})
}

type importRequest struct {
	Name string `json:"name"`
	Date string `json:"date"`
}

// newRawFolder is the date folder for a new raw recording of date (made
// after the layout, not created here). It fails if the folder already has a
// raw recording; the status is the HTTP status for the error.
func (s *Server) newRawFolder(date string) (string, int, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", http.StatusBadRequest, fmt.Errorf("invalid date %q (use YYYY-MM-DD)", date)
	}
	folder, err := s.resolve(d.Format(s.cfg.Layout))
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	if filepath.Base(folder) != date {
		return "", http.StatusInternalServerError, fmt.Errorf("the layout %q must end in the date (2006-01-02)", s.cfg.Layout)
	}
	if !fileExists(folder) {
		return folder, 0, nil
	}
	paths, err := normalizer.NewPaths(folder, s.cfg.Archive)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	if raw := paths.Sources().Raw; raw != "" {
		return "", http.StatusConflict, fmt.Errorf("%s already has a raw recording (%s)", date, filepath.Base(raw))
	}
	return folder, 0, nil
}

// handleInboxImport moves a recording from the inbox into its date folder
// as <date>_raw.<ext>, creating the folder after the layout.
func (s *Server) handleInboxImport(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Inbox == "" {
		writeError(w, http.StatusNotFound, errNoInbox)
		return
	}
	var q importRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name, err := inboxName(q.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	src := filepath.Join(s.cfg.Inbox, name)
	if !fileExists(src) {
		writeError(w, http.StatusNotFound, fmt.Errorf("%s is not in the inbox", name))
		return
	}
	if s.analyses.converting(src) {
		writeError(w, http.StatusConflict, fmt.Errorf("%s is being converted to FLAC; import it when that is done", name))
		return
	}
	folder, status, err := s.newRawFolder(q.Date)
	if err != nil {
		writeError(w, status, err)
		return
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	dst := filepath.Join(folder, q.Date+"_raw"+strings.ToLower(filepath.Ext(name)))
	oldKey, _ := cacheKey(src)
	// The inbox may be on another disk: MoveFile copies, verifies, then
	// deletes. Keep them on one, or a long recording outlasts the request.
	if err := normalizer.MoveFile(src, dst); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.analyses.carry(oldKey, dst)
	s.cfg.Logger.Printf("inbox: imported %s as %s", name, s.relPath(dst))
	s.analyses.state(dst, true) // a WAV is converted now; else usually done already
	writeJSON(w, http.StatusOK, map[string]string{"path": s.relPath(folder)})
}
