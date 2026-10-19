package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
)

// NameList is a church's remembered sermon names, offered as suggestions
// when naming a sermon (so "Kveld" is not typed three ways).
type NameList struct {
	Enabled bool     `json:"enabled"`
	List    []string `json:"list"`
}

const maxNames = 200

// defaultNames: suggestions are on, with nothing remembered yet.
var defaultNames = NameList{Enabled: true, List: []string{}}

type nameStore struct {
	mu   sync.Mutex
	v    NameList
	save func(NameList) error // nil = memory only
}

func (n *nameStore) get() NameList {
	n.mu.Lock()
	defer n.mu.Unlock()
	return NameList{Enabled: n.v.Enabled, List: slices.Clone(n.v.List)}
}

// set stores a new list; the caller holds n.mu.
func (n *nameStore) set(v NameList) error {
	if n.save != nil {
		if err := n.save(v); err != nil {
			return err
		}
	}
	n.v = v
	return nil
}

// tidyNames cleans a list: each name checked, duplicates (ignoring case)
// dropped, sorted.
func tidyNames(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in {
		name, err := cleanName(raw)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", raw, err)
		}
		if key := strings.ToLower(name); name != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	if len(out) > maxNames {
		return nil, fmt.Errorf("at most %d names", maxNames)
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out, nil
}

// learnName remembers a name used for a sermon, if suggestions are on.
func (s *Server) learnName(name string) {
	if name == "" {
		return
	}
	s.names.mu.Lock()
	defer s.names.mu.Unlock()
	if !s.names.v.Enabled {
		return
	}
	list, err := tidyNames(append(slices.Clone(s.names.v.List), name))
	if err == nil && len(list) != len(s.names.v.List) {
		if err = s.names.set(NameList{Enabled: true, List: list}); err != nil {
			s.cfg.Logger.Printf("names: %v", err)
		}
	}
}

func (s *Server) handleNames(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.names.get())
}

func (s *Server) handleNamesPut(w http.ResponseWriter, r *http.Request) {
	var q NameList
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	list, err := tidyNames(q.List)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.names.mu.Lock()
	defer s.names.mu.Unlock()
	v := NameList{Enabled: q.Enabled, List: list}
	if err := s.names.set(v); err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("could not save: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, v)
}
