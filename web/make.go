package web

import (
	"errors"
	"net/http"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// handleMake makes one missing format (flac, mp3 or opus) of a sermon from its
// saved version, with the default levels and bitrates. Files that exist are
// kept: it is an ordinary run with only that format on and no Force.
func (s *Server) handleMake(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format != "flac" && format != "mp3" && format != "opus" {
		writeError(w, http.StatusBadRequest, errors.New("format must be flac, mp3 or opus"))
		return
	}
	sf := readSermon(abs)
	if sf == nil || sf.Saved == nil {
		writeError(w, http.StatusBadRequest, errors.New("this sermon is not saved yet: open it in the editor, select the sermon and use Save"))
		return
	}
	q := requestFrom(normalizer.DefaultOptions())
	q.cutting = sf.Saved.cutting
	q.FadeIn, q.FadeOut = sf.Saved.FadeIn, sf.Saved.FadeOut
	q.Flac, q.Mp3, q.Opus = format == "flac", format == "mp3", format == "opus"
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
