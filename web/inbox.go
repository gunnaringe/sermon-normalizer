package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
}

var errNoInbox = errors.New("no inbox folder configured")

// inboxName checks an inbox file name: a plain .wav/.flac file, not hidden.
func inboxName(name string) (string, error) {
	base := filepath.Base(name)
	if base != name || base == "" || strings.HasPrefix(base, ".") || strings.ContainsAny(base, `/\`) {
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
func (s *Server) suggestDate(ctx context.Context, path string, mod time.Time) (string, string) {
	if d := dateFromName(filepath.Base(path)); d != "" {
		return d, "name"
	}
	if res, err := s.cfg.FFmpeg.Run(ctx, "-i", path, "-t", "0", "-f", "null", "-"); err == nil {
		if d := dateFromMetadata(res.Stderr); d != "" {
			return d, "metadata"
		}
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
		f := inboxFile{Name: name, Size: info.Size(), Modified: info.ModTime()}
		f.SuggestedDate, f.DateSource = s.suggestDate(r.Context(), filepath.Join(s.cfg.Inbox, name), info.ModTime())
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "layout": s.cfg.Layout})
}

// handleInboxUpload streams a multipart upload into the inbox. The file is
// written under a hidden .partial name and renamed only when complete; an
// aborted upload leaves nothing. A "lastModified" field (ms since 1970,
// from the browser's File) before the file sets its modification time,
// which would otherwise be the upload time.
func (s *Server) handleInboxUpload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Inbox == "" {
		writeError(w, http.StatusNotFound, errNoInbox)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var modified time.Time
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, http.StatusBadRequest, errors.New("no file in the upload"))
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		switch part.FormName() {
		case "lastModified":
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			if ms, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && ms > 0 {
				modified = time.UnixMilli(ms)
			}
		case "file":
			name, err := inboxName(part.FileName())
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			dst := filepath.Join(s.cfg.Inbox, name)
			if fileExists(dst) {
				writeError(w, http.StatusConflict, fmt.Errorf("%s is already in the inbox", name))
				return
			}
			if err := receive(part, dst, modified); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			s.cfg.Logger.Printf("inbox: received %s", name)
			info, _ := os.Stat(dst)
			f := inboxFile{Name: name, Size: info.Size(), Modified: info.ModTime()}
			f.SuggestedDate, f.DateSource = s.suggestDate(r.Context(), dst, info.ModTime())
			writeJSON(w, http.StatusCreated, f)
			return
		}
	}
}

func receive(src io.Reader, dst string, modified time.Time) error {
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".partial")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, src)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && !modified.IsZero() {
		err = os.Chtimes(tmp, modified, modified)
	}
	if err == nil && fileExists(dst) {
		err = fmt.Errorf("%s appeared in the inbox meanwhile", filepath.Base(dst))
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

type importRequest struct {
	Name string `json:"name"`
	Date string `json:"date"`
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
	date, err := time.Parse("2006-01-02", q.Date)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid date %q (use YYYY-MM-DD)", q.Date))
		return
	}
	src := filepath.Join(s.cfg.Inbox, name)
	if !fileExists(src) {
		writeError(w, http.StatusNotFound, fmt.Errorf("%s is not in the inbox", name))
		return
	}
	folder, err := s.resolve(date.Format(s.cfg.Layout))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if filepath.Base(folder) != q.Date {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("the layout %q must end in the date (2006-01-02)", s.cfg.Layout))
		return
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	paths, err := normalizer.NewPaths(folder, s.cfg.Archive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if raw := paths.Sources().Raw; raw != "" {
		writeError(w, http.StatusConflict, fmt.Errorf("%s already has a raw recording (%s)", q.Date, filepath.Base(raw)))
		return
	}
	dst := filepath.Join(folder, q.Date+"_raw"+strings.ToLower(filepath.Ext(name)))
	// The inbox may be on another disk: MoveFile copies, verifies, then deletes.
	if err := normalizer.MoveFile(src, dst); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.cfg.Logger.Printf("inbox: imported %s as %s", name, s.relPath(dst))
	writeJSON(w, http.StatusOK, map[string]string{"path": s.relPath(folder)})
}
