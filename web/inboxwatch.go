package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// inboxWatcher takes recordings that appear in the inbox by any means (a
// synced folder, a file share, a copy, the API) once they have stopped
// changing, and moves them into their date folder (see ingest). There the
// folder list shows them while a WAV is converted to FLAC and the waveform
// and preview are prepared.
type inboxWatcher struct {
	s      *Server
	every  time.Duration
	settle time.Duration
	seen   map[string]fileStamp // name -> last size and mtime
	failed map[string]fileStamp // name -> stamp when ingest failed
}

type fileStamp struct {
	size int64
	mod  time.Time
}

func newInboxWatcher(s *Server) *inboxWatcher {
	w := &inboxWatcher{s: s, every: s.cfg.InboxEvery, settle: s.cfg.InboxSettle, seen: map[string]fileStamp{}, failed: map[string]fileStamp{}}
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

// scan ingests files unchanged since the last scan and for at least the
// settle time. A file that failed is left alone until it changes.
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
		if w.failed[name] == stamp {
			continue
		}
		if err := w.s.ingest(name); err != nil {
			w.failed[name] = stamp
			w.s.cfg.Logger.Printf("inbox: %s: %v", name, err)
		}
	}
	w.seen = now
}

// ingest moves a recording from the inbox into its date folder as
// <date>_raw.<ext> and starts the FLAC conversion and the waveform there. The
// date is guessed (see suggestDate). If the date already has a raw recording
// the folder becomes "<date> 2", "<date> 3"... until a named sermon renames it.
// The inbox may be on another disk: MoveFile then copies, verifies and
// deletes, so keep them on one if recordings are long.
func (s *Server) ingest(name string) error {
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	src := filepath.Join(s.cfg.Inbox, name)
	info, err := os.Stat(src)
	if err != nil {
		return nil // taken meanwhile
	}
	date, _ := s.suggestDate(s.ctx, src, info.ModTime())
	var folder string
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = strconv.Itoa(n)
		}
		var status int
		if folder, status, err = s.newRawFolder(date, suffix); err == nil {
			break
		}
		if status != http.StatusConflict || n >= 20 {
			return err
		}
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(folder, filepath.Base(folder)+"_raw"+strings.ToLower(filepath.Ext(name)))
	if err := normalizer.MoveFile(src, dst); err != nil {
		return err
	}
	s.markImported(folder, name)
	s.cfg.Logger.Printf("inbox: %s is now %s", name, s.relPath(dst))
	s.analyses.state(dst, true)
	return nil
}
