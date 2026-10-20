package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	Convert bool     `json:"converting"` // the recording is being converted (normalizer.ConvertsTo)
	Prepare bool     `json:"preparing"`  // the waveform and preview are being made
	Job     string   `json:"job,omitempty"`
	// Saved: the editor has a saved version (the recording is done); Outdated:
	// some of its files were made from another cut than that.
	Saved    bool `json:"saved"`
	Outdated bool `json:"outdated"`
	// Imported is when the recording arrived, if that is known.
	Imported *time.Time `json:"imported,omitempty"`
	Labels   []string   `json:"labels"`
}

// newFor is how long a recording stays among the new ones, on top of the
// recordings page, after it arrived (the page does the filtering).
const newFor = 7 * 24 * time.Hour

type output struct {
	format, path string
}

func outputsOf(p normalizer.Paths) []output {
	return []output{{"flac", p.Master}, {"mp3", p.Mp3}, {"opus", p.Opus}}
}

func (s *Server) entry(abs string) folderEntry {
	e := folderEntry{Name: filepath.Base(abs), Path: s.relPath(abs), Outputs: []string{}, Labels: []string{}}
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
	if e.Date {
		st := readState(abs)
		e.Imported = st.Imported
		e.Labels = append(e.Labels, st.Labels...)
		if st.Saved != nil {
			e.Saved = true
			cut := savedCut(*st.Saved)
			for _, m := range st.Outputs {
				e.Outdated = e.Outdated || (m.Cut != "" && m.Cut != cut)
			}
		}
	}
	return e
}

// allRecordings are all the date folders under the root (not in original/ or
// hidden ones), newest first by name: every recording of the church, for the
// recordings page to group, search and filter.
func (s *Server) allRecordings() []folderEntry {
	out := []folderEntry{}
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != s.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "original") {
			return filepath.SkipDir
		}
		if e := s.entry(path); e.Date {
			out = append(out, e)
			return filepath.SkipDir // a recording folder holds no other recordings
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"recordings": s.allRecordings(), "newFor": int(newFor.Seconds()), "trashDays": s.trashDays()})
}

const (
	maxLabels      = 10
	maxLabelLength = 30
)

// cleanLabels tidies a recording's labels: spaces collapsed, empty ones and
// repeats (any case) dropped; too many or too long ones are refused.
func cleanLabels(in []string) ([]string, error) {
	out := []string{}
	for _, l := range in {
		l = strings.Join(strings.Fields(l), " ")
		switch {
		case l == "":
			continue
		case strings.ContainsFunc(l, func(r rune) bool { return r < ' ' }):
			return nil, errors.New("a label cannot hold control characters")
		case utf8.RuneCountInString(l) > maxLabelLength:
			return nil, fmt.Errorf("a label can be at most %d characters", maxLabelLength)
		case slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, l) }):
			continue
		}
		out = append(out, l)
	}
	if len(out) > maxLabels {
		return nil, fmt.Errorf("a recording can have at most %d labels", maxLabels)
	}
	return out, nil
}

// handleLabelsPut sets a recording's labels: PUT api/labels?path=<date folder>, {"labels": [...]}.
func (s *Server) handleLabelsPut(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	if !s.entry(abs).Date {
		writeError(w, http.StatusNotFound, errors.New("no recording there"))
		return
	}
	var q struct {
		Labels []string `json:"labels"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	labels, err := cleanLabels(q.Labels)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.updateState(abs, func(st *folderState) error { st.Labels = labels; return nil }); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.entry(abs))
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
	// Recording is what the recording is (format, metadata, channels), once
	// it has been looked at; nil while that is still to come.
	Recording     *recordingFacts `json:"recording"`
	ConvertedFrom *sourceFile     `json:"convertedFrom"`
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
	st := readState(abs)
	info.ConvertedFrom = st.ConvertedFrom
	if raw := paths.Source(); raw != "" {
		info.Raw = s.fileInfo(raw)
		if factsFresh(st, raw) {
			info.Recording = st.Recording
		}
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
	// Label is the user's own note on a section or cut ("Intro", "Song"). It
	// changes nothing in the files, so it is not part of what makes them out of date.
	Label string `json:"label,omitempty"`
	// Pause is silence (seconds) put into the sermon where a section ends, or where a cut starts.
	Pause float64 `json:"pause,omitempty"`
}

// maxSpanLabel is the longest label of a section or cut, in characters.
const maxSpanLabel = 60

// cutting is what of the recording becomes the sermon: the sections, in
// order, without the cuts. One section with cuts and several sections give
// the same kind of result; the gaps between sections are left out like cuts.
type cutting struct {
	Sections []span `json:"sections"`
	Cuts     []span `json:"cuts"`
}

// normalize sorts the sections and merges those that overlap (ones that only touch stay two: a
// pause or a label can be between them), and sorts the cuts. Empty lists are [] in JSON.
func (c *cutting) normalize() {
	sort.Slice(c.Sections, func(i, j int) bool { return c.Sections[i].Start < c.Sections[j].Start })
	for i := range c.Sections {
		c.Sections[i].Label = strings.Join(strings.Fields(c.Sections[i].Label), " ")
	}
	for i := range c.Cuts {
		c.Cuts[i].Label = strings.Join(strings.Fields(c.Cuts[i].Label), " ")
	}
	merged := []span{}
	for _, x := range c.Sections {
		if n := len(merged); n > 0 && x.Start < merged[n-1].End {
			if x.End >= merged[n-1].End {
				merged[n-1].Pause = x.Pause // the pause is where the section ends
			}
			merged[n-1].End = math.Max(merged[n-1].End, x.End)
			if merged[n-1].Label == "" {
				merged[n-1].Label = x.Label
			}
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
	for _, x := range append(slices.Clone(c.Sections), c.Cuts...) {
		if utf8.RuneCountInString(x.Label) > maxSpanLabel {
			return fmt.Errorf("a label can be at most %d characters", maxSpanLabel)
		}
		if strings.ContainsFunc(x.Label, unicode.IsControl) {
			return errors.New("a label cannot hold control characters")
		}
		if !(x.Pause >= 0 && x.Pause <= normalizer.MaxPause) {
			return fmt.Errorf("a pause can be from 0 to %g seconds", normalizer.MaxPause)
		}
	}
	for _, x := range c.Sections {
		if x.Start < 0 || x.End-x.Start < minSpan {
			return errors.New("a section must end after it starts")
		}
	}
	if len(c.Sections) == 0 {
		if len(c.Cuts) > 0 {
			return errors.New("select the sermon before cutting parts out of it")
		}
		o.Start, o.End, o.Cuts, o.Pauses = 0, 0, nil, nil
		return nil
	}
	first, last := c.Sections[0], c.Sections[len(c.Sections)-1]
	o.Start, o.End, o.Cuts = first.Start, last.End, nil
	o.Pauses = nil
	for i := 1; i < len(c.Sections); i++ {
		if c.Sections[i-1].End < c.Sections[i].Start { // sections that touch have no gap
			o.Cuts = append(o.Cuts, normalizer.Span{Start: c.Sections[i-1].End, End: c.Sections[i].Start})
		}
		if p := c.Sections[i-1].Pause; p > 0 {
			o.Pauses = append(o.Pauses, normalizer.Pause{At: c.Sections[i-1].End, Seconds: p})
		}
	}
	for _, x := range c.Cuts {
		if x.Start < 0 || x.End-x.Start < minSpan {
			return errors.New("a cut must end after it starts")
		}
		// What is outside the sermon is left out anyway: a cut there changes nothing
		// (the editor warns about it), and one that sticks out is cut at the edge.
		start, end := math.Max(x.Start, first.Start), math.Min(x.End, last.End)
		if end-start < minSpan {
			continue
		}
		o.Cuts = append(o.Cuts, normalizer.Span{Start: start, End: end})
		if x.Pause > 0 && start > first.Start {
			o.Pauses = append(o.Pauses, normalizer.Pause{At: start, Seconds: x.Pause})
		}
	}
	return nil
}

// minSpan is the shortest section or cut (seconds), as in the editor.
const minSpan = 0.05

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
	Outputs map[string]outputState `json:"outputs,omitempty"` // when it can run
	Error   string                 `json:"error,omitempty"`
	Notices []string               `json:"notices"`

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
	// A cut that is in no section changes nothing: the editor warns by the cut itself.
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
		return fail(errors.New("the recording is still being converted"))
	}
	pl.Outputs = s.outputStatus(paths, pl.opt)
	return pl
}

// outputState is an output as the page sees it: whether it is there, and
// whether a run with the page's settings would make it differently. Known is
// false for a file made before the studio kept track (or by hand).
type outputState struct {
	Exists bool `json:"exists"`
	Known  bool `json:"known"`
	Stale  bool `json:"stale"`
}

func (s *Server) outputStatus(paths normalizer.Paths, o normalizer.Options) map[string]outputState {
	made := readState(paths.Folder).Outputs
	out := map[string]outputState{}
	for _, x := range outputsOf(paths) {
		var st outputState
		if _, err := os.Stat(x.path); err == nil {
			m, ok := made[x.format]
			st = outputState{Exists: true, Known: ok, Stale: ok && m.From != madeFrom(o, x.format)}
		}
		out[x.format] = st
	}
	return out
}

// recordOutputs keeps in recording.json how the outputs a run wrote (those
// changed since it began) were made, also when it failed half-way.
func (s *Server) recordOutputs(pl plan, began time.Time) {
	on := map[string]bool{"flac": pl.opt.Outputs.Flac, "mp3": pl.opt.Outputs.Mp3, "opus": pl.opt.Outputs.Opus}
	made := map[string]outputMade{}
	for _, x := range outputsOf(pl.paths) {
		if st, err := os.Stat(x.path); err == nil && on[x.format] && !st.ModTime().Before(began.Add(-time.Second)) {
			made[x.format] = outputMade{MadeAt: st.ModTime().UTC(), From: madeFrom(pl.opt, x.format), Cut: cutOf(pl.opt),
				Stereo: pl.opt.Stereo, Bitrate: bitrateFor(pl.opt, x.format)}
		}
	}
	if len(made) == 0 {
		return
	}
	if _, err := s.updateState(pl.paths.Folder, func(st *folderState) error {
		for f, m := range made {
			st.Outputs[f] = m
		}
		return nil
	}); err != nil {
		s.cfg.Logger.Printf("%s: %v", s.display(pl.paths.Folder), err)
	}
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
