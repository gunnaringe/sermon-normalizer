package web

import (
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// sampleSeconds is how much of the recording a sample is.
const sampleSeconds = 30

// handleSample makes a sample to hear what the sound settings do: POST api/sample?path=<date folder>&at=<seconds>
// with the settings as for a run; the answer is an MP3 of sampleSeconds from the recording at that time,
// made as the files are (the cuts, pauses and fades are not applied). It is short enough to be done in the
// request, and only when no other conversion is running.
func (s *Server) handleSample(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	var q runRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	q.Flac, q.Mp3, q.Opus = false, true, false
	q.cutting = cutting{Sections: []span{}, Cuts: []span{}}
	o, err := q.options()
	if err == nil {
		err = o.Validate()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	raw := paths.Source()
	switch {
	case raw == "":
		writeError(w, http.StatusNotFound, errors.New("there is no raw recording to listen to"))
		return
	case s.analyses.stage(raw) != "":
		writeError(w, http.StatusConflict, errors.New("the recording is still being converted or prepared; try again in a moment"))
		return
	}
	at, _ := strconv.ParseFloat(r.URL.Query().Get("at"), 64)
	if dur, err := s.cfg.FFmpeg.Duration(r.Context(), raw); err == nil && at > dur-sampleSeconds {
		at = dur - sampleSeconds
	}
	if math.IsNaN(at) || at < 0 {
		at = 0
	}
	select {
	case s.limits.encode <- struct{}{}:
		defer func() { <-s.limits.encode }()
	default:
		writeError(w, http.StatusConflict, errors.New("another conversion is running; try again when it is done"))
		return
	}
	tmp, err := os.CreateTemp(s.cfg.CacheDir, "sample-*.mp3")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tmp.Close()
	defer normalizer.Remove(tmp.Name())
	if err := s.cfg.FFmpeg.Sample(r.Context(), raw, o, at, sampleSeconds, tmp.Name()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "sample.mp3", time.Now(), f)
}
