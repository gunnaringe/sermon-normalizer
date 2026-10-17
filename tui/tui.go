// Package tui is an interactive front end for the normalizer: pick a date
// folder, choose how to cut out the sermon, set options and run.
package tui

import (
	"context"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// Config is how the TUI starts.
type Config struct {
	Dir     string // a date folder, or a folder of date folders
	Archive string // default archive folder ("" = none)
	FFmpeg  normalizer.FFmpeg
	Options normalizer.Options // defaults for the options screen
}

type screen int

const (
	screenBrowser screen = iota
	screenCut
	screenOptions
	screenRun
)

type model struct {
	ctx      context.Context
	cfg      Config
	archive  string // last archive folder used
	player   *player
	width    int
	height   int
	screen   screen
	quitting bool

	browser browserModel
	cut     cutModel
	opts    optionsModel
	run     runModel
	initCmd tea.Cmd
}

func newModel(ctx context.Context, cfg Config) (model, error) {
	dir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return model{}, err
	}
	m := model{ctx: ctx, cfg: cfg, archive: cfg.Archive, width: 80, height: 24,
		player: &player{bin: findFFplay(cfg.FFmpeg)}}
	if _, isDate, _ := folderStatus(dir, cfg.Archive); isDate {
		// Opened straight on a date folder: its parent is the folder list.
		m.browser = newBrowser(filepath.Dir(dir), cfg.Archive)
		m.selectInBrowser(dir)
		m, m.initCmd = m.openFolder(dir)
	} else {
		m.browser = newBrowser(dir, cfg.Archive)
	}
	return m, nil
}

// Run starts the TUI and returns when the user quits. A run in progress is
// cancelled and waited for, so no ffmpeg or .partial file is left behind.
func Run(ctx context.Context, cfg Config) error {
	m, err := newModel(ctx, cfg)
	if err != nil {
		return err
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if fm, ok := final.(model); ok {
		fm.player.stop()
		fm.cut.close()
		if fm.run.finished != nil {
			fm.run.stop()
			<-fm.run.finished
		}
	}
	if err == tea.ErrProgramKilled && ctx.Err() != nil {
		return nil // interrupted from outside (signal)
	}
	return err
}

func (m model) Init() tea.Cmd { return m.initCmd }

func (m *model) selectInBrowser(path string) {
	for i, e := range m.browser.entries {
		if e.path == path {
			m.browser.cursor = i
		}
	}
}

func (m model) openFolder(path string) (model, tea.Cmd) {
	paths, err := normalizer.NewPaths(path, m.archive)
	if err != nil {
		m.browser.err = err
		return m, nil
	}
	var cmd tea.Cmd
	m.cut, cmd = newCut(m.ctx, paths, m.cfg.FFmpeg, m.player, m.width)
	m.screen = screenCut
	return m, cmd
}

func (m model) running() bool { return m.screen == screenRun && !m.run.done }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.cut.width = msg.Width
		m.run.bar.Width = max(min(msg.Width-12, 60), 10)
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		// Keys typed faster than a redraw arrive as one message ("]]s"):
		// handle them one at a time, as if typed slowly. Pastes stay whole.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			var next tea.Model = m
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				var cmd tea.Cmd
				next, cmd = next.(model).key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
				cmds = append(cmds, cmd)
			}
			return next, tea.Batch(cmds...)
		}
		return m.key(msg)

	case openFolderMsg:
		return m.openFolder(msg.path)

	case backMsg:
		m.cut.close()
		m.browser.archive = m.archive
		m.browser.refresh()
		m.selectInBrowser(m.cut.paths.Folder)
		m.screen = screenBrowser
		return m, nil

	case cutDoneMsg:
		m.player.stop()
		m.opts = newOptions(m.cut.paths, msg, m.cfg.Options, m.archive)
		m.screen = screenOptions
		return m, nil

	case backToCutMsg:
		m.screen = screenCut
		return m, nil

	case startRunMsg:
		paths, err := normalizer.NewPaths(m.cut.paths.Folder, msg.archive)
		if err != nil {
			m.opts.msg = err.Error()
			return m, nil
		}
		m.cut.close()
		m.archive = msg.archive
		// The next folder starts from these choices, except the cut and Force.
		m.cfg.Options = msg.opt
		m.cfg.Options.Start, m.cfg.Options.End, m.cfg.Options.Force = 0, 0, false
		var cmd tea.Cmd
		m.run, cmd = startRun(m.ctx, m.cfg.FFmpeg, paths, msg.opt, m.width)
		m.screen = screenRun
		return m, cmd

	case playDoneMsg:
		m.player.done(msg)
		return m, nil

	case levelsMsg, editLengthMsg:
		var cmd tea.Cmd
		m.cut, cmd = m.cut.update(msg)
		return m, cmd

	case runDoneMsg:
		m.run, _ = m.run.update(msg)
		if m.quitting {
			return m, tea.Quit
		}
		return m, nil

	case logMsg, stepMsg, progressMsg:
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}
	if m.screen == screenRun {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}
	return m, nil
}

// quit leaves the TUI; a running pipeline is cancelled first and the program
// quits when it reports back.
func (m model) quit() (tea.Model, tea.Cmd) {
	m.player.stop()
	m.cut.close()
	if m.running() {
		m.quitting = true
		m.run.stop()
		return m, nil
	}
	return m, tea.Quit
}

func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenBrowser:
		if msg.String() == "q" || msg.String() == "esc" {
			return m.quit()
		}
		m.browser, cmd = m.browser.update(msg)
	case screenCut:
		m.cut, cmd = m.cut.update(msg)
	case screenOptions:
		m.opts, cmd = m.opts.update(msg)
	case screenRun:
		switch {
		case !m.run.done && (msg.String() == "esc" || msg.String() == "q"):
			m.run.stop()
		case m.run.done && msg.String() == "q":
			return m, tea.Quit
		case m.run.done && (msg.String() == "enter" || msg.String() == "esc"):
			return m, func() tea.Msg { return backMsg{} }
		}
	}
	return m, cmd
}

func (m model) View() string {
	switch m.screen {
	case screenCut:
		return m.cut.view(m.height)
	case screenOptions:
		return m.opts.view(m.width)
	case screenRun:
		return m.run.view(m.height)
	}
	return m.browser.view(m.width, m.height)
}
