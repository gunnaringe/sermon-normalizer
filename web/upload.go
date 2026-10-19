package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxChunk is the largest upload chunk accepted. Browsers send recordings in
// smaller pieces, which keeps every request well below proxy body limits
// (Cloudflare's is 100 MB) and lets a broken upload continue where it was.
const MaxChunk = 64 << 20

// uploadStale is how long an unfinished upload blocks a new one to the
// same date.
const uploadStale = 2 * time.Minute

// handleUpload receives one chunk of a recording uploaded straight into its
// date folder (no inbox needed; "sermon" names a second sermon of the day):
//
//	POST api/upload?date=2026-10-04[&sermon=Evening]&name=x.wav&size=<total>&offset=<n>&modified=<ms>
//
// with the bytes from offset n as the body. Chunks are appended to a hidden
// file in the date folder; a chunk at the wrong offset gets 409 with the
// offset to continue from. The last chunk renames it to <folder>_raw.<ext>
// and starts the FLAC conversion (for a WAV) and the waveform right away,
// without waiting for them.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date := q.Get("date")
	name, err := inboxName(q.Get("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	size, err1 := strconv.ParseInt(q.Get("size"), 10, 64)
	offset, err2 := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err1 != nil || err2 != nil || size <= 0 || offset < 0 || offset >= size {
		writeError(w, http.StatusBadRequest, errors.New("invalid size or offset"))
		return
	}
	sermon, err := cleanName(q.Get("sermon"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	folder, status, err := s.newRawFolder(date, sermon)
	if err != nil {
		writeError(w, status, err)
		return
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	base := filepath.Base(folder) // <date> or <date> <name>
	final := filepath.Join(folder, base+"_raw"+ext)
	tmp := filepath.Join(folder, "."+base+"_raw.upload") // one per sermon, whatever the format

	conflict := func(msg string, at int64) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "offset": at})
	}
	st, statErr := os.Stat(tmp)
	flag := os.O_WRONLY | os.O_APPEND
	switch {
	case offset == 0:
		if statErr == nil && st.Size() > 0 && time.Since(st.ModTime()) < uploadStale {
			conflict("another upload to "+date+" is in progress", st.Size())
			return
		}
		flag |= os.O_CREATE | os.O_TRUNC
	case statErr != nil:
		conflict("the upload was not started or has been replaced; start again", 0)
		return
	case st.Size() != offset:
		conflict(fmt.Sprintf("expected the chunk at %d", st.Size()), st.Size())
		return
	}

	f, err := os.OpenFile(tmp, flag, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, min(MaxChunk, size-offset)))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// Drop what this chunk wrote, so the client can send it again.
		os.Truncate(tmp, offset)
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, errors.New("chunk too large"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if offset+n < size {
		writeJSON(w, http.StatusOK, map[string]any{"offset": offset + n})
		return
	}

	if ms, err := strconv.ParseInt(q.Get("modified"), 10, 64); err == nil && ms > 0 {
		t := time.UnixMilli(ms)
		os.Chtimes(tmp, t, t)
	}
	if fileExists(final) {
		os.Remove(tmp)
		writeError(w, http.StatusConflict, fmt.Errorf("%s appeared meanwhile", filepath.Base(final)))
		return
	}
	if err := os.Rename(tmp, final); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.cfg.Logger.Printf("upload: received %s as %s", name, s.relPath(final))
	s.analyses.state(final, true)
	writeJSON(w, http.StatusCreated, map[string]any{"offset": size, "path": s.relPath(folder)})
}
