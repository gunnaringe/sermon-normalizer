package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

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
		writeError(w, http.StatusBadRequest, errors.New("this sermon is not saved yet: open it in the editor and press Save"))
		return
	}
	q := s.processing().start() // for one saved before the studio kept its sound
	q.cutting = sf.Saved.cutting
	q.FadeIn, q.FadeOut = sf.Saved.FadeIn, sf.Saved.FadeOut
	q.Intro, q.Outro = sf.Saved.Intro, sf.Saved.Outro
	sf.Saved.Sound.applyTo(&q)
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

// handleRemoveOutputs deletes outputs of a sermon (format=flac, mp3, opus;
// repeated for several) and forgets how they were made. Only the outputs: the
// recording and its original are never touched. Not while a run is busy.
func (s *Server) handleRemoveOutputs(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	formats := r.URL.Query()["format"]
	if len(formats) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("which format?"))
		return
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	byFormat, sermonOnly := map[string]string{}, map[string]string{}
	for _, o := range outputsOf(paths) {
		byFormat[o.format] = o.path
	}
	for _, o := range sermonOnlyOutputs(paths) {
		sermonOnly[baseFormat(o.format)] = o.path
	}
	for _, f := range formats {
		if byFormat[f] == "" {
			writeError(w, http.StatusBadRequest, errors.New("format must be flac, mp3 or opus"))
			return
		}
	}
	if s.jobs.active(s.relPath(abs)) != nil {
		writeError(w, http.StatusConflict, errBusy)
		return
	}
	for _, f := range formats {
		for _, p := range []string{byFormat[f], sermonOnly[f]} { // with the intro and outro, and the sermon only
			if err := normalizer.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusInternalServerError, errors.New("could not delete "+filepath.Base(p)))
				return
			} else if err == nil {
				s.cfg.Logger.Printf("deleted %s", s.display(p))
			}
		}
	}
	if _, err := s.updateState(abs, func(st *folderState) error {
		for _, f := range formats {
			delete(st.Outputs, f)
			delete(st.Outputs, f+normalizer.SermonOnly)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	info, err := s.folderInfo(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}
