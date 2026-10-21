package web

import (
	"errors"
	"net/http"
	"sync"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// maxDefaultFade is the longest fade the defaults can have, as in the editor.
const maxDefaultFade = 30

// processing is a church's default processing ("processing" in church.json):
// what a recording starts with until it has its own, kept in its
// recording.json. Loudness and bitrates have a value for mono and one for
// stereo: switching the channels in the editor moves between them.
type processing struct {
	Stereo         bool          `json:"stereo"` // the channels a recording starts with
	NoiseReduction int           `json:"noiseReduction"`
	RoomSound      int           `json:"roomSound"`
	Leveling       string        `json:"leveling"`
	Compression    string        `json:"compression"`
	TruePeak       float64       `json:"truePeak"`
	FadeIn         float64       `json:"fadeIn"`
	FadeOut        float64       `json:"fadeOut"`
	Mono           channelLevels `json:"mono"`
	StereoLevels   channelLevels `json:"stereoLevels"`
}

// channelLevels are the defaults that depend on the channels.
type channelLevels struct {
	TargetLUFS  float64 `json:"targetLufs"`
	Mp3Bitrate  string  `json:"mp3Bitrate"`
	OpusBitrate string  `json:"opusBitrate"`
}

func levelsOf(q runRequest) channelLevels {
	return channelLevels{q.TargetLUFS, q.Mp3Bitrate, q.OpusBitrate}
}

// builtinProcessing is the processing of a church that has chosen none.
func builtinProcessing() processing {
	m := requestFrom(normalizer.DefaultOptions())
	return processing{
		Stereo: m.Stereo, NoiseReduction: m.NoiseReduction, RoomSound: m.RoomSound,
		Leveling: m.Leveling, Compression: m.Compression, TruePeak: m.TruePeak,
		FadeIn: m.FadeIn, FadeOut: m.FadeOut,
		Mono: levelsOf(m), StereoLevels: levelsOf(stereoDefaults()),
	}
}

// request is a run with the processing, in mono or stereo.
func (p processing) request(stereo bool) runRequest {
	q := requestFrom(normalizer.DefaultOptions())
	q.Stereo, q.NoiseReduction, q.RoomSound, q.Leveling, q.Compression = stereo, p.NoiseReduction, p.RoomSound, p.Leveling, p.Compression
	q.TruePeak, q.FadeIn, q.FadeOut = p.TruePeak, p.FadeIn, p.FadeOut
	l := p.Mono
	if stereo {
		l = p.StereoLevels
	}
	q.TargetLUFS, q.Mp3Bitrate, q.OpusBitrate = l.TargetLUFS, l.Mp3Bitrate, l.OpusBitrate
	return q
}

// start is what a recording starts with.
func (p processing) start() runRequest { return p.request(p.Stereo) }

func (p processing) validate() error {
	if p.FadeIn > maxDefaultFade || p.FadeOut > maxDefaultFade {
		return errors.New("a fade can be at most 30 seconds")
	}
	for _, stereo := range []bool{false, true} {
		o, err := p.request(stereo).options()
		if err == nil {
			err = o.Validate()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// procStore holds a church's processing; save persists it (nil = built-in;
// a nil save func = memory only).
type procStore struct {
	mu   sync.Mutex
	proc *processing
	save func(*processing) error
}

// processing is the church's default processing.
func (s *Server) processing() processing {
	s.proc.mu.Lock()
	defer s.proc.mu.Unlock()
	if s.proc.proc != nil {
		return *s.proc.proc
	}
	return builtinProcessing()
}

// handleProcessingPut changes the church's default processing:
// PUT api/processing {"processing": {...}}; null goes back to the built-in.
func (s *Server) handleProcessingPut(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Processing *processing `json:"processing"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if q.Processing != nil {
		if err := q.Processing.validate(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	s.proc.mu.Lock()
	defer s.proc.mu.Unlock()
	if s.proc.save != nil {
		if err := s.proc.save(q.Processing); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.proc.proc = q.Processing
	p := builtinProcessing()
	if q.Processing != nil {
		p = *q.Processing
	}
	writeJSON(w, http.StatusOK, map[string]any{"processing": p})
}
