package normalizer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return f.run(ctx, nil, args)
}

// run runs ffmpeg; with progress set, ffmpeg reports how many seconds of
// output it has written, which are passed to progress as they arrive.
func (f FFmpeg) run(ctx context.Context, progress func(seconds float64), args []string) (Result, error) {
	full := []string{"-nostdin", "-hide_banner", "-nostats"}
	if progress != nil {
		// Every 0.1 s instead of 0.5: on a fast machine a step can be over before the first report.
		full = append(full, "-progress", "pipe:1", "-stats_period", "0.1")
	}
	cmd := exec.CommandContext(ctx, f.Bin, append(full, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	if progress != nil {
		cmd.Stdout = &progressWriter{fn: progress}
	}
	cmd.Stderr = &errb
	// ffmpeg on Windows ends its lines with \r\n; the parsers expect \n.
	res := func() Result {
		return Result{Stdout: out.String(), Stderr: strings.ReplaceAll(errb.String(), "\r\n", "\n")}
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return res(), ctx.Err()
		}
		return res(), fmt.Errorf("ffmpeg failed (%w):\n%s", err, lastLines(res().Stderr, 15))
	}
	return res(), nil
}

// RunTo runs ffmpeg with dst as the output file. ffmpeg writes to a temporary
// file next to dst, which is renamed only on success, so an interrupted or
// failed run never leaves a half-written dst that a later run would skip.
func (f FFmpeg) RunTo(ctx context.Context, dst string, args ...string) error {
	return f.RunToProgress(ctx, dst, nil, args...)
}

// RunToProgress is RunTo that reports the seconds of output written so far.
func (f FFmpeg) RunToProgress(ctx context.Context, dst string, progress func(seconds float64), args ...string) error {
	ext := filepath.Ext(dst) // ffmpeg picks the format from the extension
	tmp := strings.TrimSuffix(dst, ext) + ".partial" + ext
	if _, err := f.run(ctx, progress, append(append([]string{"-y"}, args...), tmp)); err != nil {
		Remove(tmp)
		return err
	}
	return Rename(tmp, dst)
}

// progressWriter reads ffmpeg's -progress output (key=value lines) and
// passes out_time_us on as seconds.
type progressWriter struct {
	fn  func(seconds float64)
	buf []byte
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && us >= 0 {
				w.fn(float64(us) / 1e6)
			}
		}
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
