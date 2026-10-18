package normalizer

import (
	"errors"
	"strings"
)

// Validate checks the options on their own, before any file is looked at.
// The messages use the names of the web form.
func (o Options) Validate() error {
	switch {
	case o.Outputs == (Outputs{}):
		return errors.New("choose at least one output")
	case o.Start < 0 || o.End < 0:
		return errors.New("start and end cannot be negative")
	case o.End > 0 && o.End <= o.Start:
		return errors.New("the end must be after the start")
	case o.FadeIn < 0 || o.FadeOut < 0:
		return errors.New("fades cannot be negative")
	case o.NoiseReduction < 0 || o.NoiseReduction > 97:
		return errors.New("noise reduction must be from 0 to 97 dB")
	case o.TargetLUFS < -70 || o.TargetLUFS > -5:
		return errors.New("target loudness must be from -70 to -5 LUFS")
	case o.TruePeak < -20 || o.TruePeak > 0:
		return errors.New("max true peak must be from -20 to 0 dBTP")
	case strings.TrimSpace(o.Mp3Bitrate) == "" || strings.TrimSpace(o.OpusBitrate) == "":
		return errors.New("bitrates cannot be empty")
	}
	return nil
}
