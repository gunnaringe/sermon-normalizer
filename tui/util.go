package tui

import (
	"bytes"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

func secs(s float64) string { return strconv.FormatFloat(s, 'f', 3, 64) }

// clock formats seconds as h:mm:ss (or m:ss under an hour) for display.
func clock(s float64) string {
	t := normalizer.FormatTime(math.Round(math.Max(s, 0)))
	if rest, ok := strings.CutPrefix(t, "0:"); ok {
		return strings.TrimPrefix(rest, "0")
	}
	return t
}

// wrap word-wraps text to the terminal width, breaking only at spaces so
// flags like --fade-in stay whole.
func wrap(style lipgloss.Style, text string, width int) string {
	width = max(width-1, 20)
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		switch {
		case line == "":
			line = w
		case len([]rune(line))+1+len([]rune(w)) <= width:
			line += " " + w
		default:
			lines = append(lines, style.Render(line))
			line = w
		}
	}
	return strings.Join(append(lines, style.Render(line)), "\n")
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// lineWriter turns writes into whole lines for fn; the pipeline's report is
// written through a tabwriter in pieces, so partial lines are buffered.
type lineWriter struct {
	fn  func(string)
	buf []byte
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		w.fn(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}
