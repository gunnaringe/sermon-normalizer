package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

type (
	logMsg      string
	stepMsg     string
	progressMsg float64
	runDoneMsg  struct{ err error }
)

type runModel struct {
	paths      normalizer.Paths
	opt        normalizer.Options
	steps      []string
	fraction   float64
	log        []string
	done       bool
	err        error
	cancelling bool
	spinner    spinner.Model
	bar        progress.Model
	events     chan tea.Msg
	cancel     context.CancelFunc
	finished   chan struct{} // closed when the pipeline goroutine has returned
}

func waitEvent(ch chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }

// startRun runs the pipeline in the background. Everything it reports comes
// back as messages; nothing writes to the terminal directly.
func startRun(ctx context.Context, ff normalizer.FFmpeg, paths normalizer.Paths, opt normalizer.Options, width int) (runModel, tea.Cmd) {
	rctx, cancel := context.WithCancel(ctx)
	m := runModel{
		paths:    paths,
		opt:      opt,
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		bar:      progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		events:   make(chan tea.Msg, 256),
		cancel:   cancel,
		finished: make(chan struct{}),
	}
	m.bar.Width = max(min(width-12, 60), 10)
	events, finished := m.events, m.finished
	go func() {
		defer close(finished)
		lw := &lineWriter{fn: func(l string) { events <- logMsg(l) }}
		p := &normalizer.Pipeline{P: paths, Opt: opt, FF: ff, Out: lw,
			OnStep: func(t string) { events <- stepMsg(t) },
			OnProgress: func(f float64) {
				select { // progress is only a hint; drop it rather than block
				case events <- progressMsg(f):
				default:
				}
			},
		}
		err := p.Run(rctx)
		lw.flush()
		events <- runDoneMsg{err}
	}()
	return m, tea.Batch(waitEvent(m.events), m.spinner.Tick)
}

func (m runModel) update(msg tea.Msg) (runModel, tea.Cmd) {
	switch msg := msg.(type) {
	case logMsg:
		if line := string(msg); strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "== ") {
			m.log = append(m.log, line)
		}
		return m, waitEvent(m.events)
	case stepMsg:
		m.steps = append(m.steps, string(msg))
		m.fraction = 0
		return m, waitEvent(m.events)
	case progressMsg:
		m.fraction = float64(msg)
		return m, waitEvent(m.events)
	case runDoneMsg:
		m.done, m.err = true, msg.err
		return m, nil
	case spinner.TickMsg:
		if m.done {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// stop cancels a running pipeline; runDoneMsg follows once it has cleaned up.
func (m *runModel) stop() {
	if !m.done && m.cancel != nil {
		m.cancelling = true
		m.cancel()
	}
}

func (m runModel) view(height int) string {
	var s strings.Builder
	title := "Processing " + m.paths.Date
	switch {
	case m.cancelling && !m.done:
		title = "Cancelling…"
	case m.done && m.err == nil:
		title = m.paths.Date + " done"
	case m.cancelled():
		title = m.paths.Date + " cancelled"
	case m.done:
		title = m.paths.Date + " failed"
	}
	s.WriteString(titleStyle.Render(title) + "\n\n")
	for i, st := range m.steps {
		last := i == len(m.steps)-1
		switch {
		case !last || (m.done && m.err == nil):
			s.WriteString(okStyle.Render("✓ ") + st + "\n")
		case m.done:
			s.WriteString(errorStyle.Render("✗ ") + st + "\n")
		default:
			s.WriteString(m.spinner.View() + " " + st + "\n")
		}
	}
	if !m.done && len(m.steps) > 0 {
		s.WriteString("\n  " + m.bar.ViewAs(m.fraction) + "\n")
	}
	s.WriteString("\n")
	rows := max(height-len(m.steps)-12, 3)
	first := max(len(m.log)-rows, 0)
	for _, l := range m.log[first:] {
		s.WriteString(subtleStyle.Render(l) + "\n")
	}
	switch {
	case m.done && m.err == nil:
		s.WriteString("\n" + okStyle.Render("Files:") + "\n")
		for _, f := range m.outputs() {
			s.WriteString("  " + f + "\n")
		}
		s.WriteString("\n" + helpStyle.Render("enter back to folders · q quit") + "\n")
	case m.cancelled():
		s.WriteString("\nCancelled. Nothing half-written was left behind.\n")
		s.WriteString("\n" + helpStyle.Render("enter back to folders · q quit") + "\n")
	case m.done:
		s.WriteString("\n" + errorStyle.Render(m.err.Error()) + "\n")
		s.WriteString("\n" + helpStyle.Render("enter back to folders · q quit") + "\n")
	default:
		s.WriteString("\n" + helpStyle.Render("esc/ctrl+c cancel") + "\n")
	}
	return s.String()
}

func (m runModel) cancelled() bool { return m.done && errors.Is(m.err, context.Canceled) }

// outputs are the selected output files that exist.
func (m runModel) outputs() []string {
	var out []string
	for _, o := range []struct {
		on   bool
		path string
	}{{m.opt.Outputs.Flac, m.paths.Master}, {m.opt.Outputs.Mp3, m.paths.Mp3}, {m.opt.Outputs.Opus, m.paths.Opus}} {
		if _, err := os.Stat(o.path); o.on && err == nil {
			out = append(out, filepath.Base(o.path))
		}
	}
	return out
}
