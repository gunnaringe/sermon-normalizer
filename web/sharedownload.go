package web

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// A download share gives one finished sermon to people without a login: the
// same kind of code and link as an upload share, on the same public page,
// which then offers the share's formats instead of a place to drop files.
// Pressing Download counts one download and hands out a ticket: a plain
// address of the file that works for ticketFor, so the browser can download
// and resume it (Range) without the code, and without counting again.

const (
	kindUpload   = "upload"
	kindDownload = "download"
	ticketFor    = 6 * time.Hour
	// downloadWriteFor is how long one response with a file may take (the listener's own write timeout is short)
	downloadWriteFor = 3 * time.Hour
)

// downloadFormats are what a download share can give, in the order shown.
var downloadFormats = []string{"flac", "mp3", "opus"}

var downloadTypes = map[string]string{"flac": "audio/flac", "mp3": "audio/mpeg", "opus": "audio/ogg"}

// downloadTicket is a download that has been counted, and can be fetched (and resumed) until it expires.
type downloadTicket struct {
	Format  string    `json:"format"`
	IP      string    `json:"ip"`
	Expires time.Time `json:"expires"`
}

func outputPath(p normalizer.Paths, format string) string {
	for _, o := range outputsOf(p) {
		if o.format == format {
			return o.path
		}
	}
	return ""
}

// recordingIDs are the church's recordings that have an id, by it: where they are now.
// The trash is hidden, so a recording in it is not found.
func (s *Server) recordingIDs() map[string]string {
	out := map[string]string{}
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != s.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == normalizer.OriginalDir) {
			return filepath.SkipDir
		}
		if fileExists(statePath(path)) {
			if id := readState(path).ID; id != "" {
				out[id] = path
			}
			return filepath.SkipDir // a recording folder holds no other recordings
		}
		return nil
	})
	return out
}

// findRecordings is where the recordings of the church's download shares are now (only's, if not nil):
// "" for one that is not there (in the trash, or removed). A share follows its recording when it was
// renamed or given another date.
func (a *App) findRecordings(t *tenant, only *Share) map[*Share]string {
	type want struct {
		sh       *Share
		hint, id string
	}
	var wants []want
	a.shareMu.Lock()
	for _, sh := range t.shares {
		if sh.Kind == kindDownload && (only == nil || sh == only) {
			wants = append(wants, want{sh, sh.Recording, sh.RecordingID})
		}
	}
	a.shareMu.Unlock()
	s := t.srv
	found := map[*Share]string{}
	var lost []want
	for _, w := range wants {
		if abs, err := s.resolve(w.hint); err == nil && w.hint != "" && readState(abs).ID == w.id {
			found[w.sh] = abs
		} else {
			lost = append(lost, w)
		}
	}
	if len(lost) == 0 {
		return found
	}
	byID := s.recordingIDs()
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	changed := false
	for _, w := range lost {
		abs := byID[w.id]
		found[w.sh] = abs
		if abs != "" && w.sh.Recording == w.hint {
			w.sh.Recording = s.relPath(abs)
			changed = true
		}
	}
	if changed {
		if err := a.saveShares(t); err != nil {
			a.cfg.Base.Logger.Printf("share: saving %s: %v", sharesFile, err)
		}
	}
	return found
}

// createDownloadShare makes a download share of a recording's finished files: POST api/shares with kind "download".
func (a *App) createDownloadShare(w http.ResponseWriter, t *tenant, label, rec string, formats []string, maxDownloads int, expires *time.Time) {
	s := t.srv
	abs, err := s.resolve(rec)
	if err != nil || rec == "" || !s.entry(abs).Date {
		writeError(w, http.StatusNotFound, errors.New("no recording there"))
		return
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var picked []string
	for _, f := range downloadFormats {
		if slices.Contains(formats, f) {
			picked = append(picked, f)
		}
	}
	err = checkLabel(label)
	switch {
	case err != nil:
	case len(picked) == 0 || len(picked) != len(formats):
		err = fmt.Errorf("choose formats from %s", strings.Join(downloadFormats, ", "))
	case maxDownloads < 0:
		err = errors.New("limits cannot be negative")
	case expires != nil && !expires.After(time.Now()):
		err = errors.New("the expiry is in the past")
	case slices.ContainsFunc(picked, func(f string) bool { return !fileExists(outputPath(paths, f)) }):
		err = errors.New("a chosen format has not been made yet")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	st, err := s.updateState(abs, func(st *folderState) error {
		if st.ID == "" {
			st.ID = randomID(8)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.addShare(w, t, &Share{Kind: kindDownload, Recording: s.relPath(abs), RecordingID: st.ID, Formats: picked, MaxDownloads: maxDownloads}, label, expires)
}

// uploadShareFor and downloadShareFor are shareFor for the one kind of share.
func (a *App) uploadShareFor(w http.ResponseWriter, r *http.Request) (*tenant, *Share, string, bool) {
	t, sh, ip, ok := a.shareFor(w, r)
	if ok && sh.Kind == kindDownload {
		writeError(w, http.StatusForbidden, errors.New("this code is for downloading, not for sending files"))
		return nil, nil, ip, false
	}
	return t, sh, ip, ok
}

func (a *App) downloadShareFor(w http.ResponseWriter, r *http.Request) (*tenant, *Share, string, bool) {
	t, sh, ip, ok := a.shareFor(w, r)
	if ok && sh.Kind != kindDownload {
		writeError(w, http.StatusForbidden, errors.New("this code is for sending files, not for downloading"))
		return nil, nil, ip, false
	}
	return t, sh, ip, ok
}

// downloadInfo is what the public page shows of a download share: GET api/share with its code.
func (a *App) downloadInfo(w http.ResponseWriter, t *tenant, sh *Share) {
	abs := a.findRecordings(t, sh)[sh]
	a.shareMu.Lock()
	state := sh.state(time.Now())
	info := map[string]any{"kind": kindDownload, "label": sh.Label, "church": t.info().Name, "expires": sh.Expires,
		"logo": t.logoFile() != ""}
	if sh.MaxDownloads > 0 {
		info["downloadsLeft"] = max(0, sh.MaxDownloads-sh.Count)
	}
	a.shareMu.Unlock()
	files := []map[string]any{}
	if abs == "" {
		if state == "" {
			state = "missing"
		}
	} else if paths, err := normalizer.NewPaths(abs); err == nil {
		for _, f := range sh.Formats {
			if st, err := os.Stat(outputPath(paths, f)); err == nil {
				files = append(files, map[string]any{"format": f, "size": st.Size()})
			}
		}
		title, artist := filepath.Base(abs), ""
		if tags, _, err := t.srv.defaultTags(abs); err == nil {
			if tags.Title != "" {
				title = tags.Title
			}
			artist = tags.Artist
		}
		info["title"], info["artist"] = title, artist
		if d, _, ok := parseFolder(filepath.Base(abs)); ok {
			info["date"] = d.Format("2006-01-02")
		}
	}
	info["state"], info["files"] = state, files
	writeJSON(w, http.StatusOK, info)
}

// handleShareDownloadStart counts a download and returns the address to fetch it from:
// POST api/share/downloads {format} → {url}. Pressing again for the same file, from the same
// address, while that download can still be resumed counts nothing.
func (a *App) handleShareDownloadStart(w http.ResponseWriter, r *http.Request) {
	t, sh, ip, ok := a.downloadShareFor(w, r)
	if !ok {
		return
	}
	var q struct {
		Format string `json:"format"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !slices.Contains(sh.Formats, q.Format) {
		writeError(w, http.StatusBadRequest, errors.New("this code does not give that format"))
		return
	}
	abs := a.findRecordings(t, sh)[sh]
	if abs == "" {
		writeError(w, http.StatusGone, errors.New("this recording is no longer available"))
		return
	}
	paths, err := normalizer.NewPaths(abs)
	var st os.FileInfo
	if err == nil {
		st, err = os.Stat(outputPath(paths, q.Format))
	}
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("this file is not there now; ask the church that gave you the code"))
		return
	}
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	now := time.Now()
	for id, tk := range sh.Tickets {
		if tk.IP == ip && tk.Format == q.Format && now.Before(tk.Expires) {
			writeJSON(w, http.StatusOK, map[string]string{"url": "api/share/downloads/" + id})
			return
		}
	}
	if state := sh.state(now); state != "" {
		writeError(w, http.StatusForbidden, fmt.Errorf("this code gives no more downloads (%s)", state))
		return
	}
	id := randomID(16)
	if sh.Tickets == nil {
		sh.Tickets = map[string]*downloadTicket{}
	}
	sh.Tickets[id] = &downloadTicket{Format: q.Format, IP: ip, Expires: now.Add(ticketFor).UTC()}
	sh.Count++
	log := sh.Log
	sh.Log = append(sh.Log, ShareUpload{Name: st.Name(), Size: st.Size(), IP: ip, Time: now.UTC()})
	if len(sh.Log) > shareLogMax {
		sh.Log = sh.Log[len(sh.Log)-shareLogMax:]
	}
	if err := a.saveShares(t); err != nil {
		delete(sh.Tickets, id)
		sh.Count--
		sh.Log = log
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("share: %s downloads %s with share %s", ip, st.Name(), sh.ID)
	writeJSON(w, http.StatusCreated, map[string]string{"url": "api/share/downloads/" + id})
}

// findTicket is the church, share and ticket of a ticket id, or nils.
func (a *App) findTicket(id string) (*tenant, *Share, *downloadTicket) {
	a.mu.Lock()
	tenants := slices.Clone(a.tenants)
	a.mu.Unlock()
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	for _, t := range tenants {
		for _, sh := range t.shares {
			if tk := sh.Tickets[id]; tk != nil {
				c := *tk
				return t, sh, &c
			}
		}
	}
	return nil, nil, nil
}

// handleShareDownloadFile sends the file of a counted download, with Range for resuming:
// GET api/share/downloads/{id}. The ticket is the key; no code is needed.
func (a *App) handleShareDownloadFile(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if wait := a.share.limits.blocked(ip); wait > 0 {
		tooMany(w, wait, "too many wrong codes")
		return
	}
	id := r.PathValue("id")
	t, sh, tk := a.findTicket(id)
	if tk == nil {
		a.share.limits.failed(ip)
		writeError(w, http.StatusNotFound, errors.New("no such download"))
		return
	}
	if !time.Now().Before(tk.Expires) {
		writeError(w, http.StatusGone, errors.New("this download has ended: open the link again"))
		return
	}
	abs := a.findRecordings(t, sh)[sh]
	if abs == "" {
		writeError(w, http.StatusGone, errors.New("this recording is no longer available"))
		return
	}
	paths, err := normalizer.NewPaths(abs)
	var f *os.File
	if err == nil {
		f, err = os.Open(outputPath(paths, tk.Format))
	}
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("this file is not there now; ask the church that gave you the code"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, errors.New("this file is not there now; ask the church that gave you the code"))
		return
	}
	a.shareMu.Lock()
	a.share.sending[sh.ID]++
	a.shareMu.Unlock()
	defer func() {
		a.shareMu.Lock()
		if a.share.sending[sh.ID]--; a.share.sending[sh.ID] <= 0 {
			delete(a.share.sending, sh.ID)
		}
		a.shareMu.Unlock()
	}()
	// The listener's write timeout is for short requests; a sermon to a slow phone takes longer.
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(downloadWriteFor))
	w.Header().Set("Content-Type", downloadTypes[tk.Format])
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name()}))
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}
