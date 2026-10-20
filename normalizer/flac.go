package normalizer

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var reMD5 = regexp.MustCompile(`MD5=(\w+)`)

// AudioMD5 is the MD5 of the decoded audio of path: equal for a WAV and a
// lossless FLAC of it.
func (f FFmpeg) AudioMD5(ctx context.Context, path string) (string, error) {
	res, err := f.Run(ctx, "-v", "error", "-i", path, "-map", "0:a:0", "-f", "md5", "-")
	if err != nil {
		return "", err
	}
	m := reMD5.FindStringSubmatch(res.Stdout + res.Stderr)
	if m == nil {
		return "", errors.New("no MD5 found in ffmpeg output")
	}
	return m[1], nil
}

// SameAudio reports whether a and b decode to the same audio.
func (f FFmpeg) SameAudio(ctx context.Context, a, b string) (bool, error) {
	ha, err := f.AudioMD5(ctx, a)
	if err != nil {
		return false, err
	}
	hb, err := f.AudioMD5(ctx, b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

// IsPartial reports whether name is a file being written (see RunTo), not
// a finished one.
func IsPartial(name string) bool {
	return strings.Contains(name, ".partial")
}
