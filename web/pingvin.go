package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PingvinConfig makes the server pick up recordings uploaded to Pingvin Share
// (X) through a reverse share: each new one is converted to FLAC into the
// inbox, and its waveform prepared, without anyone opening it first.
//
// Pingvin keeps a file at <data>/uploads/shares/<share id>/<file id>, and its
// name and share in its SQLite database, which is read with the sqlite3
// command, read-only: the data directory can be mounted read-only, and
// Pingvin's files are never changed.
type PingvinConfig struct {
	Data   string        // Pingvin's data directory (pingvin-share.db, uploads/)
	Prefix string        // reverse share token (or share id) the uploads go through, by prefix
	SQLite string        // the sqlite3 command; "" = "sqlite3"
	Every  time.Duration // how often to look; 0 = 30 s
}

var rePingvinPrefix = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// rePingvinID is what share and file IDs may look like before they become
// path components (Pingvin allows letters, digits, - and _ in share IDs).
var rePingvinID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type pingvinFile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  string `json:"size"`
	Share string `json:"share"`
}

type pingvin struct {
	s   *Server
	cfg PingvinConfig

	mu      sync.Mutex
	busy    []string             // inbox names being written
	failed  map[string]time.Time // file ID -> last failure; retried after an hour
	lastErr string               // last query error, logged once
	seen    map[string]bool      // file IDs done (or skipped); persisted
}

// seenFile lists the Pingvin files already brought in, so that one imported
// or removed from the inbox does not come back.
const seenFile = ".pingvin-seen"

func newPingvin(s *Server, cfg PingvinConfig) (*pingvin, error) {
	if s.cfg.Inbox == "" {
		return nil, errors.New("pingvin: needs an inbox folder")
	}
	if !rePingvinPrefix.MatchString(cfg.Prefix) {
		return nil, fmt.Errorf("pingvin: invalid reverse share prefix %q (letters, digits and -)", cfg.Prefix)
	}
	if cfg.SQLite == "" {
		cfg.SQLite = "sqlite3"
	}
	if cfg.Every <= 0 {
		cfg.Every = 30 * time.Second
	}
	p := &pingvin{s: s, cfg: cfg, failed: map[string]time.Time{}, seen: map[string]bool{}}
	b, err := os.ReadFile(filepath.Join(s.cfg.Inbox, seenFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, id := range strings.Fields(string(b)) {
		p.seen[id] = true
	}
	return p, nil
}

// watch polls until the server closes.
func (p *pingvin) watch() {
	defer p.s.wg.Done()
	t := time.NewTicker(p.cfg.Every)
	defer t.Stop()
	for {
		p.poll(p.s.ctx)
		select {
		case <-p.s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// processing is the inbox names being written now.
func (p *pingvin) processing() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.busy...)
}

func (p *pingvin) poll(ctx context.Context) {
	files, err := p.query(ctx)
	if err != nil {
		// Usually passing: Pingvin writing (locked, hot journal) or not up yet.
		if ctx.Err() == nil && err.Error() != p.lastErr {
			p.s.cfg.Logger.Printf("pingvin: %v (retrying)", err)
		}
		p.lastErr = err.Error()
		return
	}
	p.lastErr = ""
	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
		if p.seen[f.ID] || time.Since(p.failed[f.ID]) < time.Hour {
			continue
		}
		if err := p.bring(ctx, f); err != nil {
			if ctx.Err() != nil {
				return
			}
			p.failed[f.ID] = time.Now()
			p.s.cfg.Logger.Printf("pingvin: %s: %v (retrying in an hour)", f.Name, err)
		}
	}
}

// query lists the files uploaded through the reverse share, oldest first.
// The prefix is validated (letters, digits, -), so it is safe in the SQL;
// substr rather than LIKE, where _ would be a wildcard.
func (p *pingvin) query(ctx context.Context) ([]pingvinFile, error) {
	db := filepath.Join(p.cfg.Data, "pingvin-share.db")
	if _, err := os.Stat(db); err != nil {
		return nil, err
	}
	pre := p.cfg.Prefix
	sql := `SELECT f."id" AS id, f."name" AS name, f."size" AS size, s."id" AS share
FROM "File" f JOIN "Share" s ON s."id" = f."shareId"
LEFT JOIN "ReverseShare" r ON r."id" = s."reverseShareId"
WHERE s."removedReason" IS NULL AND s."storageProvider" = 'LOCAL'
  AND (substr(r."token", 1, ` + strconv.Itoa(len(pre)) + `) = '` + pre + `'
    OR substr(s."id", 1, ` + strconv.Itoa(len(pre)) + `) = '` + pre + `')
ORDER BY f."createdAt", f."id"`
	cmd := exec.CommandContext(ctx, p.cfg.SQLite, "-readonly", "-json", "-cmd", ".timeout 5000", db, sql)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v: %s", db, err, strings.TrimSpace(stderr.String()))
	}
	var files []pingvinFile
	if strings.TrimSpace(string(out)) == "" {
		return nil, nil // sqlite3 prints nothing for no rows
	}
	if err := json.Unmarshal(out, &files); err != nil {
		return nil, fmt.Errorf("reading %s: %v", db, err)
	}
	return files, nil
}

// bring converts one Pingvin file to FLAC in the inbox and starts its
// analysis. A file still being written (size not reached) is left for later.
func (p *pingvin) bring(ctx context.Context, f pingvinFile) error {
	if !rePingvinID.MatchString(f.ID) || !rePingvinID.MatchString(f.Share) {
		return p.skip(f, "unexpected file or share ID")
	}
	src := filepath.Join(p.cfg.Data, "uploads", "shares", f.Share, f.ID)
	st, err := os.Stat(src)
	if err != nil || strconv.FormatInt(st.Size(), 10) != f.Size {
		return nil
	}
	base := cleanUploadName(f.Name)
	ext := strings.ToLower(filepath.Ext(base))
	if ext != ".wav" && ext != ".flac" {
		return p.skip(f, "not a WAV or FLAC recording")
	}
	inbox := p.s.cfg.Inbox
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	dst := filepath.Join(inbox, stem+".flac")
	for i := 2; fileExists(dst); i++ {
		dst = filepath.Join(inbox, fmt.Sprintf("%s (%d).flac", stem, i))
	}

	p.setBusy(filepath.Base(dst), true)
	err = p.s.cfg.FFmpeg.ToFlac(ctx, src, ext, dst, nil)
	p.setBusy(filepath.Base(dst), false)
	if err != nil {
		return err
	}
	os.Chtimes(dst, st.ModTime(), st.ModTime()) // when it was uploaded
	if err := p.markSeen(f.ID); err != nil {
		return err
	}
	p.s.cfg.Logger.Printf("pingvin: %s is in the inbox as %s", f.Name, filepath.Base(dst))
	p.s.analyses.state(dst, true)
	return nil
}

func (p *pingvin) skip(f pingvinFile, why string) error {
	p.s.cfg.Logger.Printf("pingvin: skipping %s: %s", f.Name, why)
	return p.markSeen(f.ID)
}

func (p *pingvin) setBusy(name string, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.busy = append(p.busy, name)
		return
	}
	for i, n := range p.busy {
		if n == name {
			p.busy = append(p.busy[:i], p.busy[i+1:]...)
			return
		}
	}
}

// markSeen records id in the seen file, written whole and renamed.
func (p *pingvin) markSeen(id string) error {
	p.seen[id] = true
	ids := make([]string, 0, len(p.seen))
	for k := range p.seen {
		ids = append(ids, k)
	}
	path := filepath.Join(p.s.cfg.Inbox, seenFile)
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// cleanUploadName makes an uploaded file name safe as an inbox name: no
// folders, not hidden, no ".partial".
func cleanUploadName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.TrimLeft(strings.ReplaceAll(name, ".partial", ""), ". ")
	if name == "" || name == "/" {
		return "recording"
	}
	return name
}
