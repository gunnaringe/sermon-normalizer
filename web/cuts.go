package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// savedCuts is what the editor remembers of a sermon: where it starts and
// ends in the recording, and the parts cut out of it. It lives in the date
// folder as <date>_cuts.json, so it follows a rename and a move of the folder.
type savedCuts struct {
	Start float64   `json:"start"`
	End   float64   `json:"end"`
	Cuts  []cutSpan `json:"cuts"`
}

func cutsFile(folder string) string {
	return filepath.Join(folder, filepath.Base(folder)+"_cuts.json")
}

// readCuts returns the saved selection of a folder, or nil if there is none
// (or the file cannot be read: the next save replaces it).
func readCuts(folder string) *savedCuts {
	data, err := os.ReadFile(cutsFile(folder))
	if err != nil {
		return nil
	}
	var c savedCuts
	if json.Unmarshal(data, &c) != nil || c.Validate() != nil {
		return nil
	}
	if c.Cuts == nil {
		c.Cuts = []cutSpan{}
	}
	return &c
}

// Validate checks the selection the way a run would.
func (c savedCuts) Validate() error {
	if c.End <= 0 {
		return errors.New("select where the sermon starts and ends")
	}
	o := normalizer.DefaultOptions()
	o.Start, o.End = c.Start, c.End
	for _, x := range c.Cuts {
		o.Cuts = append(o.Cuts, normalizer.Span{Start: x.Start, End: x.End})
	}
	return o.Validate()
}

func (s *Server) handleCutsPut(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	var c savedCuts
	if err := decodeRequest(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("there is no such folder"))
		return
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	tmp := filepath.Join(abs, "."+filepath.Base(abs)+"_cuts.json.partial")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err == nil {
		err = os.Rename(tmp, cutsFile(abs))
		if err == nil {
			writeJSON(w, http.StatusOK, c)
			return
		}
		os.Remove(tmp)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeError(w, http.StatusInternalServerError, errors.New("could not save in the folder"))
}
