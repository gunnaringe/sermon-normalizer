package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// folderState is <folder>/recording.json, the one file the studio keeps about
// a recording and the sermon made from it: what the recording is, the sermon's
// saved and adjusted settings, and how each output was made.
type folderState struct {
	// Recording is what the recording is; nil until it has been looked at, and
	// redone when the file changes (Stamp).
	Recording *recordingFacts `json:"recording"`
	// ConvertedFrom is the file (WAV, AIFF, video) the recording was made from, if it was.
	ConvertedFrom *sourceFile `json:"convertedFrom,omitempty"`
	// Imported is when the recording arrived (upload, inbox, share); the
	// folder list shows it among the new ones for a while (newFor).
	Imported *time.Time `json:"imported,omitempty"`
	// Labels are the user's own tags on the recording ("Easter"), searched
	// on the recordings page; not the title/artist/album written into files.
	Labels []string `json:"labels,omitempty"`
	sermonFile
	// Outputs is how each output (flac, mp3, opus) was made, by the format.
	Outputs map[string]outputMade `json:"outputs"`
}

// recStamp tells whether a file is still the one that was looked at.
type recStamp struct {
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func stampOf(path string) (recStamp, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return recStamp{}, false
	}
	return recStamp{Size: st.Size(), Modified: st.ModTime().UTC()}, true
}

type recordingFacts struct {
	Stamp    recStamp                   `json:"stamp"`
	Format   normalizer.Probe           `json:"format"`
	Channels *normalizer.ChannelVerdict `json:"channels"` // nil when the check failed
	Error    string                     `json:"error,omitempty"`
}

type sourceFile struct {
	Name   string           `json:"name"`
	Size   int64            `json:"size"`
	Format normalizer.Probe `json:"format"`
}

// outputMade is how an output was made. From is the fingerprint of everything
// that decides how it sounds and what it says (madeFrom): when the page would
// make it differently, it is out of date.
type outputMade struct {
	MadeAt  time.Time `json:"madeAt"`
	From    string    `json:"from"`
	Cut     string    `json:"cut,omitempty"` // cutOf: which cut it was made from
	Stereo  bool      `json:"stereo"`
	Bitrate string    `json:"bitrate,omitempty"`
}

const stateName = "recording.json"

func statePath(folder string) string { return filepath.Join(folder, stateName) }

// readState returns a folder's recording.json; an empty one if there is none
// or it cannot be read. Saved or adjusted settings that are not valid any more
// are dropped.
func readState(folder string) *folderState {
	st := &folderState{}
	if data, err := os.ReadFile(statePath(folder)); err == nil && json.Unmarshal(data, st) != nil {
		st = &folderState{}
	}
	for _, c := range []**sermonSettings{&st.Saved, &st.Adjusted} {
		if *c == nil {
			continue
		}
		(*c).normalize()
		if (*c).Validate() != nil {
			*c = nil
		}
	}
	if st.Saved == nil {
		st.SavedAt = nil
	}
	if st.Outputs == nil {
		st.Outputs = map[string]outputMade{}
	}
	return st
}

var errStateWrite = errors.New("could not save in the folder")

// updateState reads the folder's recording.json, applies fn and writes it
// (atomically), as one step: several things change it (the editor, the
// analysis, finished runs), and none may undo another's change.
func (s *Server) updateState(folder string, fn func(*folderState) error) (*folderState, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	st := readState(folder)
	if err := fn(st); err != nil {
		return st, err
	}
	data, _ := json.MarshalIndent(st, "", "  ")
	tmp := filepath.Join(folder, "."+stateName+".partial")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return st, errStateWrite
	}
	if err := os.Rename(tmp, statePath(folder)); err != nil {
		os.Remove(tmp)
		return st, errStateWrite
	}
	return st, nil
}

// markImported notes in the folder that its recording has just arrived.
func (s *Server) markImported(folder string) {
	now := time.Now().UTC()
	if _, err := s.updateState(folder, func(st *folderState) error { st.Imported = &now; return nil }); err != nil {
		s.cfg.Logger.Printf("%s: %v", s.relPath(folder), err)
	}
}

// folderOfRecording is the date folder of a recording: its folder, or the
// one above for a recording in original/.
func folderOfRecording(raw string) string {
	dir := filepath.Dir(raw)
	if filepath.Base(dir) == "original" {
		return filepath.Dir(dir)
	}
	return dir
}

// factsFresh reports whether recording.json describes raw as it is now.
func factsFresh(st *folderState, raw string) bool {
	stamp, ok := stampOf(raw)
	return ok && st.Recording != nil && st.Recording.Stamp.Equal(stamp)
}

func (a recStamp) Equal(b recStamp) bool { return a.Size == b.Size && a.Modified.Equal(b.Modified) }

// lookAt finds out what raw is (format, metadata, channels) and keeps it in
// recording.json. The channel check decodes the whole recording.
func (s *Server) lookAt(ctx context.Context, raw string) error {
	stamp, ok := stampOf(raw)
	if !ok {
		return errors.New("the recording is gone")
	}
	facts := &recordingFacts{Stamp: stamp}
	ff := s.cfg.FFmpeg
	p, err := ff.ProbeFile(ctx, raw)
	if err != nil {
		return err
	}
	facts.Format = p
	if c, err := ff.CheckChannels(ctx, raw); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		facts.Error = "could not check the channels: " + err.Error()
	} else {
		v := c.Verdict()
		facts.Channels = &v
	}
	_, err = s.updateState(folderOfRecording(raw), func(st *folderState) error {
		st.Recording = facts
		return nil
	})
	return err
}

// madeFrom is the fingerprint of an output made with o: the parts of the
// recording, the fades, the sound, the tags and, for MP3 and Opus, the bitrate.
// It does not depend on which other outputs a run makes.
func madeFrom(o normalizer.Options, format string) string {
	v := struct {
		Start, End, FadeIn, FadeOut float64
		Cuts                        []normalizer.Span
		Stereo                      bool
		NoiseReduction              int
		TargetLUFS, TruePeak        float64
		Tags                        normalizer.Tags
		Format, Bitrate             string
	}{o.Start, o.End, o.FadeIn, o.FadeOut, o.Cuts, o.Stereo, o.NoiseReduction, o.TargetLUFS, o.TruePeak, o.Tags, format, bitrateFor(o, format)}
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// cutOf is the fingerprint of the cut alone: the parts of the recording and
// the fades. An output made from another cut than the saved version is out of
// date, whatever the sound settings.
func cutOf(o normalizer.Options) string {
	v := struct {
		Start, End, FadeIn, FadeOut float64
		Cuts                        []normalizer.Span
	}{o.Start, o.End, o.FadeIn, o.FadeOut, o.Cuts}
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// savedCut is cutOf the saved version, or "" when it cannot be read.
func savedCut(saved sermonSettings) string {
	q := requestFrom(normalizer.DefaultOptions())
	q.cutting, q.FadeIn, q.FadeOut = saved.cutting, saved.FadeIn, saved.FadeOut
	o, err := q.options()
	if err != nil {
		return ""
	}
	return cutOf(o)
}

func bitrateFor(o normalizer.Options, format string) string {
	switch format {
	case "mp3":
		return o.Mp3Bitrate
	case "opus":
		return o.OpusBitrate
	}
	return ""
}
