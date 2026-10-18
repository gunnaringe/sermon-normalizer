package normalizer

import "strings"

// Outputs selects which files a run produces.
type Outputs struct {
	Flac bool // the master, 48 kHz / 24-bit
	Mp3  bool
	Opus bool
}

// AllOutputs is the default: master, MP3 and Opus.
var AllOutputs = Outputs{Flac: true, Mp3: true, Opus: true}

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
