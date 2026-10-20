// Package web is a long-running web UI for the sermon studio: browse date
// folders, cut the sermon on a waveform, set options, run and follow jobs,
// and bring new recordings in: uploaded straight to a date folder, or
// through an inbox folder that anything can drop files into.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

//go:embed static
var static embed.FS

func init() { mime.AddExtensionType(".webmanifest", "application/manifest+json") }

// Config is how the server is set up.
type Config struct {
	Root     string // folder holding the date folders (e.g. Taler/)
	Inbox    string // folder new recordings arrive in; "" = no inbox
	CacheDir string // waveform data and previews
	Layout   string // where imports go under Root, as a Go time layout
	// InboxEvery is how often the inbox is looked at, InboxSettle how long a
	// file must stay unchanged before it is taken (0 = 10 s and 30 s).
	InboxEvery, InboxSettle time.Duration
	FFmpeg                  normalizer.FFmpeg
	Logger                  *log.Logger

	limits *limits // set by App so churches share them

	Meta     *Meta            // default tags; nil = DefaultMeta
	SaveMeta func(Meta) error // stores changed default tags; nil = kept in memory only

	Names     *NameList            // remembered sermon names; nil = defaultNames
	SaveNames func(NameList) error // nil = kept in memory only
}

// limits keeps the heavy work to one at a time, across all churches.
type limits struct{ encode, decode chan struct{} }

func newLimits() *limits {
	return &limits{encode: make(chan struct{}, 1), decode: make(chan struct{}, 1)}
}

// DefaultLayout puts imported recordings in <year>/<date>/.
const DefaultLayout = "2006/2006-01-02"

// Server serves the API and the embedded front end.
type Server struct {
	cfg    Config
	root   string // Root, absolute with symlinks resolved
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // background work: analyses and jobs

	limits   *limits // shared by all churches: one encode, one decode at a time
	jobs     *jobs
	analyses *analyses
	inbox    *inboxWatcher
	meta     metaStore
	names    nameStore
	ingestMu sync.Mutex // one inbox file moves at a time
	stateMu  sync.Mutex // recording.json is read, changed and written as one step

	tagsMu   sync.Mutex
	tagDates map[string]string // recording date from tags, by cacheKey ("" = none)
}

// New prepares a server. Close it to stop background work.
func New(cfg Config) (*Server, error) {
	if cfg.Layout == "" {
		cfg.Layout = DefaultLayout
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	root, err := realDir(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("root: %w", err)
	}
	if cfg.Inbox != "" {
		if cfg.Inbox, err = realDir(cfg.Inbox); err != nil {
			return nil, fmt.Errorf("inbox: %w", err)
		}
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if cfg.limits == nil {
		cfg.limits = newLimits()
	}
	s := &Server{cfg: cfg, root: root, ctx: ctx, cancel: cancel, limits: cfg.limits, tagDates: map[string]string{}}
	s.meta = metaStore{meta: DefaultMeta, save: cfg.SaveMeta}
	if cfg.Meta != nil {
		s.meta.meta = *cfg.Meta
	}
	s.names = nameStore{v: defaultNames, save: cfg.SaveNames}
	if cfg.Names != nil {
		s.names.v = *cfg.Names
	}
	s.jobs = newJobs(s)
	s.analyses = newAnalyses(s)
	if cfg.Inbox != "" {
		s.inbox = newInboxWatcher(s)
		s.wg.Add(1)
		go s.inbox.watch()
	}
	return s, nil
}

func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// Close cancels running jobs and analyses and waits for them to clean up,
// so no ffmpeg or .partial file is left behind.
func (s *Server) Close() {
	s.cancel()
	s.wg.Wait()
}

// Handler is the whole site for one church: the front end and the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	mux.Handle("GET /", http.FileServerFS(sub))
	api := s.apiMux()
	for _, method := range []string{"GET", "POST", "PUT"} {
		mux.Handle(method+" /api/", api)
	}
	// Once a login proxy sets a cookie, other sites must not be able to
	// start runs, uploads or imports on the user's behalf.
	return http.NewCrossOriginProtection().Handler(mux)
}

// apiMux is the API of one church.
func (s *Server) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("PUT /api/settings", s.handleSettingsPut)
	mux.HandleFunc("GET /api/names", s.handleNames)
	mux.HandleFunc("PUT /api/names", s.handleNamesPut)
	mux.HandleFunc("POST /api/meta/preview", s.handleMetaPreview)
	mux.HandleFunc("GET /api/folders", s.handleFolders)
	mux.HandleFunc("GET /api/folder", s.handleFolder)
	mux.HandleFunc("POST /api/check", s.handleCheck)
	mux.HandleFunc("PUT /api/sermon", s.handleSermonPut)
	mux.HandleFunc("POST /api/sermon/save", s.handleSermonSave)
	mux.HandleFunc("POST /api/sermon/revert", s.handleSermonRevert)
	mux.HandleFunc("GET /api/file", s.handleFile)

	mux.HandleFunc("GET /api/analysis", s.handleAnalysis)
	mux.HandleFunc("POST /api/analysis", s.handleAnalysis)
	mux.HandleFunc("GET /api/peaks", s.handlePeaks)
	mux.HandleFunc("GET /api/preview", s.handlePreview)

	mux.HandleFunc("POST /api/rename", s.handleRename)
	mux.HandleFunc("POST /api/run", s.handleRun)
	mux.HandleFunc("POST /api/make", s.handleMake)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /api/jobs/{id}/events", s.handleJobEvents)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleJobCancel)

	mux.HandleFunc("POST /api/upload", s.handleUpload)
	return mux
}

// errBadPath is returned for paths outside the root or naming hidden files.
var errBadPath = errors.New("path outside the root")

// resolve turns a slash-separated path relative to the root into an
// absolute path inside it. Hidden names (.partial files, the temporary
// master) and ".." are refused, and symlinks may not lead out of the root,
// also for paths that do not exist yet (an import creates folders).
func (s *Server) resolve(rel string) (string, error) {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") {
			return "", errBadPath
		}
	}
	abs, err := realPath(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(s.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errBadPath
	}
	return abs, nil
}

// realPath resolves symlinks in the longest existing part of path and
// appends the rest.
func realPath(path string) (string, error) {
	var rest []string
	for {
		real, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		rest = append([]string{filepath.Base(path)}, rest...)
		path = parent
	}
}

// display names a file for the log: relative to the root, or just its name.
func (s *Server) display(abs string) string {
	if r, err := filepath.Rel(s.root, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.Base(abs)
}

// relPath is abs relative to the root, slash-separated ("" for the root).
func (s *Server) relPath(abs string) string {
	r, err := filepath.Rel(s.root, abs)
	if err != nil || r == "." {
		return ""
	}
	return filepath.ToSlash(r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// pathParam resolves the "path" query parameter, writing an error if bad.
func (s *Server) pathParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	abs, err := s.resolve(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return "", false
	}
	return abs, true
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"inbox": s.cfg.Inbox != "",
		"chunk": MaxChunk,
		"defaults": map[string]any{
			"mono":   requestFrom(normalizer.DefaultOptions()),
			"stereo": stereoDefaults(),
		},
	})
}

func stereoDefaults() runRequest {
	o := normalizer.DefaultOptions()
	o.SetStereo(true)
	return requestFrom(o)
}

// fileInfo describes a file in the UI.
type fileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (s *Server) fileInfo(abs string) *fileInfo {
	st, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	return &fileInfo{Name: filepath.Base(abs), Path: s.relPath(abs), Size: st.Size(), Modified: st.ModTime()}
}

// handleFile serves audio files under the root: outputs to listen to or download.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	types := map[string]string{".flac": "audio/flac", ".mp3": "audio/mpeg", ".opus": "audio/ogg", ".wav": "audio/wav"}
	ctype, known := types[strings.ToLower(filepath.Ext(abs))]
	if !known {
		writeError(w, http.StatusNotFound, errors.New("not an audio file"))
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("no such file"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, errors.New("no such file"))
		return
	}
	w.Header().Set("Content-Type", ctype)
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", st.Name()))
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}
