package web

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// sermonSettings are what the editor keeps of a sermon: its sections of the
// recording, the parts cut out of them, and its fades.
type sermonSettings struct {
	cutting
	FadeIn  float64 `json:"fadeIn"`
	FadeOut float64 `json:"fadeOut"`
}

// sermonFile is the sermon's part of recording.json: the editor keeps the
// adjustments as you make them; saving makes them the saved version, and
// reverting drops them. It is also what the sermon API returns.
type sermonFile struct {
	Saved    *sermonSettings `json:"saved"`
	SavedAt  *time.Time      `json:"savedAt,omitempty"`
	Adjusted *sermonSettings `json:"adjusted"` // nil when it is the same as Saved
}

// Validate checks the settings the way a run would. c must be normalized.
func (c sermonSettings) Validate() error {
	if len(c.Sections) == 0 {
		return errors.New("select the sermon: drag on the waveform")
	}
	o := normalizer.DefaultOptions()
	o.FadeIn, o.FadeOut = c.FadeIn, c.FadeOut
	if err := c.apply(&o); err != nil {
		return err
	}
	return o.Validate()
}

// readSermon returns the sermon of a folder, or nil if nothing is kept.
func readSermon(folder string) *sermonFile {
	f := readState(folder).sermonFile
	if f.Saved == nil && f.Adjusted == nil {
		return nil
	}
	return &f
}

// changeSermon changes the sermon in the folder's recording.json and returns it.
func (s *Server) changeSermon(w http.ResponseWriter, r *http.Request, fn func(f *sermonFile) error) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("there is no such folder"))
		return
	}
	st, err := s.updateState(abs, func(st *folderState) error { return fn(&st.sermonFile) })
	switch {
	case errors.Is(err, errStateWrite):
		writeError(w, http.StatusInternalServerError, errors.New("could not save in the folder"))
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
	default:
		writeJSON(w, http.StatusOK, st.sermonFile)
	}
}

// PUT api/sermon keeps the adjustments the editor has made.
func (s *Server) handleSermonPut(w http.ResponseWriter, r *http.Request) {
	var c sermonSettings
	if err := decodeRequest(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	c.normalize()
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.changeSermon(w, r, func(f *sermonFile) error {
		if f.Saved != nil && reflect.DeepEqual(*f.Saved, c) {
			f.Adjusted = nil
		} else {
			f.Adjusted = &c
		}
		return nil
	})
}

// POST api/sermon/save makes the adjustments the saved version.
func (s *Server) handleSermonSave(w http.ResponseWriter, r *http.Request) {
	s.changeSermon(w, r, func(f *sermonFile) error {
		if f.Adjusted == nil {
			if f.Saved == nil {
				return errors.New("there is nothing to save yet")
			}
			return nil // already saved
		}
		now := time.Now().UTC().Truncate(time.Second)
		f.Saved, f.SavedAt, f.Adjusted = f.Adjusted, &now, nil
		return nil
	})
}

// POST api/sermon/revert drops the adjustments, back to the saved version.
func (s *Server) handleSermonRevert(w http.ResponseWriter, r *http.Request) {
	s.changeSermon(w, r, func(f *sermonFile) error {
		if f.Saved == nil {
			return errors.New("there is no saved version to go back to")
		}
		f.Adjusted = nil
		return nil
	})
}
