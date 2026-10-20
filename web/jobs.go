package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// keepFinished is how many finished jobs are remembered (in memory only).
const keepFinished = 50

// job is one pipeline run for a date folder.
type job struct {
	ID     string
	Folder string // relative to the root

	mu       sync.Mutex
	state    string // queued, running, done, failed, cancelled
	steps    []string
	log      []string
	warnings []string
	progress float64
	err      string
	outputs  []outputFileInfo
	subs     map[chan struct{}]struct{}
	cancel   context.CancelFunc
	finished chan struct{}
}

// jobSnapshot is a job as the front end sees it.
type jobSnapshot struct {
	ID       string           `json:"id"`
	Folder   string           `json:"folder"`
	State    string           `json:"state"`
	Steps    []string         `json:"steps"`
	Log      []string         `json:"log"`
	Warnings []string         `json:"warnings"`
	Progress float64          `json:"progress"`
	Error    string           `json:"error,omitempty"`
	Outputs  []outputFileInfo `json:"outputs"`
}

func (j *job) snapshot() jobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobSnapshot{
		ID: j.ID, Folder: j.Folder, State: j.state,
		Steps: append([]string{}, j.steps...), Log: append([]string{}, j.log...),
		Warnings: append([]string{}, j.warnings...), Progress: j.progress, Error: j.err,
		Outputs: append([]outputFileInfo{}, j.outputs...),
	}
}

func (j *job) over() bool { return j.state == "done" || j.state == "failed" || j.state == "cancelled" }

// update changes the job under its lock and wakes the event streams.
func (j *job) update(fn func()) {
	j.mu.Lock()
	fn()
	for ch := range j.subs {
		select { // a pending wake-up already covers this change
		case ch <- struct{}{}:
		default:
		}
	}
	j.mu.Unlock()
}

func (j *job) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	j.mu.Lock()
	j.subs[ch] = struct{}{}
	j.mu.Unlock()
	return ch, func() {
		j.mu.Lock()
		delete(j.subs, ch)
		j.mu.Unlock()
	}
}

type jobs struct {
	s    *Server
	mu   sync.Mutex
	byID map[string]*job
	all  []*job        // oldest first
	sem  chan struct{} // one encode at a time
}

func newJobs(s *Server) *jobs {
	return &jobs{s: s, byID: map[string]*job{}, sem: s.limits.encode}
}

func (js *jobs) get(id string) *job {
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.byID[id]
}

// latest is the newest job for a folder, if any.
func (js *jobs) latest(folder string) *job {
	js.mu.Lock()
	defer js.mu.Unlock()
	for i := len(js.all) - 1; i >= 0; i-- {
		if js.all[i].Folder == folder {
			return js.all[i]
		}
	}
	return nil
}

// active is the queued or running job for a folder, if any.
func (js *jobs) active(folder string) *job {
	if j := js.latest(folder); j != nil {
		j.mu.Lock()
		defer j.mu.Unlock()
		if !j.over() {
			return j
		}
	}
	return nil
}

var errBusy = errors.New("this recording is already being processed")

// start queues a run. It runs on the server's context, not the request's:
// closing the browser does not stop it, shutting down the server does.
func (js *jobs) start(pl plan) (*job, error) {
	folder := js.s.relPath(pl.paths.Folder)
	js.mu.Lock()
	defer js.mu.Unlock()
	for _, j := range js.all {
		if j.Folder == folder {
			j.mu.Lock()
			busy := !j.over()
			j.mu.Unlock()
			if busy {
				return nil, errBusy
			}
		}
	}
	id := make([]byte, 8)
	rand.Read(id)
	ctx, cancel := context.WithCancel(js.s.ctx)
	j := &job{ID: hex.EncodeToString(id), Folder: folder, state: "queued",
		subs: map[chan struct{}]struct{}{}, cancel: cancel, finished: make(chan struct{})}
	js.byID[j.ID] = j
	js.all = append(js.all, j)
	js.prune()
	js.s.wg.Add(1)
	go js.run(ctx, j, pl)
	return j, nil
}

// prune forgets the oldest finished jobs beyond keepFinished.
func (js *jobs) prune() {
	finished := 0
	for i := len(js.all) - 1; i >= 0; i-- {
		j := js.all[i]
		j.mu.Lock()
		over := j.over()
		j.mu.Unlock()
		if over {
			if finished++; finished > keepFinished {
				delete(js.byID, j.ID)
				js.all = append(js.all[:i], js.all[i+1:]...)
			}
		}
	}
}

func (js *jobs) run(ctx context.Context, j *job, pl plan) {
	defer js.s.wg.Done()
	defer close(j.finished)
	defer j.cancel()
	select {
	case js.sem <- struct{}{}:
		defer func() { <-js.sem }()
	case <-ctx.Done():
		j.update(func() { j.state = "cancelled" })
		return
	}
	j.update(func() { j.state = "running" })
	lw := &lineWriter{fn: func(line string) {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "== ") {
			return
		}
		j.update(func() {
			j.log = append(j.log, line)
			if w, ok := strings.CutPrefix(line, "WARNING: "); ok {
				j.warnings = append(j.warnings, w)
			}
		})
	}}
	if sn := readState(pl.paths.Folder).SourceName; sn != "" {
		pl.opt.OriginalName = stemOf(sn)
	}
	p := &normalizer.Pipeline{P: pl.paths, Opt: pl.opt, FF: js.s.cfg.FFmpeg, Out: lw,
		OnStep:     func(t string) { j.update(func() { j.steps = append(j.steps, t); j.progress = 0 }) },
		OnProgress: func(f float64) { j.update(func() { j.progress = f }) },
	}
	// The run moves the recording into original/; its analysis moves with it.
	var oldKey string
	if raw := pl.paths.Source(); raw != "" {
		oldKey, _ = cacheKey(raw)
	}
	began := time.Now()
	err := p.Run(ctx)
	lw.flush()
	js.s.recordOutputs(pl, began)
	if oldKey != "" {
		if raw := pl.paths.Source(); raw != "" {
			js.s.analyses.carry(oldKey, raw)
		}
	}
	var outs []outputFileInfo
	on := map[string]bool{"flac": pl.opt.Outputs.Flac, "mp3": pl.opt.Outputs.Mp3, "opus": pl.opt.Outputs.Opus}
	for _, o := range outputsOf(pl.paths) {
		if fi := js.s.fileInfo(o.path); on[o.format] && fi != nil {
			outs = append(outs, outputFileInfo{*fi, o.format})
		}
	}
	j.update(func() {
		j.outputs = outs
		switch {
		case err == nil:
			j.state = "done"
		case errors.Is(err, context.Canceled):
			j.state = "cancelled"
		default:
			j.state, j.err = "failed", err.Error()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		js.s.cfg.Logger.Printf("job %s (%s): %v", j.ID, j.Folder, err)
	}
}

// lineWriter turns writes into whole lines; the report is written in pieces.
type lineWriter struct {
	fn  func(string)
	buf []byte
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		w.fn(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	var q runRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pl := s.check(abs, q)
	if pl.Error != "" {
		writeError(w, http.StatusBadRequest, errors.New(pl.Error))
		return
	}
	j, err := s.jobs.start(pl)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j.snapshot())
}

func (s *Server) jobParam(w http.ResponseWriter, r *http.Request) (*job, bool) {
	j := s.jobs.get(r.PathValue("id"))
	if j == nil {
		writeError(w, http.StatusNotFound, errors.New("no such job"))
		return nil, false
	}
	return j, true
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	if j, ok := s.jobParam(w, r); ok {
		writeJSON(w, http.StatusOK, j.snapshot())
	}
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobParam(w, r)
	if !ok {
		return
	}
	j.cancel()
	<-j.finished // cleaned up: the reply is the final state
	writeJSON(w, http.StatusOK, j.snapshot())
}

// heartbeat keeps idle event streams alive through proxies.
var heartbeat = 20 * time.Second

// handleJobEvents streams the job as server-sent events: the whole snapshot
// on every change (coalesced), until the job is over.
func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobParam(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	wake, unsubscribe := j.subscribe()
	defer unsubscribe()
	send := func() bool {
		snap := j.snapshot()
		b, _ := json.Marshal(snap)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		return snap.State == "done" || snap.State == "failed" || snap.State == "cancelled"
	}
	if send() {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-wake:
			if send() {
				return
			}
			time.Sleep(100 * time.Millisecond) // at most ~10 updates a second
		case <-tick.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// fileExists is a small helper for the inbox.
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
