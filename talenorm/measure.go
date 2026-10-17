package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// Loudness er resultatet av en ebur128-måling.
type Loudness struct {
	LUFS     float64 // integrated loudness
	TruePeak float64 // dBTP
	LRA      float64 // loudness range, LU
}

// Sammendraget står på egne linjer; linjene for hver 100 ms starter med "[Parsed_..."
// og inneholder "I:" midt i linjen, så ^\s* skiller dem fra sammendraget.
var (
	reLUFS = regexp.MustCompile(`(?m)^\s*I:\s+(-?inf|-?[\d.]+) LUFS`)
	reLRA  = regexp.MustCompile(`(?m)^\s*LRA:\s+(-?inf|-?[\d.]+) LU`)
	rePeak = regexp.MustCompile(`(?m)^\s*Peak:\s+(-?inf|-?[\d.]+) dBFS`)
)

func lastFloat(re *regexp.Regexp, s, what string) (float64, error) {
	m := re.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("fant ikke %s i ffmpeg-utdata", what)
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

// Measure måler loudness med ebur128 (mye raskere enn loudnorm).
func (f FFmpeg) Measure(ctx context.Context, path string) (Loudness, error) {
	res, err := f.Run(ctx, "-i", path, "-af", "ebur128=peak=true:framelog=quiet", "-f", "null", "-")
	if err != nil {
		return Loudness{}, err
	}
	return parseEbur128(res.Stderr)
}
