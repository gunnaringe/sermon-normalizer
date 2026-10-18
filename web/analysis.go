package web

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// peakWindow is the waveform resolution: 10 ms is finer than any cut needs.
const peakWindow = 0.01

// analysisState is how far the waveform data and preview for a recording are.
type analysisState struct {
	State    string  `json:"state"` // none, running, done, error
	Progress float64 `json:"progress"`
	Error    string  `json:"error,omitempty"`
	Key      string  `json:"key,omitempty"` // changes with the recording; for cache-busting URLs
}

type analyses struct {
	s       *Server
	mu      sync.Mutex
	current map[string]*analysisState // running or failed, by key
	sem     chan struct{}             // one full decode at a time
}

func newAnalyses(s *Server) *analyses {
	return &analyses{s: s, current: map[string]*analysisState{}, sem: make(chan struct{}, 1)}
}

// cacheVersion changes when the analysis changes, so old caches are redone
// (2: normalized preview).
const cacheVersion = 2

// cacheKey identifies a recording by path, size and modification time.
func cacheKey(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("v%d\x00%s\x00%d\x00%d", cacheVersion, path, st.Size(), st.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:8]), nil
}

func (a *analyses) dir(key string) string { return filepath.Join(a.s.cfg.CacheDir, key) }

func (a *analyses) done(key string) bool {
	_, err := os.Stat(filepath.Join(a.dir(key), "peaks.json.gz"))
	return err == nil
}

// state reports on the analysis of raw, starting it if start is set.
func (a *analyses) state(raw string, start bool) analysisState {
	key, err := cacheKey(raw)
	if err != nil {
		return analysisState{State: "error", Error: err.Error()}
	}
	if a.done(key) {
		now := time.Now()
		os.Chtimes(a.dir(key), now, now) // in use: keep it in the cache
		return analysisState{State: "done", Progress: 1, Key: key}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.current[key]; st != nil && (st.State == "running" || !start) {
		return *st
	}
	if !start {
		return analysisState{State: "none", Key: key}
	}
	st := &analysisState{State: "running", Key: key}
	a.current[key] = st
	a.s.wg.Add(1)
	go func() {
		defer a.s.wg.Done()
		err := a.run(raw, key, st)
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil {
			st.State, st.Error = "error", err.Error()
			a.s.cfg.Logger.Printf("analysis of %s: %v", raw, err)
			return
		}
		delete(a.current, key)
	}()
	return *st
}

func (a *analyses) run(raw, key string, st *analysisState) error {
	ctx := a.s.ctx
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	dir := a.dir(key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	total, _ := a.s.cfg.FFmpeg.Duration(ctx, raw)
	progress := func(sec float64) {
		if total > 0 {
			a.mu.Lock()
			st.Progress = min(sec/total, 0.99)
			a.mu.Unlock()
		}
	}
	data, err := analyze(ctx, a.s.cfg.FFmpeg, raw, dir, progress)
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	return writePeaks(filepath.Join(dir, "peaks.json.gz"), data)
}

// analyze decodes raw once: levels for the waveform from one output, and a
// small MP3 preview for listening from the other. Fixed-bitrate MP3 is what
// browsers seek most reliably in, which matters in an hour-long file.
//
// The preview is normalized (normalizer.ListenLevels): recordings are often
// far too quiet to hear where the sermon starts. The waveform keeps the
// original levels, where loud music and quieter speech are easy to tell apart.
func analyze(ctx context.Context, ff normalizer.FFmpeg, raw, dir string, progress func(float64)) (normalizer.LevelData, error) {
	preview := filepath.Join(dir, "preview.mp3")
	tmp := filepath.Join(dir, "preview.partial.mp3")
	cmd := exec.CommandContext(ctx, ff.Bin, "-nostdin", "-hide_banner", "-v", "error", "-y", "-i", raw,
		"-map", "0:a:0", "-ac", "1", "-ar", strconv.Itoa(normalizer.LevelRate), "-f", "s16le", "pipe:1",
		"-map", "0:a:0", "-ac", "1", "-af", normalizer.ListenLevels, "-ar", "44100",
		"-c:a", "libmp3lame", "-b:a", "64k", "-f", "mp3", tmp)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return normalizer.LevelData{}, err
	}
	if err := cmd.Start(); err != nil {
		return normalizer.LevelData{}, err
	}
	data, readErr := normalizer.ReadLevels(out, peakWindow, progress)
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		os.Remove(tmp)
		if ctx.Err() != nil {
			return data, ctx.Err()
		}
		return data, fmt.Errorf("ffmpeg failed (%w): %s", err, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return data, readErr
	}
	if len(data.Peak) == 0 {
		return data, errors.New("the recording is empty")
	}
	return data, os.Rename(tmp, preview)
}

// writePeaks stores the waveform data as gzipped JSON, written compactly by
// hand: an hour has 360 000 values.
func writePeaks(path string, data normalizer.LevelData) error {
	tmp := path + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	w := bufio.NewWriter(zw)
	duration := float64(len(data.Peak)) * data.Window
	fmt.Fprintf(w, `{"duration":%.3f,"window":%g,"silences":[`, duration, data.Window)
	for i, sp := range normalizer.Silences(data.RMS, data.Window, 1) {
		if i > 0 {
			w.WriteByte(',')
		}
		fmt.Fprintf(w, `{"start":%.2f,"end":%.2f}`, sp.Start, sp.End)
	}
	w.WriteString(`],"peaks":[`)
	for i, p := range data.Peak {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteString(strconv.FormatFloat(p, 'f', 2, 64))
	}
	w.WriteString("]}")
	err = errors.Join(w.Flush(), zw.Close(), f.Close())
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// rawFor is the raw recording of the folder in the path parameter.
func (s *Server) rawFor(w http.ResponseWriter, r *http.Request) (string, bool) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return "", false
	}
	paths, err := normalizer.NewPaths(abs, s.cfg.Archive)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return "", false
	}
	raw := paths.Sources().Raw
	if raw == "" {
		writeError(w, http.StatusNotFound, errors.New("no raw recording in this folder"))
		return "", false
	}
	return raw, true
}

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.rawFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.analyses.state(raw, r.Method == http.MethodPost))
}

// cached opens a file of the finished analysis of the folder's recording.
func (s *Server) cached(w http.ResponseWriter, r *http.Request, name string) (*os.File, bool) {
	raw, ok := s.rawFor(w, r)
	if !ok {
		return nil, false
	}
	key, err := cacheKey(raw)
	if err != nil || !s.analyses.done(key) {
		writeError(w, http.StatusConflict, errors.New("not analysed yet"))
		return nil, false
	}
	f, err := os.Open(filepath.Join(s.analyses.dir(key), name))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return nil, false
	}
	return f, true
}

func (s *Server) handlePeaks(w http.ResponseWriter, r *http.Request) {
	f, ok := s.cached(w, r, "peaks.json.gz")
	if !ok {
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		io.Copy(w, f)
		return
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	io.Copy(w, zr)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	f, ok := s.cached(w, r, "preview.mp3")
	if !ok {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "preview.mp3", st.ModTime(), f) // handles Range: seeking
}

// CleanCache removes analyses not used for maxAge, at startup.
func CleanCache(dir string, maxAge time.Duration) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range des {
		if info, err := de.Info(); err == nil && de.IsDir() && time.Since(info.ModTime()) > maxAge {
			os.RemoveAll(filepath.Join(dir, de.Name()))
		}
	}
}
