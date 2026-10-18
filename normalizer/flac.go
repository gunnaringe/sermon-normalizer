package normalizer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var reMD5 = regexp.MustCompile(`MD5=(\w+)`)

// AudioMD5 is the MD5 of the decoded audio of path: equal for a WAV and a
// lossless FLAC of it.
func (f FFmpeg) AudioMD5(ctx context.Context, path string) (string, error) {
	res, err := f.Run(ctx, "-v", "error", "-i", path, "-map", "0:a", "-f", "md5", "-")
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

// ToFlac makes dst, a FLAC with exactly the audio of src, and leaves src
// alone. ext is src's format as an extension (.wav or .flac; src itself
// may have none). A WAV is converted (tags kept, so a recording date survives) and
// checked with AudioMD5; a FLAC is copied and checked byte for byte. dst
// appears only when complete and verified. progress, if set, gets the
// seconds converted.
func (f FFmpeg) ToFlac(ctx context.Context, src, ext, dst string, progress func(seconds float64)) error {
	switch strings.ToLower(ext) {
	case ".flac":
		tmp := strings.TrimSuffix(dst, filepath.Ext(dst)) + ".partial.flac"
		if err := CopyFile(src, tmp); err != nil {
			os.Remove(tmp)
			return err
		}
		if same, err := SameContent(src, tmp); err != nil || !same {
			os.Remove(tmp)
			if err == nil {
				err = fmt.Errorf("copy of %s differs from the original", filepath.Base(src))
			}
			return err
		}
		return os.Rename(tmp, dst)
	case ".wav":
		if err := f.RunToProgress(ctx, dst, progress, "-f", "wav", "-i", src, "-map", "0:a:0", "-map_metadata", "0", "-c:a", "flac"); err != nil {
			return err
		}
		same, err := f.SameAudio(ctx, src, dst)
		if err == nil && !same {
			err = fmt.Errorf("%s is not identical to %s", filepath.Base(dst), filepath.Base(src))
		}
		if err != nil {
			os.Remove(dst)
		}
		return err
	}
	return fmt.Errorf("%s: only WAV and FLAC recordings are accepted", filepath.Base(src))
}

// IsPartial reports whether name is a file being written (see RunTo), not
// a finished one.
func IsPartial(name string) bool {
	return strings.Contains(name, ".partial")
}
