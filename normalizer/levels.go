package normalizer

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strings"
)

// levelRate is the sample rate levels are computed at: plenty for loudness,
// and keeps an hour of audio to ~58 MB streamed through a pipe.
const levelRate = 8000

// SilenceFloor is the level (dBFS) reported for digital silence.
const SilenceFloor = -90.0

// Levels returns the RMS level in dBFS of every window seconds of path,
// mixed to mono. The audio is streamed, never held in memory.
func (f FFmpeg) Levels(ctx context.Context, path string, window float64) ([]float64, error) {
	cmd := exec.CommandContext(ctx, f.Bin, "-nostdin", "-hide_banner", "-v", "error",
		"-i", path, "-ac", "1", "-ar", fmt.Sprint(levelRate), "-f", "s16le", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	levels, readErr := readLevels(bufio.NewReaderSize(out, 1<<16), int(window*levelRate))
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ffmpeg failed (%w):\n%s", err, lastLines(stderr.String(), 15))
	}
	return levels, readErr
}

// readLevels computes the RMS level of every n samples of 16-bit little-endian PCM.
func readLevels(r io.Reader, n int) ([]float64, error) {
	if n < 1 {
		n = 1
	}
	var levels []float64
	var sum float64
	var count int
	flush := func() {
		ms := sum / float64(count) / (32768 * 32768)
		levels = append(levels, math.Max(10*math.Log10(ms), SilenceFloor))
		sum, count = 0, 0
	}
	var sample [2]byte
	for {
		if _, err := io.ReadFull(r, sample[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return levels, err
		}
		v := float64(int16(binary.LittleEndian.Uint16(sample[:])))
		sum += v * v
		count++
		if count == n {
			flush()
		}
	}
	if count > 0 {
		flush()
	}
	return levels, nil
}

// Span is a stretch of time in seconds.
type Span struct{ Start, End float64 }

// Silences finds the quiet stretches of at least minLen seconds in levels
// (one per window seconds). Quiet is relative to the recording: near its
// noise floor, and well below its typical level.
func Silences(levels []float64, window, minLen float64) []Span {
	if len(levels) == 0 {
		return nil
	}
	sorted := append([]float64(nil), levels...)
	sort.Float64s(sorted)
	floor := sorted[len(sorted)/20] // 5th percentile
	median := sorted[len(sorted)/2]
	threshold := math.Min(floor+12, median-15)

	var spans []Span
	start := -1
	for i := 0; i <= len(levels); i++ {
		quiet := i < len(levels) && levels[i] <= threshold
		switch {
		case quiet && start < 0:
			start = i
		case !quiet && start >= 0:
			if float64(i-start)*window >= minLen {
				spans = append(spans, Span{float64(start) * window, float64(i) * window})
			}
			start = -1
		}
	}
	return spans
}
