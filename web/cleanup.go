package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// leftoverAge is how long a half-written file may sit untouched before it is
// taken for left behind (a stopped upload, a crash in the middle of an
// encode) and removed. Files that are being written are touched all the time.
const leftoverAge = 24 * time.Hour

// leftover says whether a file name is one of the half-written files the app
// makes: ffmpeg's and the state files' .partial, an upload's hidden .upload,
// a share upload's .share-<id>.partial.
func leftover(name string) bool {
	return strings.Contains(name, ".partial") || (strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".upload"))
}

// sweepLeftovers removes half-written files older than leftoverAge from the
// church's folders, inbox and cache, and a date folder an abandoned upload
// leaves empty.
func (s *Server) sweepLeftovers(now time.Time) {
	for _, dir := range []string{s.root, s.cfg.Inbox, s.cfg.CacheDir} {
		if dir == "" {
			continue
		}
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() || !leftover(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil || now.Sub(info.ModTime()) < leftoverAge {
				return nil
			}
			if os.Remove(path) != nil {
				return nil
			}
			s.cfg.Logger.Printf("cleanup: removed %s, untouched since %s", path, info.ModTime().Format(time.DateTime))
			if parent := filepath.Dir(path); parent != dir && strings.HasSuffix(d.Name(), ".upload") {
				os.Remove(parent) // only if it is empty now
			}
			return nil
		})
	}
}

// keepSweeping runs sweepLeftovers at start and then every hour, until the
// server closes.
func (s *Server) keepSweeping() {
	defer s.wg.Done()
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for now := time.Now(); ; {
		s.sweepLeftovers(now)
		select {
		case <-s.ctx.Done():
			return
		case now = <-tick.C:
		}
	}
}
