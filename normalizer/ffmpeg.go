package normalizer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FFmpeg calls ffmpeg with an argument list (never through a shell).
type FFmpeg struct {
	Bin string
}

// Result holds what ffmpeg wrote to stdout and stderr.
type Result struct {
	Stdout string
	Stderr string
}

// Run runs ffmpeg. -nostdin stops ffmpeg from waiting for key presses.
func (f FFmpeg) Run(ctx context.Context, args ...string) (Result, error) {
	full := append([]string{"-nostdin", "-hide_banner", "-nostats"}, args...)
	cmd := exec.CommandContext(ctx, f.Bin, full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	res := func() Result { return Result{Stdout: out.String(), Stderr: errb.String()} }
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return res(), ctx.Err()
		}
		return res(), fmt.Errorf("ffmpeg failed (%w):\n%s", err, lastLines(errb.String(), 15))
	}
	return res(), nil
}

// RunTo runs ffmpeg with dst as the output file. ffmpeg writes to a temporary
// file next to dst, which is renamed only on success, so an interrupted or
// failed run never leaves a half-written dst that a later run would skip.
func (f FFmpeg) RunTo(ctx context.Context, dst string, args ...string) error {
	ext := filepath.Ext(dst) // ffmpeg picks the format from the extension
	tmp := strings.TrimSuffix(dst, ext) + ".partial" + ext
	if _, err := f.Run(ctx, append(append([]string{"-y"}, args...), tmp)...); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
