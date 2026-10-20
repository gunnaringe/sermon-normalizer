package normalizer

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Probe is what a recording is: its format and the metadata in it, as read
// from the header ffmpeg prints.
type Probe struct {
	Container  string            `json:"container"`      // "flac", "wav", ...
	Codec      string            `json:"codec"`          // "flac", "pcm_s24le", ...
	SampleRate int               `json:"sampleRate"`     // Hz
	Bits       int               `json:"bits,omitempty"` // bit depth, when the format has one
	Channels   int               `json:"channels"`
	Bitrate    int               `json:"bitrate,omitempty"` // kb/s, the whole file's average
	Duration   float64           `json:"duration"`          // seconds
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ProbeFile reads the format and metadata of path, without decoding it.
func (f FFmpeg) ProbeFile(ctx context.Context, path string) (Probe, error) {
	res, err := f.Run(ctx, "-i", path, "-t", "0", "-f", "null", "-")
	if err != nil {
		return Probe{}, err
	}
	return parseProbe(res.Stderr)
}

var (
	reInput    = regexp.MustCompile(`(?m)^Input #0, ([^,]+), from`)
	reBitrate  = regexp.MustCompile(`Duration: .*bitrate: (\d+) kb/s`)
	reStream   = regexp.MustCompile(`(?m)Stream #0:\d+.*?: Audio: (.*)$`)
	reCodec    = regexp.MustCompile(`^([a-z0-9_]+)`)
	reRate     = regexp.MustCompile(`, (\d+) Hz,`)
	reBits     = regexp.MustCompile(`\((\d+) bit\)`)
	rePCMBits  = regexp.MustCompile(`^pcm_[su](\d+)`)
	reFmtBits  = regexp.MustCompile(`, [su](\d+)p?(?:,|$)`)
	reMetadata = regexp.MustCompile(`^\s+([^:]+?)\s*: (.*)$`)
)

// maxMetadata is how many metadata entries are kept: a recorder writes a few,
// a broken file could write thousands.
const maxMetadata = 30

func parseProbe(stderr string) (Probe, error) {
	var p Probe
	if m := reInput.FindStringSubmatch(stderr); m != nil {
		p.Container = strings.Split(m[1], ",")[0]
	}
	d, err := parseDuration(stderr)
	if err != nil {
		return p, err
	}
	p.Duration = d
	if m := reBitrate.FindStringSubmatch(stderr); m != nil {
		p.Bitrate, _ = strconv.Atoi(m[1])
	}
	m := reStream.FindStringSubmatch(stderr)
	if m == nil {
		return p, fmt.Errorf("no audio stream found in ffmpeg output")
	}
	audio := m[1]
	if c := reCodec.FindStringSubmatch(audio); c != nil {
		p.Codec = c[1]
	}
	if r := reRate.FindStringSubmatch(audio); r != nil {
		p.SampleRate, _ = strconv.Atoi(r[1])
	}
	switch {
	case reBits.MatchString(audio):
		p.Bits, _ = strconv.Atoi(reBits.FindStringSubmatch(audio)[1])
	case rePCMBits.MatchString(p.Codec):
		p.Bits, _ = strconv.Atoi(rePCMBits.FindStringSubmatch(p.Codec)[1])
	case p.Codec == "flac" && reFmtBits.MatchString(audio):
		p.Bits, _ = strconv.Atoi(reFmtBits.FindStringSubmatch(audio)[1])
	}
	if p.Channels, err = parseChannels(stderr); err != nil {
		return p, err
	}
	p.Metadata = parseMetadata(stderr)
	return p, nil
}

// parseMetadata reads the "Metadata:" blocks of the input and its first
// stream; the file's own entries come first and win.
func parseMetadata(stderr string) map[string]string {
	md := map[string]string{}
	in, indent := false, 0
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		depth := len(line) - len(trimmed)
		switch {
		case strings.HasPrefix(line, "Output #"):
			return nilIfEmpty(md)
		case trimmed == "Metadata:":
			in, indent = true, depth
			continue
		case in && depth > indent:
			if m := reMetadata.FindStringSubmatch(line); m != nil && len(md) < maxMetadata {
				k, v := strings.ToLower(strings.TrimSpace(m[1])), strings.TrimSpace(m[2])
				if _, seen := md[k]; !seen && v != "" {
					md[k] = v
				}
			}
			continue
		}
		in = false
	}
	return nilIfEmpty(md)
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
