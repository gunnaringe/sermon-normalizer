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

// ChannelCheck describes the channels of a source.
type ChannelCheck struct {
	Channels int     // 1, 2, or more
	Side     float64 // two channels: level of L-R relative to L+R in dB; -Inf when identical
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

// parseSide reads the RMS levels astats prints per channel after the
// mid/side pan, and returns side minus mid in dB.
func parseSide(stderr string) (float64, error) {
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
				return 0, fmt.Errorf("bad RMS level %q", v)
			}
			levels[ch] = level
		}
	}
	mid, okMid := levels[1]
	side, okSide := levels[2]
	if !okMid || !okSide {
		return 0, fmt.Errorf("no channel levels found in ffmpeg output")
	}
	if math.IsInf(mid, -1) {
		return math.Inf(-1), nil // silence: nothing to tell apart
	}
	return side - mid, nil
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
	args := append(append([]string{}, pre...), "-i", path,
		"-af", "pan=stereo|c0=0.5*c0+0.5*c1|c1=0.5*c0-0.5*c1,astats", "-f", "null", "-")
	if res, err = f.Run(ctx, args...); err != nil {
		return ChannelCheck{}, err
	}
	side, err := parseSide(res.Stderr)
	return ChannelCheck{Channels: 2, Side: side}, err
}
