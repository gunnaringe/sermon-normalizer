package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// sermonSettings are what the editor keeps of a sermon: where it starts and
// ends in the recording, the parts cut out of it, and its fades.
type sermonSettings struct {
	Start   float64   `json:"start"`
	End     float64   `json:"end"`
	Cuts    []cutSpan `json:"cuts"`
	FadeIn  float64   `json:"fadeIn"`
	FadeOut float64   `json:"fadeOut"`
}

// sermonFile is <folder>/sermon.json. The editor keeps the adjustments as you
// make them; saving makes them the saved version, and reverting drops them.
type sermonFile struct {
	Saved    *sermonSettings `json:"saved"`
	SavedAt  *time.Time      `json:"savedAt,omitempty"`
	Adjusted *sermonSettings `json:"adjusted"` // nil when it is the same as Saved
}

const sermonName = "sermon.json"

func sermonPath(folder string) string { return filepath.Join(folder, sermonName) }

// legacyCutsPath is where an earlier version kept the selection and cuts.
func legacyCutsPath(folder string) string {
	return filepath.Join(folder, filepath.Base(folder)+"_cuts.json")
}

// Validate checks the settings the way a run would.
func (c sermonSettings) Validate() error {
	if c.End <= 0 {
		return errors.New("select where the sermon starts and ends")
	}
	o := normalizer.DefaultOptions()
	o.Start, o.End, o.FadeIn, o.FadeOut = c.Start, c.End, c.FadeIn, c.FadeOut
	for _, x := range c.Cuts {
		o.Cuts = append(o.Cuts, normalizer.Span{Start: x.Start, End: x.End})
	}
	return o.Validate()
}

func (c *sermonSettings) normalize() {
	if c != nil && c.Cuts == nil {
		c.Cuts = []cutSpan{}
	}
}

// readSermon returns a folder's sermon.json, or nil if there is none (or it
// cannot be read: the next change replaces it). An earlier version's
// <date>_cuts.json counts as the saved version.
func readSermon(folder string) *sermonFile {
	if data, err := os.ReadFile(sermonPath(folder)); err == nil {
		var f sermonFile
		if json.Unmarshal(data, &f) != nil {
			return nil
		}
		for _, c := range []*sermonSettings{f.Saved, f.Adjusted} {
			if c != nil && c.Validate() != nil {
				return nil
			}
			c.normalize()
		}
		return &f
	}
	data, err := os.ReadFile(legacyCutsPath(folder))
	if err != nil {
		return nil
	}
	var c sermonSettings
	if json.Unmarshal(data, &c) != nil || c.Validate() != nil {
		return nil
	}
	c.normalize()
	return &sermonFile{Saved: &c}
}

// writeSermon replaces sermon.json (atomically) and drops the old file.
func writeSermon(folder string, f *sermonFile) error {
	data, _ := json.MarshalIndent(f, "", "  ")
	tmp := filepath.Join(folder, "."+sermonName+".partial")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, sermonPath(folder)); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(legacyCutsPath(folder))
	return nil
}

// change reads the folder's sermon.json, applies fn and writes the result.
func (s *Server) changeSermon(w http.ResponseWriter, r *http.Request, fn func(f *sermonFile) error) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("there is no such folder"))
		return
	}
	s.sermonMu.Lock()
	defer s.sermonMu.Unlock()
	f := readSermon(abs)
	if f == nil {
		f = &sermonFile{}
	}
	if err := fn(f); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := writeSermon(abs, f); err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("could not save in the folder"))
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// PUT api/sermon keeps the adjustments the editor has made.
func (s *Server) handleSermonPut(w http.ResponseWriter, r *http.Request) {
	var c sermonSettings
	if err := decodeRequest(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	c.normalize()
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
