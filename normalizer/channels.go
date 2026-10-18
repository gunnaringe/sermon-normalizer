package normalizer

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// NearMonoSide is how far below the sum (L+R) the difference (L-R) may be
// before two channels count as the same signal: at -30 dB the difference
// carries 0.1 % of the energy, which is noise, not stereo.
const NearMonoSide = -30.0

// OneSided is how much quieter one channel may be before the recording counts
// as being on one side only. Panning in a real stereo mix is a few dB.
const OneSided = 20.0

// ChannelCheck describes the channels of a source.
type ChannelCheck struct {
	Channels    int     // 1, 2, or more
	Side        float64 // two channels: level of L-R relative to L+R in dB; -Inf when identical
	Left, Right float64 // two channels: RMS level of each in dBFS; -Inf when silent
}

// Quieter returns which channel ("left"/"right") is at least OneSided dB
// quieter than the other, and by how much (+Inf when silent), or "".
func (c ChannelCheck) Quieter() (string, float64) {
	if c.Channels != 2 || math.IsInf(c.Left, -1) && math.IsInf(c.Right, -1) {
		return "", 0
	}
	switch d := c.Left - c.Right; {
	case d >= OneSided:
		return "right", d
	case -d >= OneSided:
		return "left", -d
	}
	return "", 0
}

// EffectivelyMono reports whether stereo output would add nothing.
func (c ChannelCheck) EffectivelyMono() bool {
	return c.Channels == 1 || (c.Channels == 2 && c.Side <= NearMonoSide)
}

var reAudioLayout = regexp.MustCompile(`Audio: .*?, \d+ Hz, ([^,]+),`)

// parseChannels reads the channel count of the first audio stream from
// ffmpeg's header ("mono", "stereo", "1 channels", "5.1(side)", ...).
func parseChannels(stderr string) (int, error) {
	m := reAudioLayout.FindStringSubmatch(stderr)
	if m == nil {
		return 0, fmt.Errorf("no audio stream found in ffmpeg output")
	}
	switch layout := strings.TrimSpace(m[1]); layout {
	case "mono":
		return 1, nil
	case "stereo":
		return 2, nil
	default:
		if n, ok := strings.CutSuffix(layout, " channels"); ok {
			return strconv.Atoi(n)
		}
		return 3, nil // 2.1, quad, 5.1, ...: more than two
	}
}

// parseLevels reads the RMS level astats prints for each channel (1-based).
func parseLevels(stderr string) (map[int]float64, error) {
	levels := map[int]float64{}
	ch := 0
	for _, line := range strings.Split(stderr, "\n") {
		if i := strings.Index(line, "] "); i >= 0 {
			line = line[i+2:]
		}
		switch {
		case strings.HasPrefix(line, "Channel: "):
			ch, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Channel: ")))
		case strings.HasPrefix(line, "Overall"):
			ch = 0
		case strings.HasPrefix(line, "RMS level dB: ") && ch > 0:
			v := strings.TrimSpace(strings.TrimPrefix(line, "RMS level dB: "))
			level, err := strconv.ParseFloat(v, 64) // "-inf" parses too
			if err != nil {
				return nil, fmt.Errorf("bad RMS level %q", v)
			}
			levels[ch] = level
		}
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("no channel levels found in ffmpeg output")
	}
	return levels, nil
}

// checkFromLevels turns the levels of the analysis channels (left, right,
// mid L+R, side L-R) into a ChannelCheck.
func checkFromLevels(levels map[int]float64) (ChannelCheck, error) {
	for ch := 1; ch <= 4; ch++ {
		if _, ok := levels[ch]; !ok {
			return ChannelCheck{}, fmt.Errorf("no level for analysis channel %d in ffmpeg output", ch)
		}
	}
	c := ChannelCheck{Channels: 2, Left: levels[1], Right: levels[2], Side: math.Inf(-1)}
	if mid := levels[3]; !math.IsInf(mid, -1) { // all silent: nothing to tell apart
		c.Side = levels[4] - mid
	}
	return c, nil
}

// CheckChannels looks at how different the channels of path are. pre are
// input options (e.g. -ss/-to) to look at only part of it.
func (f FFmpeg) CheckChannels(ctx context.Context, path string, pre ...string) (ChannelCheck, error) {
	res, err := f.Run(ctx, "-i", path, "-t", "0", "-f", "null", "-")
	if err != nil {
		return ChannelCheck{}, err
	}
	n, err := parseChannels(res.Stderr)
	if err != nil || n != 2 {
		return ChannelCheck{Channels: n}, err
	}
	// Four analysis channels: left, right, mid (L+R) and side (L-R).
	args := append(append([]string{}, pre...), "-i", path,
		"-af", "pan=quad|c0=c0|c1=c1|c2=0.5*c0+0.5*c1|c3=0.5*c0-0.5*c1,astats", "-f", "null", "-")
	if res, err = f.Run(ctx, args...); err != nil {
		return ChannelCheck{}, err
	}
	levels, err := parseLevels(res.Stderr)
	if err != nil {
		return ChannelCheck{}, err
	}
	return checkFromLevels(levels)
}
