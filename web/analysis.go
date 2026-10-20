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

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// peakWindow is the waveform resolution: 10 ms is finer than any cut needs.
const peakWindow = 0.01

// analysisState is how far the waveform data and preview for a recording are.
// A WAV is first converted to FLAC next to it (stage "convert"), which then
// replaces it; the analysis (stage "analyze") is of the FLAC.
type analysisState struct {
	State    string  `json:"state"` // none, running, done, error
	Stage    string  `json:"stage,omitempty"`
	Progress float64 `json:"progress"`
	Error    string  `json:"error,omitempty"`
	Key      string  `json:"key,omitempty"` // changes with the recording; for cache-busting URLs
}

type analyses struct {
	s       *Server
	mu      sync.Mutex
	current map[string]*analysisState // running or failed, by key
	pending map[string]*analysisState // by the path of a FLAC being made from a WAV
	looking map[string]bool           // keys being looked at by lookLater
	sem     chan struct{}             // one full decode at a time
}

func newAnalyses(s *Server) *analyses {
	return &analyses{s: s, current: map[string]*analysisState{}, pending: map[string]*analysisState{}, looking: map[string]bool{}, sem: s.limits.decode}
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
		if start && !factsFresh(readState(folderOfRecording(raw)), raw) {
			a.lookLater(raw, key)
		}
		return analysisState{State: "done", Progress: 1, Key: key}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.pending[raw]; st != nil {
		return *st // the FLAC is being made and checked: the WAV's run covers it
	}
	if st := a.current[key]; st != nil && (st.State == "running" || !start) {
		return *st
	}
	if !start {
		return analysisState{State: "none", Key: key}
	}
	st := &analysisState{State: "running", Stage: "analyze", Key: key}
	if isWAV(raw) {
		st.Stage = "convert"
	}
	a.current[key] = st
	a.s.wg.Add(1)
	go func() {
		defer a.s.wg.Done()
		keys := []string{key}
		err := a.run(raw, key, st, &keys)
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil {
			st.State, st.Error = "error", err.Error()
			a.s.cfg.Logger.Printf("analysis of %s: %v", raw, err)
			return
		}
		for _, k := range keys {
			delete(a.current, k)
		}
	}()
	return *st
}

func isWAV(path string) bool { return strings.EqualFold(filepath.Ext(path), ".wav") }

// stage is what is being done to raw right now ("convert" to FLAC, else
// "analyze"), or "" when nothing is.
func (a *analyses) stage(raw string) string {
	key, _ := cacheKey(raw)
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.pending[raw]
	if st == nil {
		st = a.current[key]
	}
	if st == nil || st.State != "running" {
		return ""
	}
	return st.Stage
}

// converting reports whether raw is being converted to FLAC right now.
func (a *analyses) converting(raw string) bool { return a.stage(raw) == "convert" }

// carry moves the finished analysis of a recording along when the file is
// moved (an import), so it is not redone: from the key it had before the
// move to the key of its new path.
func (a *analyses) carry(oldKey, newPath string) {
	newKey, err := cacheKey(newPath)
	if err != nil || oldKey == "" || !a.done(oldKey) || a.done(newKey) {
		return
	}
	if err := os.Rename(a.dir(oldKey), a.dir(newKey)); err != nil {
		a.s.cfg.Logger.Printf("analysis cache: %v", err)
	}
}

// run converts raw to FLAC if it is a WAV, then analyses it. keys collects
// the keys the state is registered under (the WAV's, then the FLAC's).
func (a *analyses) run(raw, key string, st *analysisState, keys *[]string) error {
	ctx := a.s.ctx
	ff := a.s.cfg.FFmpeg
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	total, _ := ff.Duration(ctx, raw)
	progress := func(sec float64) {
		if total > 0 {
			a.mu.Lock()
			st.Progress = min(sec/total, 0.99)
			a.mu.Unlock()
		}
	}
	if isWAV(raw) {
		flac := strings.TrimSuffix(raw, filepath.Ext(raw)) + ".flac"
		if fileExists(flac) {
			return fmt.Errorf("%s and %s both exist", filepath.Base(raw), filepath.Base(flac))
		}
		a.mu.Lock()
		a.pending[flac] = st
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			delete(a.pending, flac)
			a.mu.Unlock()
		}()
		wav := sourceFile{Name: filepath.Base(raw)}
		if st, err := os.Stat(raw); err == nil {
			wav.Size = st.Size()
		}
		wav.Format, _ = ff.ProbeFile(ctx, raw) // only shown: a WAV ffmpeg can read has a header
		if err := ff.ToFlac(ctx, raw, ".wav", flac, progress); err != nil {
			return err
		}
		if err := os.Remove(raw); err != nil {
			return err
		}
		if _, err := a.s.updateState(folderOfRecording(flac), func(st *folderState) error {
			st.ConvertedFrom = &wav
			return nil
		}); err != nil {
			a.s.cfg.Logger.Printf("%s: %v", a.s.display(flac), err)
		}
		a.s.cfg.Logger.Printf("converted %s to FLAC (verified identical) and removed the WAV", a.s.display(raw))
		newKey, err := cacheKey(flac)
		if err != nil {
			return err
		}
		a.mu.Lock()
		a.current[newKey] = st
		*keys = append(*keys, newKey)
		st.Stage, st.Progress, st.Key = "analyze", 0, newKey
		a.mu.Unlock()
		raw, key = flac, newKey
	}
	dir := a.dir(key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := analyze(ctx, ff, raw, dir, progress)
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err := writePeaks(filepath.Join(dir, "peaks.json.gz"), data); err != nil {
		return err
	}
	a.lookAt(ctx, raw)
	return nil
}

// lookAt keeps what raw is in recording.json (lookAt on the server), unless
// that is known already. A failure is logged: the waveform works without it.
func (a *analyses) lookAt(ctx context.Context, raw string) {
	if factsFresh(readState(folderOfRecording(raw)), raw) {
		return
	}
	if err := a.s.lookAt(ctx, raw); err != nil && ctx.Err() == nil {
		a.s.cfg.Logger.Printf("looking at %s: %v", a.s.display(raw), err)
	}
}

// lookLater looks at a recording that was analysed before recording.json
// knew about it, in the background and one decode at a time; once per key.
func (a *analyses) lookLater(raw, key string) {
	a.mu.Lock()
	if a.looking[key] {
		a.mu.Unlock()
		return
	}
	a.looking[key] = true
	a.mu.Unlock()
	a.s.wg.Add(1)
	go func() {
		defer a.s.wg.Done()
		defer func() {
			a.mu.Lock()
			delete(a.looking, key)
			a.mu.Unlock()
		}()
		select {
		case a.sem <- struct{}{}:
			defer func() { <-a.sem }()
		case <-a.s.ctx.Done():
			return
		}
		a.lookAt(a.s.ctx, raw)
	}()
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
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return "", false
	}
	raw := paths.Source()
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
