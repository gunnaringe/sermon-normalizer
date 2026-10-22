package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// A church can have its own logo, shown in the header instead of the app's:
// logo.png|jpg|webp|svg in the church folder (next to church.json).
const maxLogo = 1 << 20

var logoTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/svg+xml": ".svg"}

// logoKind tells what an uploaded file is: one of logoTypes, or "".
func logoKind(b []byte) string {
	if t := http.DetectContentType(b); logoTypes[t] != "" {
		return t
	}
	head := bytes.TrimLeft(b[:min(len(b), 1024)], "\xef\xbb\xbf \t\r\n")
	if (bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<svg"))) && bytes.Contains(b, []byte("<svg")) {
		return "image/svg+xml"
	}
	return ""
}

// logoFile is the church's logo file, "" if it has none.
func (t *tenant) logoFile() string {
	if t.dir == "" {
		return ""
	}
	for _, ext := range logoTypes {
		if p := filepath.Join(t.dir, "logo"+ext); fileExists(p) {
			return p
		}
	}
	return ""
}

// logoVersion changes whenever the logo does ("" = no logo): the page puts it
// in the address so a new logo is not hidden by the browser's cache.
func (t *tenant) logoVersion() string {
	if st, err := os.Stat(t.logoFile()); err == nil {
		return strconv.FormatInt(st.ModTime().UnixNano(), 36)
	}
	return ""
}

func (a *App) handleLogo(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tenantFor(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
		return
	}
	serveLogo(w, r, t)
}

// serveLogo sends the church's logo, or a 404.
func serveLogo(w http.ResponseWriter, r *http.Request, t *tenant) {
	p := t.logoFile()
	if p == "" {
		writeError(w, http.StatusNotFound, errors.New("no logo"))
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("no logo"))
		return
	}
	defer f.Close()
	for ct, ext := range logoTypes {
		if ext == filepath.Ext(p) {
			w.Header().Set("Content-Type", ct)
		}
	}
	// It is an upload: shown only as an image, never run (an SVG can hold script).
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	st, _ := f.Stat()
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (a *App) handleLogoPut(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tenantFor(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
		return
	}
	if t.dir == "" {
		writeError(w, http.StatusBadRequest, errors.New("this church has no folder of its own, so it cannot have a logo"))
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogo))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("the logo can be at most %d KB", maxLogo>>10))
		return
	}
	kind := logoKind(b)
	if kind == "" {
		writeError(w, http.StatusBadRequest, errors.New("the logo must be a PNG, JPEG, WebP or SVG image"))
		return
	}
	dst := filepath.Join(t.dir, "logo"+logoTypes[kind])
	tmp := dst + ".partial"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.removeLogos(t) // another type from before
	if err := normalizer.Rename(tmp, dst); err != nil {
		normalizer.Remove(tmp)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, a.infoFor(t, a.userOf(r)))
}

func (a *App) handleLogoDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tenantFor(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
		return
	}
	a.removeLogos(t)
	writeJSON(w, http.StatusOK, a.infoFor(t, a.userOf(r)))
}

func (a *App) removeLogos(t *tenant) {
	if t.dir == "" {
		return
	}
	for _, ext := range logoTypes {
		normalizer.Remove(filepath.Join(t.dir, "logo"+ext))
	}
}
