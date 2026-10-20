package normalizer

import (
	"errors"
	"fmt"
	"slices"
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
	case len(o.Cuts) > 0 && !o.cutting():
		return errors.New("select the sermon before cutting parts out of it")
	case o.FadeIn < 0 || o.FadeOut < 0:
		return errors.New("fades cannot be negative")
	case o.NoiseReduction < 0 || o.NoiseReduction > 97:
		return errors.New("noise reduction must be from 0 to 97 dB")
	case o.TargetLUFS < -70 || o.TargetLUFS > -5:
		return errors.New("target loudness must be from -70 to -5 LUFS")
	case o.TruePeak < -20 || o.TruePeak > 0:
		return errors.New("max true peak must be from -20 to 0 dBTP")
	case o.RoomSound < 0 || o.RoomSound > 100:
		return errors.New("room sound must be from 0 to 100")
	case !slices.Contains(LevelingNames, o.LevelingName()):
		return fmt.Errorf("leveling must be one of %s", strings.Join(LevelingNames, ", "))
	case !slices.Contains(CompressionNames, o.CompressionName()):
		return fmt.Errorf("compression must be one of %s", strings.Join(CompressionNames, ", "))
	case strings.TrimSpace(o.Mp3Bitrate) == "" || strings.TrimSpace(o.OpusBitrate) == "":
		return errors.New("bitrates cannot be empty")
	}
	for _, p := range o.Pauses {
		if p.At < 0 || p.Seconds < 0 || p.Seconds > MaxPause {
			return fmt.Errorf("a pause can be from 0 to %g seconds", MaxPause)
		}
	}
	for _, c := range o.Cuts {
		switch {
		case c.Start < 0 || c.End <= c.Start:
			return errors.New("a cut must end after it starts")
		case c.Start < o.Start || (o.End > 0 && c.End > o.End):
			return errors.New("a cut must be inside the selected sermon")
		}
	}
	return nil
}
