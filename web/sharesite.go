package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// The public upload page. It is its own handler, served on its own listener,
// and knows nothing of the studio: a static page and api/share/*, where every
// request carries a share's code in the Share-Code header. Nothing is listed
// or read back; files go to the share's church inbox as .<id>.partial and are
// renamed when complete, then ingested like any other inbox file.

// shareChunk is the largest chunk taken; the page sends 16 MB.
const shareChunk = 32 << 20

// sharePage is what the public listener serves of the static files.
var sharePage = map[string]string{"/": "share.html", "/share.js": "share.js", "/util.js": "util.js", "/style.css": "style.css"}

// ShareHandler is the public upload page, or nil when shares are off.
func (a *App) ShareHandler() http.Handler {
	if a.share == nil {
		return nil
	}
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	for path, file := range sharePage {
		pattern := "GET " + path
		if path == "/" {
			pattern += "{$}"
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, sub, file)
		})
	}
	mux.HandleFunc("GET /api/share", a.handleShareInfo)
	mux.HandleFunc("POST /api/share/uploads", a.handleShareStart)
	mux.HandleFunc("GET /api/share/uploads/{id}", a.handleShareStatus)
	mux.HandleFunc("PUT /api/share/uploads/{id}", a.handleShareChunk)
	return a.shareGuard(http.NewCrossOriginProtection().Handler(mux))
}

// shareGuard sets the security headers and counts every request.
func (a *App) shareGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		if wait, ok := a.share.limits.request(a.clientIP(r)); !ok {
			tooMany(w, wait, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tooMany(w http.ResponseWriter, wait time.Duration, msg string) {
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": msg + "; try again later", "retry": secs})
}

// clientIP is the address a request came from: the proxy's header (behind
// Cloudflare, CF-Connecting-IP) or the connection.
func (a *App) clientIP(r *http.Request) string {
	if name := a.cfg.Share.IPHeader; name != "" {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get(name))); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// shareFor checks the request's code. On failure it has written the error.
// A share that is revoked or expired is still found (the code was right),
// but refused.
func (a *App) shareFor(w http.ResponseWriter, r *http.Request) (*tenant, *Share, string, bool) {
	ip := a.clientIP(r)
	if wait := a.share.limits.blocked(ip); wait > 0 {
		tooMany(w, wait, "too many wrong codes")
		return nil, nil, ip, false
	}
	code := r.Header.Get("Share-Code")
	var t *tenant
	var sh *Share
	if len(code) <= 64 {
		t, sh = a.findShare(code)
	}
	if sh == nil {
		a.share.limits.failed(ip)
		a.cfg.Base.Logger.Printf("share: wrong code from %s", ip)
		writeError(w, http.StatusForbidden, errors.New("unknown code"))
		return nil, nil, ip, false
	}
	a.shareMu.Lock()
	state := sh.state(time.Now())
	a.shareMu.Unlock()
	if state == "revoked" || state == "expired" {
		writeError(w, http.StatusForbidden, fmt.Errorf("this code has %s", map[string]string{"revoked": "been withdrawn", "expired": "expired"}[state]))
		return nil, nil, ip, false
	}
	return t, sh, ip, true
}

// handleShareInfo tells the page what a code allows: GET api/share.
func (a *App) handleShareInfo(w http.ResponseWriter, r *http.Request) {
	t, sh, _, ok := a.shareFor(w, r)
	if !ok {
		return
	}
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	n, bytes := sh.reserved()
	info := map[string]any{
		"label": sh.Label, "church": t.info().Name, "types": sh.Types, "maxFileSize": sh.MaxFileSize,
		"expires": sh.Expires, "state": sh.state(time.Now()), "chunk": 16 << 20,
	}
	if sh.MaxUploads > 0 {
		info["uploadsLeft"] = max(0, sh.MaxUploads-sh.Count-n)
	}
	if sh.MaxTotal > 0 {
		info["bytesLeft"] = max(0, sh.MaxTotal-sh.Bytes-bytes)
	}
	writeJSON(w, http.StatusOK, info)
}

// shareFileName makes the name a file gets in the inbox from the one it
// had: letters, digits and a few signs (the date in it is kept), a known
// extension, never hidden or looking like a .partial.
func shareFileName(name string) (string, error) {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	ext := strings.ToLower(filepath.Ext(name))
	if !slices.Contains(shareTypes, ext) {
		return "", fmt.Errorf("only %s files are accepted", strings.Join(shareTypes, " and "))
	}
	base := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" -_.,()", r) {
			return r
		}
		return '_'
	}, strings.TrimSuffix(name, filepath.Ext(name)))
	base = strings.ReplaceAll(base, ".partial", "_partial")
	if r := []rune(base); len(r) > 80 {
		base = string(r[:80])
	}
	base = strings.Trim(base, " ._")
	if base == "" {
		base = "recording"
	}
	return base + ext, nil
}

// handleShareStart starts an upload and holds its place in the share's
// limits: POST api/share/uploads {name, size, modified} → {id}.
func (a *App) handleShareStart(w http.ResponseWriter, r *http.Request) {
	t, sh, ip, ok := a.shareFor(w, r)
	if !ok {
		return
	}
	var q struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified int64  `json:"modified"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name, err := shareFileName(q.Name)
	if err == nil && !containsExt(sh.Types, name) {
		err = fmt.Errorf("this code takes %s files", strings.Join(sh.Types, " and "))
	}
	if err == nil && q.Size < 1024 {
		err = errors.New("the file is empty or too small to be a recording")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if free, err := freeSpace(t.srv.cfg.Inbox); err == nil && free < uint64(q.Size)+diskMargin {
		a.cfg.Base.Logger.Printf("share: no room for %s (%s) from %s", name, sizeText(q.Size), ip)
		writeError(w, http.StatusInsufficientStorage, errors.New("the server has no room for this file now"))
		return
	}
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	if state := sh.state(time.Now()); state != "" {
		writeError(w, http.StatusForbidden, fmt.Errorf("this code takes no more uploads (%s)", state))
		return
	}
	if err := sh.room(q.Size); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	id := randomID(16)
	if sh.Pending == nil {
		sh.Pending = map[string]*pendingUpload{}
	}
	sh.Pending[id] = &pendingUpload{Name: name, Size: q.Size, Modified: q.Modified, IP: ip, Started: time.Now().UTC()}
	if err := a.saveShares(t); err != nil {
		delete(sh.Pending, id)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("share: %s starts %s (%s) with share %s", ip, name, sizeText(q.Size), sh.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "offset": 0})
}

func containsExt(types []string, name string) bool {
	for _, ext := range types {
		if strings.EqualFold(filepath.Ext(name), ext) {
			return true
		}
	}
	return false
}

func freeSpace(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

func partialPath(t *tenant, id string) string {
	return filepath.Join(t.srv.cfg.Inbox, ".share-"+id+".partial")
}

// pending is an upload of the share, or writes 404.
func (a *App) pending(w http.ResponseWriter, sh *Share, id string) (pendingUpload, bool) {
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	p := sh.Pending[id]
	if p == nil {
		writeError(w, http.StatusNotFound, errors.New("no such upload; start again"))
		return pendingUpload{}, false
	}
	return *p, true
}

func partialSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// handleShareStatus is where an upload continues: GET api/share/uploads/{id}.
func (a *App) handleShareStatus(w http.ResponseWriter, r *http.Request) {
	t, sh, _, ok := a.shareFor(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	p, ok := a.pending(w, sh, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"offset": partialSize(partialPath(t, id)), "size": p.Size})
}

// magicOK checks the first bytes of a file against its extension.
func magicOK(name string, head []byte) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav": // RIFF, or RF64/BW64 for files over 4 GB
		return len(head) >= 12 && (bytes.HasPrefix(head, []byte("RIFF")) || bytes.HasPrefix(head, []byte("RF64")) ||
			bytes.HasPrefix(head, []byte("BW64"))) && string(head[8:12]) == "WAVE"
	case ".flac": // some tools put an ID3 tag first
		return bytes.HasPrefix(head, []byte("fLaC")) || bytes.HasPrefix(head, []byte("ID3"))
	}
	return false
}

// handleShareChunk receives the bytes of an upload from offset n:
// PUT api/share/uploads/{id}?offset=n. A chunk at the wrong offset gets 409
// with the offset to continue from. The last one renames the file into the
// inbox and starts its ingest.
func (a *App) handleShareChunk(w http.ResponseWriter, r *http.Request) {
	t, sh, ip, ok := a.shareFor(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	p, ok := a.pending(w, sh, id)
	if !ok {
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 || offset >= p.Size {
		writeError(w, http.StatusBadRequest, errors.New("invalid offset"))
		return
	}
	if !a.share.limits.acquire(ip) {
		tooMany(w, 5*time.Second, "too many uploads at once")
		return
	}
	defer a.share.limits.release(ip)
	// One chunk of an upload at a time.
	a.shareMu.Lock()
	busy := a.share.busy[id]
	a.share.busy[id] = true
	a.shareMu.Unlock()
	if busy {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a chunk of this upload is already being received"})
		return
	}
	defer func() {
		a.shareMu.Lock()
		delete(a.share.busy, id)
		a.shareMu.Unlock()
	}()

	tmp := partialPath(t, id)
	if have := partialSize(tmp); have != offset {
		writeJSON(w, http.StatusConflict, map[string]any{"error": fmt.Sprintf("expected the chunk at %d", have), "offset": have})
		return
	}
	body := io.Reader(http.MaxBytesReader(w, r.Body, min(shareChunk, p.Size-offset)))
	if offset == 0 {
		head := make([]byte, 12)
		n, _ := io.ReadFull(body, head)
		if !magicOK(p.Name, head[:n]) {
			writeError(w, http.StatusUnsupportedMediaType, fmt.Errorf("%s is not a %s file", p.Name, strings.TrimPrefix(filepath.Ext(p.Name), ".")))
			return
		}
		body = io.MultiReader(bytes.NewReader(head[:n]), body)
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	n, err := io.Copy(f, body)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Truncate(tmp, offset) // drop what this chunk wrote, so it can be sent again
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, errors.New("chunk too large"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if offset+n < p.Size {
		writeJSON(w, http.StatusOK, map[string]any{"offset": offset + n})
		return
	}
	name, err := a.finishShareUpload(t, sh, id, p, tmp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"offset": p.Size, "name": name})
}

// finishShareUpload puts a complete upload in the inbox under a free name,
// counts it, and starts its ingest.
func (a *App) finishShareUpload(t *tenant, sh *Share, id string, p pendingUpload, tmp string) (string, error) {
	if p.Modified > 0 {
		mt := time.UnixMilli(p.Modified)
		os.Chtimes(tmp, mt, mt)
	}
	ext := filepath.Ext(p.Name)
	base := strings.TrimSuffix(p.Name, ext)
	var name string
	for i := 1; ; i++ {
		name = p.Name
		if i > 1 {
			name = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		// A hard link never replaces a file; then the partial goes.
		err := os.Link(tmp, filepath.Join(t.srv.cfg.Inbox, name))
		if err == nil {
			os.Remove(tmp)
			break
		}
		if !errors.Is(err, fs.ErrExist) || i == 100 {
			return "", err
		}
	}
	a.shareMu.Lock()
	delete(sh.Pending, id)
	sh.Count++
	sh.Bytes += p.Size
	sh.Log = append(sh.Log, ShareUpload{Name: name, Size: p.Size, IP: p.IP, Time: time.Now().UTC()})
	if len(sh.Log) > shareLogMax {
		sh.Log = sh.Log[len(sh.Log)-shareLogMax:]
	}
	if err := a.saveShares(t); err != nil {
		a.cfg.Base.Logger.Printf("share: saving %s: %v", sharesFile, err)
	}
	a.shareMu.Unlock()
	a.cfg.Base.Logger.Printf("share: received %s (%s) from %s with share %s", name, sizeText(p.Size), p.IP, sh.ID)
	s := t.srv
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.ingest(name); err != nil {
			s.cfg.Logger.Printf("inbox: %s: %v", name, err) // the watcher retries when it changes
		}
	}()
	return name, nil
}

// sweepShares drops uploads that stopped long ago, with their partial files,
// and forgets quiet addresses.
func (a *App) sweepShares(now time.Time) {
	a.share.limits.prune()
	a.mu.Lock()
	tenants := append([]*tenant(nil), a.tenants...)
	a.mu.Unlock()
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	for _, t := range tenants {
		changed := false
		for _, sh := range t.shares {
			for id, p := range sh.Pending {
				last := p.Started
				if st, err := os.Stat(partialPath(t, id)); err == nil && st.ModTime().After(last) {
					last = st.ModTime()
				}
				if now.Sub(last) > pendingStale && !a.share.busy[id] {
					os.Remove(partialPath(t, id))
					delete(sh.Pending, id)
					changed = true
					a.cfg.Base.Logger.Printf("share: dropped unfinished upload %s from %s", p.Name, p.IP)
				}
			}
		}
		if changed {
			if err := a.saveShares(t); err != nil {
				a.cfg.Base.Logger.Printf("share: saving %s: %v", sharesFile, err)
			}
		}
	}
}

// keepSweeping runs sweepShares until the app closes.
func (a *App) keepSweeping() {
	defer a.share.wg.Done()
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-a.share.done:
			return
		case now := <-tick.C:
			a.sweepShares(now)
		}
	}
}
