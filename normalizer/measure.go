package normalizer

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// Loudness is the result of an ebur128 measurement.
type Loudness struct {
	LUFS     float64 // integrated loudness
	TruePeak float64 // dBTP
	LRA      float64 // loudness range, LU
}

// The summary is on lines of its own; the lines for every 100 ms start with "[Parsed_..."
// and contain "I:" mid-line, so ^\s* tells them apart from the summary.
var (
	reLUFS = regexp.MustCompile(`(?m)^\s*I:\s+(-?inf|-?[\d.]+) LUFS`)
	reLRA  = regexp.MustCompile(`(?m)^\s*LRA:\s+(-?inf|-?[\d.]+) LU`)
	rePeak = regexp.MustCompile(`(?m)^\s*Peak:\s+(-?inf|-?[\d.]+) dBFS`)
)

func lastFloat(re *regexp.Regexp, s, what string) (float64, error) {
	m := re.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("no %s found in ffmpeg output", what)
	}
	return strconv.ParseFloat(m[len(m)-1][1], 64)
}

func parseEbur128(stderr string) (Loudness, error) {
	var l Loudness
	var err error
	if l.LUFS, err = lastFloat(reLUFS, stderr, "integrated loudness"); err != nil {
		return l, err
	}
	if l.TruePeak, err = lastFloat(rePeak, stderr, "true peak"); err != nil {
		return l, err
	}
	if l.LRA, err = lastFloat(reLRA, stderr, "LRA"); err != nil {
		return l, err
	}
	return l, nil
}

// Measure measures loudness with ebur128 (much faster than loudnorm).
func (f FFmpeg) Measure(ctx context.Context, path string) (Loudness, error) {
	res, err := f.Run(ctx, "-i", path, "-af", "ebur128=peak=true:framelog=quiet", "-f", "null", "-")
	if err != nil {
		return Loudness{}, err
	}
	return parseEbur128(res.Stderr)
}
