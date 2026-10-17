package normalizer

import (
	"fmt"
	"strings"
)

// Outputs selects which files a run produces.
type Outputs struct {
	Flac bool // the master, mono 48 kHz / 24-bit
	Mp3  bool
	Opus bool
}

// AllOutputs is the default: master, MP3 and Opus.
var AllOutputs = Outputs{Flac: true, Mp3: true, Opus: true}

// ParseOutputs parses a comma-separated list like "flac,mp3,opus".
func ParseOutputs(s string) (Outputs, error) {
	var o Outputs
	for _, f := range strings.Split(s, ",") {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "flac":
			o.Flac = true
		case "mp3":
			o.Mp3 = true
		case "opus":
			o.Opus = true
		case "":
		default:
			return o, fmt.Errorf("unknown format %q (use flac, mp3, opus)", f)
		}
	}
	if o == (Outputs{}) {
		return o, fmt.Errorf("no formats in %q (use flac, mp3, opus)", s)
	}
	return o, nil
}

func (o Outputs) String() string {
	var f []string
	if o.Flac {
		f = append(f, "flac")
	}
	if o.Mp3 {
		f = append(f, "mp3")
	}
	if o.Opus {
		f = append(f, "opus")
	}
	return strings.Join(f, ",")
}
