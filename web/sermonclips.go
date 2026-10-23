package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// The clips of one sermon (#63): what it chose for its intro and outro (sermonSettings.Intro/Outro), resolved
// for a run, and its own clips, kept with it in <folder>/clips/intro.flac and outro.flac (folderState.OwnClips).

var clipWhich = []string{"intro", "outro"}

func ownClipFile(folder, which string) string { return filepath.Join(folder, clipsDir, which+".flac") }

func ownKey(folder, which string) string { return "own:" + which + ":" + folder }

// clipRef is a clip as a run used it: a church clip's id or clipOwn, its version, and its name and length
// at the time.
type clipRef struct {
	ID      string  `json:"id"`
	Version string  `json:"version"`
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

// runClips are the clips a run puts around the sermon.
type runClips struct {
	Intro *clipRef `json:"intro"`
	Outro *clipRef `json:"outro"`
	Pause float64  `json:"pause"`
}

// key is the fingerprint of the clips for madeFrom: "" when there are none, so the outputs of a sermon
// without clips keep the fingerprint they had before there were clips.
func (c runClips) key() string {
	if c.Intro == nil && c.Outro == nil {
		return ""
	}
	part := func(r *clipRef) string {
		if r == nil {
			return "-"
		}
		return r.ID + "@" + r.Version
	}
	return fmt.Sprintf("%s|%s|%g", part(c.Intro), part(c.Outro), c.Pause)
}

// resolveClip is the clip a sermon's choice for which (intro or outro) means now: its file and what it is,
// or nil for none. A clip it chose that is gone is an error, which a run reports.
func (s *Server) resolveClip(folder, which, choice string) (*clipRef, string, error) {
	switch {
	case choice == "":
		return nil, "", nil
	case choice == clipOwn:
		own, ok := readState(folder).OwnClips[which]
		path := ownClipFile(folder, which)
		if !ok || !fileExists(path) {
			return nil, "", fmt.Errorf("this sermon's own %s is missing: upload it again, or choose another", which)
		}
		return &clipRef{ID: clipOwn, Version: own.Version, Name: own.Name, Seconds: own.Seconds}, path, nil
	}
	s.clips.mu.Lock()
	defer s.clips.mu.Unlock()
	id := choice
	if choice == clipDefault {
		id = map[string]string{"intro": s.clips.lib.Intro, "outro": s.clips.lib.Outro}[which]
		if id == "" {
			return nil, "", nil
		}
	}
	i, ok := s.clips.lib.find(id)
	if !ok || !fileExists(s.clips.clipFile(id)) {
		if choice == clipDefault {
			return nil, "", nil // cannot be: the default cannot be deleted
		}
		return nil, "", fmt.Errorf("the %s this sermon used has been deleted: choose another %s, or none", which, which)
	}
	c := s.clips.lib.Clips[i]
	return &clipRef{ID: c.ID, Version: c.Version, Name: c.Name, Seconds: c.Seconds}, s.clips.clipFile(id), nil
}

// applyClips resolves the intro and outro of a run into its options.
func (s *Server) applyClips(folder string, intro, outro string, o *normalizer.Options) (runClips, error) {
	var rc runClips
	var err error
	if rc.Intro, o.Intro, err = s.resolveClip(folder, "intro", intro); err != nil {
		return rc, err
	}
	if rc.Outro, o.Outro, err = s.resolveClip(folder, "outro", outro); err != nil {
		return rc, err
	}
	if rc.Intro != nil || rc.Outro != nil {
		s.clips.mu.Lock()
		rc.Pause = s.clips.lib.pause()
		s.clips.mu.Unlock()
		o.ClipPause = rc.Pause
	}
	return rc, nil
}

// sermonClipsView is what the editor needs for the intro and outro: the church's clips and defaults, the
// sermon's own clips, and its uploads being prepared.
func (s *Server) sermonClipsView(abs string) map[string]any {
	v := s.clipList(map[string]int{})
	own := map[string]any{}
	for which, c := range readState(abs).OwnClips {
		if fileExists(ownClipFile(abs, which)) {
			own[which] = c
		}
	}
	pending := map[string]pendingClip{}
	s.clips.mu.Lock()
	for _, which := range clipWhich {
		if p := s.clips.pending[ownKey(abs, which)]; p != nil {
			pending[which] = *p
		}
	}
	s.clips.mu.Unlock()
	v["own"], v["ownPending"] = own, pending
	return v
}

// GET api/sermon/clips?path=: the clips the editor can choose from (sermonClipsView).
func (s *Server) handleSermonClips(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.sermonClipsView(abs))
}

// whichParam is ?which=, intro or outro.
func whichParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	which := r.URL.Query().Get("which")
	if which != "intro" && which != "outro" {
		writeError(w, http.StatusBadRequest, errors.New("which must be intro or outro"))
		return "", false
	}
	return which, true
}

// sermonFolder is the ?path= of a recording folder, with ?which=.
func (s *Server) sermonFolder(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return "", "", false
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("there is no such folder"))
		return "", "", false
	}
	which, ok := whichParam(w, r)
	return abs, which, ok
}

// POST api/sermon/clip?path=&which=&name=<file name>, the file as the body: the sermon's own intro or outro,
// kept with it. It is prepared like a church clip; the sermon's choice is the editor's to make.
func (s *Server) handleOwnClip(w http.ResponseWriter, r *http.Request) {
	abs, which, ok := s.sermonFolder(w, r)
	if !ok {
		return
	}
	name := clipNameOf(r)
	up := clipUpload{key: ownKey(abs, which), dst: ownClipFile(abs, which), done: func(info clipInfo) error {
		_, err := s.updateState(abs, func(st *folderState) error {
			if st.OwnClips == nil {
				st.OwnClips = map[string]clipInfo{}
			}
			info.ID, info.Name, info.Added = clipOwn, name, time.Now().UTC().Truncate(time.Second)
			st.OwnClips[which] = info
			return nil
		})
		return err
	}}
	if s.receiveClip(w, r, up, pendingClip{ID: up.key, Name: name, Folder: s.relPath(abs), Which: which}) {
		writeJSON(w, http.StatusAccepted, s.sermonClipsView(abs))
	}
}

// POST api/sermon/clip/dismiss?path=&which=: forgets an upload of the sermon's own clip that failed.
func (s *Server) handleOwnClipDismiss(w http.ResponseWriter, r *http.Request) {
	abs, which, ok := s.sermonFolder(w, r)
	if !ok {
		return
	}
	s.clips.mu.Lock()
	if p := s.clips.pending[ownKey(abs, which)]; p != nil && p.Error != "" {
		delete(s.clips.pending, ownKey(abs, which))
	}
	s.clips.mu.Unlock()
	writeJSON(w, http.StatusOK, s.sermonClipsView(abs))
}

// GET api/sermon/clip/audio?path=&which=: the sermon's own clip, to listen to.
func (s *Server) handleOwnClipAudio(w http.ResponseWriter, r *http.Request) {
	abs, which, ok := s.sermonFolder(w, r)
	if !ok {
		return
	}
	f, err := os.Open(ownClipFile(abs, which))
	if err != nil {
		writeError(w, http.StatusNotFound, errNoClip)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "audio/flac")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// POST api/sermon/clip/add?path=&which=: copies the sermon's own clip to the church's clips, under its name.
// The sermon keeps its own; the answer is sermonClipsView, with the new clip's id as "added".
func (s *Server) handleOwnClipAdd(w http.ResponseWriter, r *http.Request) {
	abs, which, ok := s.sermonFolder(w, r)
	if !ok {
		return
	}
	if s.clips.dir == "" {
		writeError(w, http.StatusBadRequest, errNoClipFolder)
		return
	}
	own, ok := readState(abs).OwnClips[which]
	src := ownClipFile(abs, which)
	if !ok || !fileExists(src) {
		writeError(w, http.StatusNotFound, errNoClip)
		return
	}
	id := newClipID()
	if err := copyFile(src, s.clips.clipFile(id)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.clips.mu.Lock()
	err := s.clips.change(func(l *clipLibrary) error {
		c := own
		c.ID, c.Added, c.Archived, c.CopiedAs = id, time.Now().UTC().Truncate(time.Second), false, ""
		l.Clips = append(l.Clips, c)
		return nil
	})
	s.clips.mu.Unlock()
	if err == nil {
		_, err = s.updateState(abs, func(st *folderState) error {
			if c, ok := st.OwnClips[which]; ok {
				c.CopiedAs = id
				st.OwnClips[which] = c
			}
			return nil
		})
	}
	if err != nil {
		normalizer.Remove(s.clips.clipFile(id))
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	v := s.sermonClipsView(abs)
	v["added"] = id
	writeJSON(w, http.StatusOK, v)
}

// copyFile copies src to dst through a hidden file next to it, renamed when whole.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".partial")
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = normalizer.Rename(tmp, dst)
	}
	if err != nil {
		normalizer.Remove(tmp)
	}
	return err
}

// sermonOnlyOutputs are the outputs of the sermon without the clips (<date>_sermon.*), made by a run with an
// intro or outro; their records in recording.json are under <format>_sermon.
func sermonOnlyOutputs(p normalizer.Paths) []output {
	return []output{{"flac" + normalizer.SermonOnly, p.SermonMaster}, {"mp3" + normalizer.SermonOnly, p.SermonMp3}, {"opus" + normalizer.SermonOnly, p.SermonOpus}}
}

// baseFormat is the format of an output's record key: flac for flac_sermon.
func baseFormat(key string) string { return strings.TrimSuffix(key, normalizer.SermonOnly) }

// dropSermonOnly removes the sermon-only files (and their records) of the formats a run without clips made:
// they belong to an earlier run with clips, and would be offered as if they were up to date.
func (s *Server) dropSermonOnly(pl plan) {
	on := map[string]bool{"flac": pl.opt.Outputs.Flac, "mp3": pl.opt.Outputs.Mp3, "opus": pl.opt.Outputs.Opus}
	var gone []string
	for _, x := range sermonOnlyOutputs(pl.paths) {
		if on[baseFormat(x.format)] && fileExists(x.path) {
			if err := normalizer.Remove(x.path); err != nil {
				s.cfg.Logger.Printf("%s: %v", s.display(x.path), err)
				continue
			}
		}
		if on[baseFormat(x.format)] {
			gone = append(gone, x.format)
		}
	}
	s.updateState(pl.paths.Folder, func(st *folderState) error {
		for _, f := range gone {
			delete(st.Outputs, f)
		}
		return nil
	})
}

// madeWith is the intro and outro the main files were made with.
type madeWith struct {
	Intro *clipRef `json:"intro"`
	Outro *clipRef `json:"outro"`
	// Changed: the saved sermon would get another intro or outro now (another clip, a new version of it, or
	// one that has been deleted).
	Changed bool `json:"changed"`
}

// madeWithOf is what the main files were made with; nil when there are none, or neither they nor the saved
// sermon have clips.
func (s *Server) madeWithOf(paths normalizer.Paths, st *folderState) *madeWith {
	var m *madeWith
	for _, x := range outputsOf(paths) {
		if rec, ok := st.Outputs[x.format]; ok && fileExists(x.path) {
			m = &madeWith{Intro: rec.Intro, Outro: rec.Outro}
			break
		}
	}
	if m == nil || st.Saved == nil {
		return nil
	}
	same := func(a, b *clipRef) bool {
		return (a == nil) == (b == nil) && (a == nil || a.ID == b.ID && a.Version == b.Version)
	}
	for i, choice := range []string{st.Saved.Intro, st.Saved.Outro} {
		now, _, err := s.resolveClip(paths.Folder, clipWhich[i], choice)
		m.Changed = m.Changed || err != nil || !same(now, []*clipRef{m.Intro, m.Outro}[i])
	}
	if m.Intro == nil && m.Outro == nil && !m.Changed {
		return nil
	}
	return m
}
