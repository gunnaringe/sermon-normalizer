package normalizer

import (
	"errors"
	"strconv"
	"strings"
)

// Validate checks the options on their own, before any file is looked at.
// The messages name CLI flags; every front end uses the same rules.
func (o Options) Validate() error {
	switch {
	case o.Outputs == (Outputs{}):
		return errors.New("choose at least one output (--formats)")
	case o.Start < 0 || o.End < 0:
		return errors.New("--start and --end cannot be negative")
	case o.End > 0 && o.End <= o.Start:
		return errors.New("--end must be after --start")
	case o.FadeIn < 0 || o.FadeOut < 0:
		return errors.New("--fade-in and --fade-out cannot be negative")
	case o.NoiseReduction < 0 || o.NoiseReduction > 97:
		return errors.New("--noise-reduction must be from 0 to 97 dB")
	case o.TargetLUFS < -70 || o.TargetLUFS > -5:
		return errors.New("--target-lufs must be from -70 to -5")
	case o.TruePeak < -20 || o.TruePeak > 0:
		return errors.New("--true-peak must be from -20 to 0 dBTP")
	case strings.TrimSpace(o.Mp3Bitrate) == "" || strings.TrimSpace(o.OpusBitrate) == "":
		return errors.New("bitrates cannot be empty")
	}
	return nil
}

// CommandLine is the CLI command for the same run, with only the flags that
// differ from the defaults (for the chosen channels).
func CommandLine(folder string, opt Options, archive string) string {
	def := DefaultOptions()
	def.SetStereo(opt.Stereo)
	args := []string{"sermon-normalizer-cli", "run", shellQuote(folder)}
	add := func(flag, value string) { args = append(args, flag, shellQuote(value)) }
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	if opt.Start > 0 {
		add("--start", FormatTime(opt.Start))
	}
	if opt.End > 0 {
		add("--end", FormatTime(opt.End))
	}
	if opt.Outputs != def.Outputs {
		add("--formats", opt.Outputs.String())
	}
	if opt.Stereo {
		add("--channels", "stereo")
	}
	if opt.FadeIn > 0 {
		add("--fade-in", num(opt.FadeIn))
	}
	if opt.FadeOut > 0 {
		add("--fade-out", num(opt.FadeOut))
	}
	if opt.NoiseReduction != def.NoiseReduction {
		add("--noise-reduction", strconv.Itoa(opt.NoiseReduction))
	}
	if opt.TargetLUFS != def.TargetLUFS {
		add("--target-lufs", num(opt.TargetLUFS))
	}
	if opt.TruePeak != def.TruePeak {
		add("--true-peak", num(opt.TruePeak))
	}
	if opt.Mp3Bitrate != def.Mp3Bitrate {
		add("--mp3-bitrate", opt.Mp3Bitrate)
	}
	if opt.OpusBitrate != def.OpusBitrate {
		add("--opus-bitrate", opt.OpusBitrate)
	}
	if archive != "" {
		add("--archive", archive)
	}
	if opt.KeepWav {
		args = append(args, "--keep-wav")
	}
	if opt.Force {
		args = append(args, "--force")
	}
	return strings.Join(args, " ")
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " '\"\\$`&|;<>()*?!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
