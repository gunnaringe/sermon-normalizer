package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// inboxWatcher takes recordings that appear in the inbox by any means (a
// synced folder, a file share, a copy, the API) once they have stopped
// changing: a WAV is converted to FLAC, and the waveform and preview are
// prepared, so the recording is ready to import and cut.
type inboxWatcher struct {
	s      *Server
	every  time.Duration
	settle time.Duration
	seen   map[string]fileStamp // name -> last size and mtime
}

type fileStamp struct {
	size int64
	mod  time.Time
}

func newInboxWatcher(s *Server) *inboxWatcher {
	w := &inboxWatcher{s: s, every: s.cfg.InboxEvery, settle: s.cfg.InboxSettle, seen: map[string]fileStamp{}}
	if w.every <= 0 {
		w.every = 10 * time.Second
	}
	if w.settle <= 0 {
		w.settle = 30 * time.Second
	}
	return w
}

func (w *inboxWatcher) watch() {
	defer w.s.wg.Done()
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		w.scan()
		select {
		case <-w.s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// scan starts the work for files unchanged since the last scan and for at
// least the settle time. A file that failed once is left alone until it
// changes (opening it in the UI tries again).
func (w *inboxWatcher) scan() {
	des, err := os.ReadDir(w.s.cfg.Inbox)
	if err != nil {
		return
	}
	now := map[string]fileStamp{}
	for _, de := range des {
		name, err := inboxName(de.Name())
		info, ierr := de.Info()
		if err != nil || ierr != nil || !info.Mode().IsRegular() {
			continue
		}
		stamp := fileStamp{info.Size(), info.ModTime()}
		now[name] = stamp
		if w.seen[name] != stamp || time.Since(stamp.mod) < w.settle {
			continue
		}
		path := filepath.Join(w.s.cfg.Inbox, name)
		if w.s.analyses.state(path, false).State == "none" {
			w.s.analyses.state(path, true)
		}
	}
	w.seen = now
}

// handleInboxPut stores a recording in the inbox:
//
//	PUT api/inbox/files/<name>[?modified=<ms since 1970>]
//
// with the file as the body, streamed (no size limit; meant for other
// services, e.g. one forwarding uploads from a file-sharing app). It is
// written under a hidden name and renamed when complete, then converted and
// prepared at once. An existing name is refused with 409, so a sender can
// tell it was already delivered.
func (s *Server) handleInboxPut(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Inbox == "" {
		writeError(w, http.StatusNotFound, errNoInbox)
		return
	}
	name, err := inboxName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	dst := filepath.Join(s.cfg.Inbox, name)
	if fileExists(dst) {
		writeError(w, http.StatusConflict, fmt.Errorf("%s is already in the inbox", name))
		return
	}
	tmp := filepath.Join(s.cfg.Inbox, "."+name+".partial")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			writeError(w, http.StatusConflict, fmt.Errorf("%s is being received already", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_, err = io.Copy(f, r.Body)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if ms, perr := strconv.ParseInt(r.URL.Query().Get("modified"), 10, 64); err == nil && perr == nil && ms > 0 {
		t := time.UnixMilli(ms)
		err = os.Chtimes(tmp, t, t)
	}
	if err == nil && fileExists(dst) {
		err = fmt.Errorf("%s appeared in the inbox meanwhile", name)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.cfg.Logger.Printf("inbox: received %s", name)
	s.analyses.state(dst, true)
	writeJSON(w, http.StatusCreated, map[string]string{"name": name})
}
