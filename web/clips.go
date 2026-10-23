package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// A church's intro and outro clips (#63): audio made elsewhere, often with music, that goes before and after
// its sermons. Each is <church>/clips/<id>.flac, converted and checked when it arrives (in the background,
// like a recording); the list, the defaults and the pause at each join are "clips" in church.json.
// A sermon chooses its own in the editor (sermonSettings.Intro/Outro).
const (
	clipsDir         = "clips"
	maxClipUpload    = 90 << 20 // bytes; the site sits behind a proxy that takes 100 MB at most
	maxClipMinutes   = 5
	defaultClipPause = 0.5
	maxClipName      = 80
)

// The choices a sermon has for its intro or outro, besides a church clip's id; "" is none.
const (
	clipDefault = "default" // the church's default, whichever it is when the files are made
	clipOwn     = "own"     // the sermon's own clip, kept with it
)

var reClipID = regexp.MustCompile(`^[0-9a-f]{12}$`)

// validClipChoice reports whether c is a choice a sermon can have (not whether the clip is there).
func validClipChoice(c string) bool {
	return c == "" || c == clipDefault || c == clipOwn || reClipID.MatchString(c)
}

// clipLibrary is "clips" in church.json.
type clipLibrary struct {
	Clips []clipInfo `json:"clips,omitempty"`
	Intro string     `json:"intro,omitempty"` // the default intro's id; "" = none
	Outro string     `json:"outro,omitempty"`
	Pause *float64   `json:"pause,omitempty"` // seconds of silence at each join; nil = defaultClipPause
}

type clipInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Seconds  float64   `json:"seconds"`
	Version  string    `json:"version"` // changes when the audio is replaced
	Added    time.Time `json:"added"`
	Archived bool      `json:"archived,omitempty"` // not offered for new choices; kept for the sermons that use it
	// CopiedAs is, for a sermon's own clip, the church clip it was copied to (Add to the church's clips).
	CopiedAs string `json:"copiedAs,omitempty"`
}

func (l clipLibrary) pause() float64 {
	if l.Pause == nil {
		return defaultClipPause
	}
	return *l.Pause
}

func (l clipLibrary) find(id string) (int, bool) {
	i := slices.IndexFunc(l.Clips, func(c clipInfo) bool { return c.ID == id })
	return i, i >= 0
}

// clipStore holds a church's clips. dir "" = the church has no folder, so no clips.
type clipStore struct {
	mu      sync.Mutex
	dir     string
	lib     clipLibrary
	save    func(clipLibrary) error
	pending map[string]*pendingClip // uploads being prepared, or that failed, by clip id
}

// pendingClip is an upload on its way to becoming a clip (or replacing one's audio).
type pendingClip struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Replaces bool   `json:"replaces"`
	Error    string `json:"error,omitempty"`
	// A sermon's own clip: the folder (relative) and intro or outro; "" for a church clip.
	Folder string `json:"folder,omitempty"`
	Which  string `json:"which,omitempty"`
}

var (
	errNoClipFolder = errors.New("this church has no folder of its own, so it cannot have clips")
	errNoClip       = errors.New("no such clip")
)

// clipFile is where a church clip's audio is.
func (c *clipStore) clipFile(id string) string { return filepath.Join(c.dir, id+".flac") }

// change applies fn to a copy of the library and saves it; if fn or saving fails, nothing changes.
// c.mu must be held.
func (c *clipStore) change(fn func(*clipLibrary) error) error {
	next := c.lib
	next.Clips = slices.Clone(c.lib.Clips)
	if err := fn(&next); err != nil {
		return err
	}
	if c.save != nil {
		if err := c.save(next); err != nil {
			return err
		}
	}
	c.lib = next
	return nil
}

// sweepClips removes what an upload left behind when the studio stopped while preparing it.
func (c *clipStore) sweep() {
	des, _ := os.ReadDir(c.dir)
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") && !de.IsDir() {
			normalizer.Remove(filepath.Join(c.dir, de.Name()))
		}
	}
}

// clipUse counts the sermons that chose each church clip, saved or being edited (the trash is not looked in).
func (s *Server) clipUse() map[string]int {
	use := map[string]int{}
	for _, e := range s.allRecordings() {
		abs, err := s.resolve(e.Path)
		if err != nil {
			continue
		}
		chosen := map[string]bool{}
		for _, c := range []*sermonSettings{readState(abs).Saved, readDraft(abs)} {
			if c != nil {
				chosen[c.Intro], chosen[c.Outro] = true, true
			}
		}
		for id := range chosen {
			if reClipID.MatchString(id) {
				use[id]++
			}
		}
	}
	return use
}

// clipView is a clip as the settings list it.
type clipView struct {
	clipInfo
	Used int `json:"used"` // sermons that chose it
}

// GET api/clips: the church's clips, the defaults, and the uploads being prepared.
func (s *Server) handleClips(w http.ResponseWriter, r *http.Request) {
	use := s.clipUse()
	writeJSON(w, http.StatusOK, s.clipList(use))
}

func (s *Server) clipList(use map[string]int) map[string]any {
	s.clips.mu.Lock()
	defer s.clips.mu.Unlock()
	views := []clipView{}
	for _, c := range s.clips.lib.Clips {
		views = append(views, clipView{c, use[c.ID]})
	}
	pending := []pendingClip{}
	for _, p := range s.clips.pending {
		if p.Folder == "" {
			pending = append(pending, *p)
		}
	}
	slices.SortFunc(pending, func(a, b pendingClip) int { return strings.Compare(a.Name, b.Name) })
	return map[string]any{
		"available": s.clips.dir != "", "clips": views, "pending": pending,
		"intro": s.clips.lib.Intro, "outro": s.clips.lib.Outro, "pause": s.clips.lib.pause(),
		"maxPause": normalizer.MaxClipPause, "maxMinutes": maxClipMinutes, "maxMB": maxClipUpload >> 20,
	}
}

// writeClips answers a change with the list, as GET api/clips does.
func (s *Server) writeClips(w http.ResponseWriter, status int) {
	writeJSON(w, status, s.clipList(s.clipUse()))
}

// cleanClipName checks a clip's name.
func cleanClipName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len([]rune(name)) > maxClipName || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }) {
		return "", fmt.Errorf("give the clip a name (up to %d characters)", maxClipName)
	}
	return name, nil
}

func newClipID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// clipUpload is where an upload goes and what becomes of it once prepared.
type clipUpload struct {
	key  string               // in clipStore.pending: a church clip's id, or ownKey for a sermon's own
	dst  string               // the clip's file; the upload waits next to it, hidden
	done func(clipInfo) error // records the clip, which is in place at dst (with Seconds and Version)
}

// POST api/clips?name=<file name>, the file as the body: a new clip, named after the file. It is
// converted and checked in the background; until then it is among the pending ones.
func (s *Server) handleClipAdd(w http.ResponseWriter, r *http.Request) {
	if s.clips.dir == "" {
		writeError(w, http.StatusBadRequest, errNoClipFolder)
		return
	}
	id := newClipID()
	name := clipNameOf(r)
	up := clipUpload{key: id, dst: s.clips.clipFile(id), done: func(info clipInfo) error {
		s.clips.mu.Lock()
		defer s.clips.mu.Unlock()
		return s.clips.change(func(l *clipLibrary) error {
			info.ID, info.Name, info.Added = id, name, time.Now().UTC().Truncate(time.Second)
			l.Clips = append(l.Clips, info)
			return nil
		})
	}}
	if s.receiveClip(w, r, up, pendingClip{ID: id, Name: name}) {
		s.writeClips(w, http.StatusAccepted)
	}
}

// POST api/clips/{id}/replace?name=<file name>: new audio for a clip. The old stays in use until the new
// is ready.
func (s *Server) handleClipReplace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.clips.mu.Lock()
	i, ok := s.clips.lib.find(id)
	var name string
	if ok {
		name = s.clips.lib.Clips[i].Name
	}
	s.clips.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errNoClip)
		return
	}
	up := clipUpload{key: id, dst: s.clips.clipFile(id), done: func(info clipInfo) error {
		s.clips.mu.Lock()
		defer s.clips.mu.Unlock()
		return s.clips.change(func(l *clipLibrary) error {
			if i, ok := l.find(id); ok {
				l.Clips[i].Seconds, l.Clips[i].Version = info.Seconds, info.Version
			}
			return nil
		})
	}}
	if s.receiveClip(w, r, up, pendingClip{ID: id, Name: name, Replaces: true}) {
		s.writeClips(w, http.StatusAccepted)
	}
}

// clipNameOf is the name a clip gets from the file it was uploaded as (?name=).
func clipNameOf(r *http.Request) string {
	file := filepath.Base(r.URL.Query().Get("name"))
	name, err := cleanClipName(strings.TrimSuffix(file, filepath.Ext(file)))
	if err != nil {
		return "Clip"
	}
	return name
}

// receiveClip takes the uploaded file (the body, its name in ?name=) next to up.dst and prepares it in the
// background, as p among the pending ones. It reports whether it did; if not, it has answered.
func (s *Server) receiveClip(w http.ResponseWriter, r *http.Request, up clipUpload, p pendingClip) bool {
	file := filepath.Base(r.URL.Query().Get("name"))
	if _, ok := normalizer.KindOf(file); !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("a clip must be one of these types: %s", strings.Join(normalizer.InputTypes(), " ")))
		return false
	}
	s.clips.mu.Lock()
	busy := s.clips.pending[up.key] != nil && s.clips.pending[up.key].Error == ""
	if !busy {
		s.clips.pending[up.key] = &p
	}
	s.clips.mu.Unlock()
	if busy {
		writeError(w, http.StatusConflict, errors.New("this clip is being prepared; wait until that is done"))
		return false
	}
	fail := func(status int, err error) bool {
		s.clips.mu.Lock()
		delete(s.clips.pending, up.key)
		s.clips.mu.Unlock()
		writeError(w, status, err)
		return false
	}
	dir := filepath.Dir(up.dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(http.StatusInternalServerError, err)
	}
	tmp := filepath.Join(dir, "."+strings.TrimSuffix(filepath.Base(up.dst), ".flac")+".upload"+strings.ToLower(filepath.Ext(file)))
	f, err := os.Create(tmp)
	if err != nil {
		return fail(http.StatusInternalServerError, err)
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxClipUpload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		normalizer.Remove(tmp)
		if tooBig := (*http.MaxBytesError)(nil); errors.As(err, &tooBig) {
			return fail(http.StatusRequestEntityTooLarge, fmt.Errorf("a clip can be at most %d MB", maxClipUpload>>20))
		}
		return fail(http.StatusBadRequest, err)
	}
	s.background(func() { s.prepareClip(up, tmp, p.Name) })
	return true
}

// prepareClip makes the uploaded file at tmp into the clip at up.dst: FLAC, the first audio stream only and
// no tags, at most maxClipMinutes long and not silent. One conversion at a time, shared with the recordings'.
func (s *Server) prepareClip(up clipUpload, tmp, name string) {
	defer normalizer.Remove(tmp)
	select {
	case s.limits.decode <- struct{}{}:
		defer func() { <-s.limits.decode }()
	case <-s.ctx.Done():
		return
	}
	out := filepath.Join(filepath.Dir(up.dst), "."+strings.TrimSuffix(filepath.Base(up.dst), ".flac")+".new.flac")
	defer normalizer.Remove(out)
	info, err := s.convertClip(tmp, out)
	if err == nil {
		err = normalizer.Rename(out, up.dst)
	}
	if err == nil {
		err = up.done(info)
	}
	s.clips.mu.Lock()
	defer s.clips.mu.Unlock()
	if err == nil {
		delete(s.clips.pending, up.key)
		return
	}
	s.cfg.Logger.Printf("clip %s (%s): %v", up.key, name, err)
	if p := s.clips.pending[up.key]; p != nil {
		p.Error = err.Error()
	}
}

// convertClip converts src into out and checks it; the result has Seconds and Version.
func (s *Server) convertClip(src, out string) (clipInfo, error) {
	ff := s.cfg.FFmpeg
	if err := ff.RunTo(s.ctx, out, "-i", src, "-map", "0:a:0", "-map_metadata", "-1", "-c:a", "flac"); err != nil {
		return clipInfo{}, errors.New("could not read the clip as audio")
	}
	seconds, err := ff.Duration(s.ctx, out)
	if err != nil {
		return clipInfo{}, errors.New("could not read the clip as audio")
	}
	if seconds > maxClipMinutes*60 {
		return clipInfo{}, fmt.Errorf("a clip can be at most %d minutes long", maxClipMinutes)
	}
	m, err := ff.Measure(s.ctx, out)
	if err != nil {
		return clipInfo{}, errors.New("could not read the clip as audio")
	}
	if m.LUFS <= -70 {
		return clipInfo{}, errors.New("the clip is silent")
	}
	f, err := os.Open(out)
	if err != nil {
		return clipInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return clipInfo{}, err
	}
	return clipInfo{Seconds: seconds, Version: hex.EncodeToString(h.Sum(nil))[:12]}, nil
}

// PUT api/clips/{id} {"name", "archived"}: renames, archives or restores a clip. The church's default
// intro or outro cannot be archived: choose another default first.
func (s *Server) handleClipPut(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Name     *string `json:"name"`
		Archived *bool   `json:"archived"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	s.clips.mu.Lock()
	err := s.clips.change(func(l *clipLibrary) error {
		i, ok := l.find(id)
		if !ok {
			return errNoClip
		}
		if q.Name != nil {
			name, err := cleanClipName(*q.Name)
			if err != nil {
				return err
			}
			l.Clips[i].Name = name
		}
		if q.Archived != nil {
			if *q.Archived {
				if err := l.notDefault(id); err != nil {
					return err
				}
			}
			l.Clips[i].Archived = *q.Archived
		}
		return nil
	})
	s.clips.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeClips(w, http.StatusOK)
}

// notDefault refuses to take away the church's default intro or outro, which every sermon on
// "Church default" would then silently be without.
func (l clipLibrary) notDefault(id string) error {
	switch id {
	case l.Intro:
		return errors.New("this clip is the default intro: choose another default intro (or none) first")
	case l.Outro:
		return errors.New("this clip is the default outro: choose another default outro (or none) first")
	}
	return nil
}

// POST api/clips/{id}/delete: deletes a clip for good (not the default intro or outro), or drops an upload
// that failed. Sermons that chose it keep their files, but cannot be made again with it: the page warns.
func (s *Server) handleClipDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.clips.mu.Lock()
	var err error
	switch p := s.clips.pending[id]; {
	case p != nil && p.Error != "":
		delete(s.clips.pending, id) // an upload that failed; a clip it was to replace stays as it was
	case p != nil:
		err = errors.New("this clip is being prepared; wait until that is done")
	default:
		err = s.clips.change(func(l *clipLibrary) error {
			i, ok := l.find(id)
			if !ok {
				return errNoClip
			}
			if err := l.notDefault(id); err != nil {
				return err
			}
			if err := normalizer.Remove(s.clips.clipFile(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			l.Clips = slices.Delete(l.Clips, i, i+1)
			return nil
		})
	}
	s.clips.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeClips(w, http.StatusOK)
}

// PUT api/clips/defaults {"intro", "outro", "pause"}: the church's default intro and outro (ids of clips
// that are not archived; "" = none) and the pause at each join.
func (s *Server) handleClipDefaults(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Intro string   `json:"intro"`
		Outro string   `json:"outro"`
		Pause *float64 `json:"pause"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if q.Pause != nil && (*q.Pause < 0 || *q.Pause > normalizer.MaxClipPause) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("the pause at the intro and outro can be from 0 to %g seconds", normalizer.MaxClipPause))
		return
	}
	s.clips.mu.Lock()
	err := s.clips.change(func(l *clipLibrary) error {
		for _, id := range []string{q.Intro, q.Outro} {
			if id == "" {
				continue
			}
			if i, ok := l.find(id); !ok || l.Clips[i].Archived {
				return errNoClip
			}
		}
		l.Intro, l.Outro = q.Intro, q.Outro
		if q.Pause != nil {
			l.Pause = q.Pause
		}
		return nil
	})
	s.clips.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeClips(w, http.StatusOK)
}

// GET api/clips/{id}/audio: a clip's audio, to listen to.
func (s *Server) handleClipAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.clips.mu.Lock()
	_, ok := s.clips.lib.find(id)
	s.clips.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errNoClip)
		return
	}
	f, err := os.Open(s.clips.clipFile(id))
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
