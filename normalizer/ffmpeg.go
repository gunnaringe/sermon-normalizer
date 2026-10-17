package normalizer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// FFmpeg kaller ffmpeg med en argumentliste (aldri via shell).
type FFmpeg struct {
	Bin string
}

// Result inneholder det ffmpeg skrev til stdout og stderr.
type Result struct {
	Stdout string
	Stderr string
}

// Run kjører ffmpeg. -nostdin hindrer at ffmpeg venter på tastetrykk.
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
		return res(), fmt.Errorf("ffmpeg feilet (%w):\n%s", err, lastLines(errb.String(), 15))
	}
	return res(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
