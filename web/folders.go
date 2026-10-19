package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// folderEntry is one row in the folder list.
type folderEntry struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Date    bool     `json:"date"` // a date folder: has sources or outputs
	Raw     bool     `json:"raw"`
	Outputs []string `json:"outputs"`
	Pending bool     `json:"pending"`    // has sources but not all outputs
	Convert bool     `json:"converting"` // the raw WAV is being converted to FLAC
	Prepare bool     `json:"preparing"`  // the waveform and preview are being made
	Job     string   `json:"job,omitempty"`
}

type output struct {
	format, path string
}

func outputsOf(p normalizer.Paths) []output {
	return []output{{"flac", p.Master}, {"mp3", p.Mp3}, {"opus", p.Opus}}
}

func (s *Server) entry(abs string) folderEntry {
	e := folderEntry{Name: filepath.Base(abs), Path: s.relPath(abs), Outputs: []string{}}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		return e
	}
	raw := paths.Source()
	e.Raw = raw != ""
	if e.Raw {
		stage := s.analyses.stage(raw)
		e.Convert, e.Prepare = stage == "convert", stage != "" && stage != "convert"
	}
	for _, o := range outputsOf(paths) {
		if _, err := os.Stat(o.path); err == nil {
			e.Outputs = append(e.Outputs, o.format)
		}
	}
	e.Date = e.Raw || len(e.Outputs) > 0
	e.Pending = e.Raw && len(e.Outputs) < 3
	if j := s.jobs.active(e.Path); j != nil {
		e.Job = j.ID
	}
	return e
}

func (s *Server) handleFolders(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("no such folder"))
		return
	}
	entries := []folderEntry{}
	for _, de := range des {
		if de.IsDir() && !strings.HasPrefix(de.Name(), ".") {
			entries = append(entries, s.entry(filepath.Join(abs, de.Name())))
		}
	}
	// Date folders first, then others (years); newest (highest name) first.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date
		}
		return entries[i].Name > entries[j].Name
	})
	resp := map[string]any{"path": s.relPath(abs), "entries": entries}
	if abs != s.root {
		resp["parent"] = s.relPath(filepath.Dir(abs))
	}
	writeJSON(w, http.StatusOK, resp)
}

// folderInfo is everything the cut page needs about a date folder.
type folderInfo struct {
	Path      string           `json:"path"`
	Date      string           `json:"date"`
	Raw       *fileInfo        `json:"raw"`
	Outputs   []outputFileInfo `json:"outputs"`
	Job       *jobSnapshot     `json:"job"`
	Tags      *Meta            `json:"tags"` // the church's defaults filled in for this folder
	TagsError string           `json:"tagsError,omitempty"`
	Sermon    *sermonFile      `json:"sermon"` // the saved and adjusted settings of this sermon
}

type outputFileInfo struct {
	fileInfo
	Format string `json:"format"`
}

func (s *Server) folderInfo(abs string) (folderInfo, error) {
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		return folderInfo{}, err
	}
	info := folderInfo{Path: s.relPath(abs), Date: paths.Date, Outputs: []outputFileInfo{}, Sermon: readSermon(abs)}
	if raw := paths.Source(); raw != "" {
		info.Raw = s.fileInfo(raw)
	}
	for _, o := range outputsOf(paths) {
		if fi := s.fileInfo(o.path); fi != nil {
			info.Outputs = append(info.Outputs, outputFileInfo{*fi, o.format})
		}
	}
	if j := s.jobs.latest(info.Path); j != nil {
		snap := j.snapshot()
		info.Job = &snap
	}
	if tags, _, err := s.defaultTags(abs); err != nil {
		info.TagsError = err.Error()
	} else {
		info.Tags = &tags
	}
	return info, nil
}

func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusNotFound, errors.New("no such folder"))
		return
	}
	info, err := s.folderInfo(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// span is a part of the recording, in seconds.
type span struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// cutting is what of the recording becomes the sermon: the sections, in
// order, without the cuts. One section with cuts and several sections give
// the same kind of result; the gaps between sections are left out like cuts.
type cutting struct {
	Sections []span `json:"sections"`
	Cuts     []span `json:"cuts"`
}

// normalize sorts the sections and merges those that overlap or touch, and
// sorts the cuts. Empty lists are [] in JSON.
func (c *cutting) normalize() {
	sort.Slice(c.Sections, func(i, j int) bool { return c.Sections[i].Start < c.Sections[j].Start })
	merged := []span{}
	for _, x := range c.Sections {
		if n := len(merged); n > 0 && x.Start <= merged[n-1].End {
			merged[n-1].End = math.Max(merged[n-1].End, x.End)
		} else {
			merged = append(merged, x)
		}
	}
	c.Sections = merged
	if c.Cuts == nil {
		c.Cuts = []span{}
	}
	sort.Slice(c.Cuts, func(i, j int) bool { return c.Cuts[i].Start < c.Cuts[j].Start })
}

// apply checks the sections and cuts and puts them in o the way the pipeline
// takes them: from the first section's start to the last one's end, with the
// gaps between sections and the cuts left out. c must be normalized.
func (c cutting) apply(o *normalizer.Options) error {
	for _, x := range c.Sections {
		if x.Start < 0 || x.End-x.Start < minSpan {
			return errors.New("a section must end after it starts")
		}
	}
	if len(c.Sections) == 0 {
		if len(c.Cuts) > 0 {
			return errors.New("select the sermon before cutting parts out of it")
		}
		o.Start, o.End, o.Cuts = 0, 0, nil
		return nil
	}
	first, last := c.Sections[0], c.Sections[len(c.Sections)-1]
	o.Start, o.End, o.Cuts = first.Start, last.End, nil
	for i := 1; i < len(c.Sections); i++ {
		o.Cuts = append(o.Cuts, normalizer.Span{Start: c.Sections[i-1].End, End: c.Sections[i].Start})
	}
	for _, x := range c.Cuts {
		switch {
		case x.Start < 0 || x.End-x.Start < minSpan:
			return errors.New("a cut must end after it starts")
		case x.Start < first.Start || x.End > last.End:
			return errors.New("a cut must be inside the sermon")
		}
		o.Cuts = append(o.Cuts, normalizer.Span{Start: x.Start, End: x.End})
	}
	return nil
}

// minSpan is the shortest section or cut (seconds), as in the editor.
const minSpan = 0.05

// idleCuts are the cuts that fall wholly between sections, so leave nothing out.
func (c cutting) idleCuts() int {
	n := 0
	for _, x := range c.Cuts {
		inside := false
		for _, s := range c.Sections {
			if x.Start < s.End && x.End > s.Start {
				inside = true
			}
		}
		if !inside {
			n++
		}
	}
	return n
}

// runRequest is the cut and the options, as sent by the front end.
type runRequest struct {
	cutting
	Flac           bool    `json:"flac"`
	Mp3            bool    `json:"mp3"`
	Opus           bool    `json:"opus"`
	Stereo         bool    `json:"stereo"`
	FadeIn         float64 `json:"fadeIn"`
	FadeOut        float64 `json:"fadeOut"`
	NoiseReduction int     `json:"noiseReduction"`
	TargetLUFS     float64 `json:"targetLufs"`
	TruePeak       float64 `json:"truePeak"`
	Mp3Bitrate     string  `json:"mp3Bitrate"`
	OpusBitrate    string  `json:"opusBitrate"`
	Force          bool    `json:"force"`
	// Tags for this run; nil = the church's defaults for the folder, "" = no tag.
	Title  *string `json:"title"`
	Artist *string `json:"artist"`
	Album  *string `json:"album"`
}

// requestFrom is the defaults of a run, without a sermon selected.
func requestFrom(o normalizer.Options) runRequest {
	return runRequest{
		cutting: cutting{Sections: []span{}, Cuts: []span{}},
		Flac:    o.Outputs.Flac, Mp3: o.Outputs.Mp3, Opus: o.Outputs.Opus,
		Stereo: o.Stereo, FadeIn: o.FadeIn, FadeOut: o.FadeOut,
		NoiseReduction: o.NoiseReduction, TargetLUFS: o.TargetLUFS, TruePeak: o.TruePeak,
		Mp3Bitrate: o.Mp3Bitrate, OpusBitrate: o.OpusBitrate,
		Force: o.Force,
	}
}

func (q runRequest) options() (normalizer.Options, error) {
	o := normalizer.Options{
		Outputs: normalizer.Outputs{Flac: q.Flac, Mp3: q.Mp3, Opus: q.Opus},
		Stereo:  q.Stereo, FadeIn: q.FadeIn, FadeOut: q.FadeOut,
		NoiseReduction: q.NoiseReduction, TargetLUFS: q.TargetLUFS, TruePeak: q.TruePeak,
		Mp3Bitrate: strings.TrimSpace(q.Mp3Bitrate), OpusBitrate: strings.TrimSpace(q.OpusBitrate),
		Force: q.Force,
	}
	q.normalize()
	return o, q.apply(&o)
}

// plan is a checked run: the options, or why it cannot run, plus notices.
type plan struct {
	Error   string   `json:"error,omitempty"`
	Notices []string `json:"notices"`

	opt   normalizer.Options
	paths normalizer.Paths
}

// check validates a run for a folder the way the pipeline would see it,
// so the front end can show problems before anything starts.
func (s *Server) check(abs string, q runRequest) plan {
	pl := plan{Notices: []string{}}
	fail := func(err error) plan { pl.Error = err.Error(); return pl }
	var err error
	if pl.opt, err = q.options(); err != nil {
		return fail(err)
	}
	if err := pl.opt.Validate(); err != nil {
		return fail(err)
	}
	if n := q.idleCuts(); n == 1 {
		pl.Notices = append(pl.Notices, "A cut is between two sections, outside the sermon: it leaves nothing out.")
	} else if n > 1 {
		pl.Notices = append(pl.Notices, fmt.Sprintf("%d cuts are between sections, outside the sermon: they leave nothing out.", n))
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		return fail(err)
	}
	pl.paths = paths
	var note string
	if pl.opt.Tags, note = s.runTags(abs, q); note != "" {
		pl.Notices = append(pl.Notices, note)
	}
	raw := paths.Source()
	switch {
	case raw == "":
		return fail(errors.New("there is no raw recording to cut"))
	case pl.opt.Start == 0 && pl.opt.End == 0:
		return fail(errors.New("select where the sermon starts and ends"))
	case s.analyses.converting(raw):
		return fail(errors.New("the recording is still being converted to FLAC"))
	}
	if !pl.opt.Force {
		var existing []string
		on := map[string]bool{"flac": q.Flac, "mp3": q.Mp3, "opus": q.Opus}
		for _, o := range outputsOf(paths) {
			if _, err := os.Stat(o.path); on[o.format] && err == nil {
				existing = append(existing, filepath.Base(o.path))
			}
		}
		if len(existing) > 0 {
			pl.Notices = append(pl.Notices, "Already there, kept: "+strings.Join(existing, ", ")+". Save and generate makes them again.")
		}
	}
	return pl
}

func decodeRequest(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	var q runRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.check(abs, q))
}
