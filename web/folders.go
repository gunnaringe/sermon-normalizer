package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// folderEntry is one row in the folder list.
type folderEntry struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Date    bool     `json:"date"` // a date folder: has sources or outputs
	Raw     bool     `json:"raw"`
	Edit    bool     `json:"edit"`
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
	paths, err := normalizer.NewPaths(abs, s.cfg.Archive)
	if err != nil {
		return e
	}
	src := paths.Sources()
	e.Raw, e.Edit = src.Raw != "", src.Edit != ""
	if e.Raw {
		stage := s.analyses.stage(src.Raw)
		e.Convert, e.Prepare = stage == "convert", stage != "" && stage != "convert"
	}
	for _, o := range outputsOf(paths) {
		if _, err := os.Stat(o.path); err == nil {
			e.Outputs = append(e.Outputs, o.format)
		}
	}
	e.Date = e.Raw || e.Edit || len(e.Outputs) > 0
	e.Pending = (e.Raw || e.Edit) && len(e.Outputs) < 3
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
	Path         string           `json:"path"`
	Date         string           `json:"date"`
	Raw          *fileInfo        `json:"raw"`
	Edit         *fileInfo        `json:"edit"`
	EditInFolder bool             `json:"editInFolder"`
	Outputs      []outputFileInfo `json:"outputs"`
	Job          *jobSnapshot     `json:"job"`
	Tags         *Meta            `json:"tags"` // the church's defaults filled in for this folder
	TagsError    string           `json:"tagsError,omitempty"`
}

type outputFileInfo struct {
	fileInfo
	Format string `json:"format"`
}

func (s *Server) folderInfo(abs string) (folderInfo, error) {
	paths, err := normalizer.NewPaths(abs, s.cfg.Archive)
	if err != nil {
		return folderInfo{}, err
	}
	src := paths.Sources()
	info := folderInfo{Path: s.relPath(abs), Date: paths.Date, EditInFolder: src.EditInFolder, Outputs: []outputFileInfo{}}
	if src.Raw != "" {
		info.Raw = s.fileInfo(src.Raw)
	}
	if src.Edit != "" {
		info.Edit = s.fileInfo(src.Edit)
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

// runRequest is the cut and the options, as sent by the front end.
type runRequest struct {
	Start          float64 `json:"start"`
	End            float64 `json:"end"`
	UseEdit        bool    `json:"useEdit"` // use the <date>_edit file instead of cutting
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
	Archive        string  `json:"archive"`
	KeepWav        bool    `json:"keepWav"`
	Force          bool    `json:"force"`
	// Tags for this run; nil = the church's defaults for the folder, "" = no tag.
	Title  *string `json:"title"`
	Artist *string `json:"artist"`
	Album  *string `json:"album"`
}

func requestFrom(o normalizer.Options, archive string) runRequest {
	return runRequest{
		Start: o.Start, End: o.End,
		Flac: o.Outputs.Flac, Mp3: o.Outputs.Mp3, Opus: o.Outputs.Opus,
		Stereo: o.Stereo, FadeIn: o.FadeIn, FadeOut: o.FadeOut,
		NoiseReduction: o.NoiseReduction, TargetLUFS: o.TargetLUFS, TruePeak: o.TruePeak,
		Mp3Bitrate: o.Mp3Bitrate, OpusBitrate: o.OpusBitrate,
		Archive: archive, KeepWav: o.KeepWav, Force: o.Force,
	}
}

func (q runRequest) options() normalizer.Options {
	o := normalizer.Options{
		Start: q.Start, End: q.End,
		Outputs: normalizer.Outputs{Flac: q.Flac, Mp3: q.Mp3, Opus: q.Opus},
		Stereo:  q.Stereo, FadeIn: q.FadeIn, FadeOut: q.FadeOut,
		NoiseReduction: q.NoiseReduction, TargetLUFS: q.TargetLUFS, TruePeak: q.TruePeak,
		Mp3Bitrate: strings.TrimSpace(q.Mp3Bitrate), OpusBitrate: strings.TrimSpace(q.OpusBitrate),
		KeepWav: q.KeepWav, Force: q.Force,
	}
	if q.UseEdit {
		o.Start, o.End = 0, 0
	}
	return o
}

// plan is a checked run: the options, or why it cannot run, plus notices.
type plan struct {
	Error   string   `json:"error,omitempty"`
	Notices []string `json:"notices"`

	opt     normalizer.Options
	paths   normalizer.Paths
	archive string
}

// check validates a run for a folder the way the pipeline would see it,
// so the front end can show problems before anything starts.
func (s *Server) check(abs string, q runRequest) plan {
	pl := plan{Notices: []string{}, opt: q.options(), archive: strings.TrimSpace(q.Archive)}
	fail := func(err error) plan { pl.Error = err.Error(); return pl }
	if err := pl.opt.Validate(); err != nil {
		return fail(err)
	}
	paths, err := normalizer.NewPaths(abs, pl.archive)
	if err != nil {
		return fail(err)
	}
	pl.paths = paths
	var note string
	if pl.opt.Tags, note = s.runTags(abs, q); note != "" {
		pl.Notices = append(pl.Notices, note)
	}
	src := paths.Sources()
	switch {
	case q.UseEdit && src.Edit == "":
		return fail(fmt.Errorf("there is no %s_edit file", paths.Date))
	case !q.UseEdit && src.Raw == "":
		return fail(errors.New("there is no raw recording to cut"))
	case !q.UseEdit && src.EditInFolder:
		return fail(fmt.Errorf("%s is in the folder and would conflict with cutting: use it, or move it away", filepath.Base(src.Edit)))
	case !q.UseEdit && pl.opt.Start == 0 && pl.opt.End == 0:
		return fail(errors.New("select where the sermon starts and ends"))
	case src.Raw != "" && s.analyses.converting(src.Raw):
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
			pl.Notices = append(pl.Notices, "Already there, skipped unless Force: "+strings.Join(existing, ", "))
		}
		if !q.UseEdit && pl.archive != "" {
			if _, err := os.Stat(paths.EditArchive()); err == nil {
				pl.Notices = append(pl.Notices, "An earlier cut is in the archive; it is used instead of this selection unless Force.")
			}
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
