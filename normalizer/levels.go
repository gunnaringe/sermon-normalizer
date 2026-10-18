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

// LevelRate is the sample rate levels are computed at: plenty for loudness,
// and keeps an hour of audio to ~58 MB streamed through a pipe. ReadLevels
// expects mono 16-bit little-endian PCM at this rate (-ac 1 -ar 8000 -f s16le).
const LevelRate = 8000

// LevelData is the level of every window of a recording.
type LevelData struct {
	Window float64   // seconds per value
	RMS    []float64 // dBFS, at least SilenceFloor: loudness, for silences
	Peak   []float64 // 0..1: the highest sample, for drawing a waveform
}

// SilenceFloor is the level (dBFS) reported for digital silence.
const SilenceFloor = -90.0

// Levels returns the RMS level in dBFS of every window seconds of path,
// mixed to mono. The audio is streamed, never held in memory.
func (f FFmpeg) Levels(ctx context.Context, path string, window float64) ([]float64, error) {
	cmd := exec.CommandContext(ctx, f.Bin, "-nostdin", "-hide_banner", "-v", "error",
		"-i", path, "-ac", "1", "-ar", fmt.Sprint(LevelRate), "-f", "s16le", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := ReadLevels(out, window, nil)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ffmpeg failed (%w):\n%s", err, lastLines(stderr.String(), 15))
	}
	return data.RMS, readErr
}

// ReadLevels computes the levels of every window seconds of PCM read from r
// (see LevelRate). progress, if set, gets the seconds read about once a second.
func ReadLevels(r io.Reader, window float64, progress func(seconds float64)) (LevelData, error) {
	n := max(int(window*LevelRate), 1)
	data := LevelData{Window: window}
	var sum, peak float64
	var count, total int
	flush := func() {
		ms := sum / float64(count) / (32768 * 32768)
		data.RMS = append(data.RMS, math.Max(10*math.Log10(ms), SilenceFloor))
		data.Peak = append(data.Peak, peak/32768)
		sum, peak, count = 0, 0, 0
	}
	br := bufio.NewReaderSize(r, 1<<16)
	var sample [2]byte
	for {
		if _, err := io.ReadFull(br, sample[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return data, err
		}
		v := float64(int16(binary.LittleEndian.Uint16(sample[:])))
		sum += v * v
		peak = math.Max(peak, math.Abs(v))
		count++
		total++
		if count == n {
			flush()
		}
		if progress != nil && total%LevelRate == 0 {
			progress(float64(total) / LevelRate)
		}
	}
	if count > 0 {
		flush()
	}
	return data, nil
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
