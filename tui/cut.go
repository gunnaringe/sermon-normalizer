package tui

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// cutMode is how the sermon is cut out of the recording.
type cutMode int

const (
	modeTimeline cutMode = iota // mark start/end on the volume timeline
	modeTimes                   // type start/end
	modeEdit                    // cut in an external editor, supply <date>_edit
)

var modeNames = []string{"1 Timeline", "2 Enter times", "3 Edit file (external editor)"}

// levelWindow is the time resolution of the timeline, in seconds.
const levelWindow = 0.1

type cutModel struct {
	paths  normalizer.Paths
	src    normalizer.Sources
	ff     normalizer.FFmpeg
	player *player
	width  int
	mode   cutMode

	// Shared by the timeline and the typed times; 0 = beginning / end.
	start, end float64

	// timeline
	loading             bool
	loadErr             error
	levels              []float64
	lo, hi              float64 // display range in dB
	silences            []normalizer.Span
	cursor              float64
	viewStart, viewSpan float64
	cancelLoad          context.CancelFunc

	// typed times
	inputs [2]textinput.Model
	focus  int

	editLength float64
	msg        string // validation error or notice
}

type levelsMsg struct {
	path   string
	levels []float64
	err    error
}

type editLengthMsg struct {
	path   string
	length float64
}

// cutDoneMsg continues to the options with the chosen cut.
type cutDoneMsg struct {
	mode       cutMode
	start, end float64
}

// backMsg returns to the folder list.
type backMsg struct{}

func newCut(ctx context.Context, paths normalizer.Paths, ff normalizer.FFmpeg, pl *player, width int) (cutModel, tea.Cmd) {
	m := cutModel{paths: paths, ff: ff, player: pl, width: width}
	for i, placeholder := range []string{"beginning", "end"} {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = 12
		ti.Width = 12
		m.inputs[i] = ti
	}
	m.src = paths.Sources()
	switch {
	case m.src.EditInFolder || m.src.Raw == "":
		m.mode = modeEdit
	default:
		m.mode = modeTimeline
	}
	var cmds []tea.Cmd
	if m.src.Raw != "" {
		m.loading = true
		lctx, cancel := context.WithCancel(ctx)
		m.cancelLoad = cancel
		raw := m.src.Raw
		cmds = append(cmds, func() tea.Msg {
			levels, err := ff.Levels(lctx, raw, levelWindow)
			return levelsMsg{raw, levels, err}
		})
	}
	cmds = append(cmds, m.editLengthCmd())
	return m, tea.Batch(cmds...)
}

func (m cutModel) editLengthCmd() tea.Cmd {
	if m.src.Edit == "" {
		return nil
	}
	ff, edit := m.ff, m.src.Edit
	return func() tea.Msg {
		d, _ := ff.Duration(context.Background(), edit)
		return editLengthMsg{edit, d}
	}
}

// close stops background work when leaving the screen.
func (m *cutModel) close() {
	if m.cancelLoad != nil {
		m.cancelLoad()
	}
	if m.player != nil {
		m.player.stop()
	}
}

func (m cutModel) duration() float64 { return float64(len(m.levels)) * levelWindow }

func (m cutModel) cols() int { return max(m.width-4, 20) }

// endOrDuration is the end mark, or the end of the recording when unset.
func (m cutModel) endOrDuration() float64 {
	if m.end > 0 {
		return m.end
	}
	return m.duration()
}

func (m cutModel) update(msg tea.Msg) (cutModel, tea.Cmd) {
	switch msg := msg.(type) {
	case levelsMsg:
		if msg.path != m.src.Raw {
			return m, nil
		}
		m.loading = false
		m.loadErr = msg.err
		m.levels = msg.levels
		m.setRange()
		m.silences = normalizer.Silences(m.levels, levelWindow, 1)
		m.viewStart, m.viewSpan = 0, m.duration()
		return m, nil
	case editLengthMsg:
		if msg.path == m.src.Edit {
			m.editLength = msg.length
		}
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// setRange picks the dB range for the bars from the recording itself.
func (m *cutModel) setRange() {
	if len(m.levels) == 0 {
		return
	}
	sorted := append([]float64(nil), m.levels...)
	sort.Float64s(sorted)
	m.lo = clamp(sorted[len(sorted)/20]-3, -90, -20)
	m.hi = math.Max(sorted[len(sorted)-1]+1, m.lo+10)
}

func (m cutModel) key(msg tea.KeyMsg) (cutModel, tea.Cmd) {
	m.msg = ""
	k := msg.String()
	switch k {
	case "esc":
		return m, func() tea.Msg { return backMsg{} }
	case "tab":
		m.setMode((m.mode + 1) % 3)
		return m, nil
	case "shift+tab":
		m.setMode((m.mode + 2) % 3)
		return m, nil
	case "enter":
		done, err := m.validate()
		if err != "" {
			m.msg = err
			return m, nil
		}
		return m, func() tea.Msg { return done }
	}
	if m.mode != modeTimes {
		switch k {
		case "1", "2", "3":
			m.setMode(cutMode(k[0] - '1'))
			return m, nil
		}
	}
	switch m.mode {
	case modeTimeline:
		return m.timelineKey(k)
	case modeTimes:
		return m.timesKey(msg)
	case modeEdit:
		if k == "r" {
			m.src = m.paths.Sources()
			m.editLength = 0
			if m.src.Edit == "" {
				m.msg = "Still no edit file."
			}
			return m, m.editLengthCmd()
		}
	}
	return m, nil
}

func (m *cutModel) setMode(mode cutMode) {
	if m.mode == modeTimes && mode != modeTimes {
		if err := m.readInputs(); err != "" {
			m.msg = err
		}
	}
	if mode == modeTimes {
		for i, v := range []float64{m.start, m.end} {
			m.inputs[i].SetValue("")
			if v > 0 {
				m.inputs[i].SetValue(normalizer.FormatTime(v))
			}
		}
		m.focus = 0
		m.inputs[0].Focus()
		m.inputs[1].Blur()
	}
	m.player.stop()
	m.mode = mode
}

// readInputs parses the typed times into start/end.
func (m *cutModel) readInputs() string {
	var v [2]float64
	for i, name := range []string{"Start", "End"} {
		s := strings.TrimSpace(m.inputs[i].Value())
		if s == "" {
			continue
		}
		t, err := normalizer.ParseTime(s)
		if err != nil {
			return name + ": " + err.Error()
		}
		v[i] = t
	}
	m.start, m.end = v[0], v[1]
	return ""
}

func (m cutModel) validate() (cutDoneMsg, string) {
	if m.mode == modeEdit {
		if m.src.Edit == "" {
			return cutDoneMsg{}, "No edit file yet: save it as shown and press r to rescan."
		}
		return cutDoneMsg{mode: modeEdit}, ""
	}
	if m.mode == modeTimes {
		if err := m.readInputs(); err != "" {
			return cutDoneMsg{}, err
		}
	}
	switch {
	case m.src.Raw == "":
		return cutDoneMsg{}, "There is no raw recording to cut."
	case m.src.EditInFolder:
		return cutDoneMsg{}, filepath.Base(m.src.Edit) + " is in the folder and would conflict: choose Edit file, or move it away."
	case m.start == 0 && m.end == 0 && m.mode == modeTimeline:
		return cutDoneMsg{}, "Mark where the sermon starts (s) and/or ends (e)."
	case m.start == 0 && m.end == 0:
		return cutDoneMsg{}, "Give a start and/or an end."
	case m.end > 0 && m.end <= m.start:
		return cutDoneMsg{}, "The end must be after the start."
	case len(m.levels) > 0 && m.start >= m.duration():
		return cutDoneMsg{}, "The start is after the end of the recording."
	}
	return cutDoneMsg{mode: m.mode, start: m.start, end: m.end}, ""
}

func (m cutModel) timesKey(msg tea.KeyMsg) (cutModel, tea.Cmd) {
	switch msg.String() {
	case "up", "down":
		m.inputs[m.focus].Blur()
		m.focus = 1 - m.focus
		m.inputs[m.focus].Focus()
		return m, nil
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m cutModel) timelineKey(k string) (cutModel, tea.Cmd) {
	if len(m.levels) == 0 {
		return m, nil
	}
	column := m.viewSpan / float64(m.cols())
	move := func(d float64) { m.cursor = clamp(m.cursor+d, 0, m.duration()) }
	switch k {
	case "left", "h":
		move(-column)
	case "right", "l":
		move(column)
	case "shift+left", "H":
		move(-1)
	case "shift+right", "L":
		move(1)
	case "pgup":
		move(-60)
	case "pgdown":
		move(60)
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = m.duration()
	case "+", "=":
		m.zoom(0.5)
	case "-":
		m.zoom(2)
	case "0":
		m.viewStart, m.viewSpan = 0, m.duration()
	case "[":
		m.cursor = m.boundary(-1)
	case "]":
		m.cursor = m.boundary(1)
	case "s":
		m.start = m.cursor
		if m.end > 0 && m.end <= m.start {
			m.end = 0
		}
	case "e":
		if m.cursor <= m.start {
			m.msg = "The end must be after the start."
		} else {
			m.end = m.cursor
		}
	case "x":
		m.start, m.end = 0, 0
	case " ":
		if m.player.playing() {
			m.player.stop()
		} else {
			m.play(m.cursor, 0)
		}
	case "S":
		m.play(m.start, 5)
	case "E":
		m.play(math.Max(m.endOrDuration()-5, 0), 5)
	}
	m.follow()
	return m, nil
}

func (m *cutModel) play(from, dur float64) {
	if !m.player.available() {
		m.msg = "Playback unavailable: ffplay not found."
		return
	}
	if err := m.player.play(m.src.Raw, from, dur); err != nil {
		m.msg = "Playback failed: " + err.Error()
	}
}

// zoom scales the visible span around the cursor.
func (m *cutModel) zoom(factor float64) {
	minSpan := float64(m.cols()) * levelWindow
	m.viewSpan = clamp(m.viewSpan*factor, math.Min(minSpan, m.duration()), m.duration())
	m.viewStart = m.cursor - m.viewSpan/2
	m.follow()
}

// follow keeps the cursor inside the view.
func (m *cutModel) follow() {
	if m.cursor < m.viewStart {
		m.viewStart = m.cursor
	}
	if m.cursor > m.viewStart+m.viewSpan {
		m.viewStart = m.cursor - m.viewSpan
	}
	m.viewStart = clamp(m.viewStart, 0, math.Max(m.duration()-m.viewSpan, 0))
}

// boundary is the nearest silence edge before (dir < 0) or after the cursor.
func (m cutModel) boundary(dir int) float64 {
	var edges []float64
	for _, s := range m.silences {
		edges = append(edges, s.Start, s.End)
	}
	if dir < 0 {
		for i := len(edges) - 1; i >= 0; i-- {
			if edges[i] < m.cursor-0.05 {
				return edges[i]
			}
		}
		return 0
	}
	for _, e := range edges {
		if e > m.cursor+0.05 {
			return e
		}
	}
	return m.duration()
}

func (m cutModel) view(height int) string {
	width := m.width
	var s strings.Builder
	s.WriteString(titleStyle.Render(m.paths.Date) + subtleStyle.Render("  choose how to cut out the sermon") + "\n\n")
	for i, name := range modeNames {
		style := inactiveTab
		if cutMode(i) == m.mode {
			style = activeTab
		}
		s.WriteString(style.Render(name) + "   ")
	}
	s.WriteString("\n\n")
	switch m.mode {
	case modeTimeline:
		s.WriteString(m.timelineView(height))
	case modeTimes:
		s.WriteString(m.timesView())
	case modeEdit:
		s.WriteString(m.editView())
	}
	if m.msg != "" {
		s.WriteString("\n" + wrap(errorStyle, m.msg, width) + "\n")
	}
	return s.String()
}

func (m cutModel) sourceLine() string {
	if m.src.Raw == "" {
		return subtleStyle.Render("No raw recording (" + m.paths.Date + "_raw.wav/.flac) found.")
	}
	line := "Recording: " + filepath.Base(m.src.Raw)
	if len(m.levels) > 0 {
		line += "  (" + clock(m.duration()) + ")"
	}
	return line
}

func (m cutModel) timelineView(height int) string {
	var s strings.Builder
	s.WriteString(m.sourceLine() + "\n\n")
	switch {
	case m.src.Raw == "":
		s.WriteString("Nothing to show: use Edit file.\n")
		return s.String()
	case m.loading:
		s.WriteString(subtleStyle.Render("Reading levels…") + "\n")
		return s.String()
	case m.loadErr != nil:
		s.WriteString(errorStyle.Render("Could not read the recording: "+m.loadErr.Error()) + "\n")
		return s.String()
	case len(m.levels) == 0:
		s.WriteString("The recording is empty.\n")
		return s.String()
	}
	rows := clamp(float64(height-18), 3, 10)
	s.WriteString(m.bars(m.cols(), int(rows)))
	selLen := m.endOrDuration() - m.start
	s.WriteString(fmt.Sprintf("\ncursor %s  %s   ", cursorStyle.Render(clock(m.cursor)), subtleStyle.Render(fmt.Sprintf("%.0f dB", m.levelAt(m.cursor)))))
	s.WriteString(fmt.Sprintf("start %s  end %s  length %s\n",
		selectedStyle.Render(m.markText(m.start, "beginning")), selectedStyle.Render(m.markText(m.end, "end")), clock(selLen)))
	help := "←/→ move · shift+←/→ 1 s · pgup/pgdn 1 min · [/] silence · +/- zoom · s start · e end · x clear"
	if m.player.available() {
		help += " · space play/stop · S/E hear start/end"
	} else {
		help += " · (no ffplay: playback off)"
	}
	s.WriteString("\n" + wrap(helpStyle, help+" · tab mode · enter continue · esc back", m.width) + "\n")
	return s.String()
}

func (m cutModel) markText(v float64, unset string) string {
	if v <= 0 {
		return unset
	}
	return clock(v)
}

func (m cutModel) levelAt(t float64) float64 {
	i := int(t / levelWindow)
	if i >= len(m.levels) {
		i = len(m.levels) - 1
	}
	return m.levels[max(i, 0)]
}

var blocks = []rune(" ▁▂▃▄▅▆▇█")

// bars renders the visible part of the timeline: level bars, a marker row
// (cursor ▲, start [, end ], silence ·) and a time axis.
func (m cutModel) bars(cols, rows int) string {
	colTime := func(c int) float64 { return m.viewStart + float64(c)*m.viewSpan/float64(cols) }
	colOf := func(t float64) int { return int(math.Floor((t - m.viewStart) / m.viewSpan * float64(cols))) }
	cursorCol := min(colOf(m.cursor), cols-1)
	startCol, endCol := colOf(m.start), min(colOf(m.endOrDuration()), cols-1)

	units := make([]int, cols)
	for c := 0; c < cols; c++ {
		i0 := int(colTime(c) / levelWindow)
		i1 := max(int(math.Ceil(colTime(c+1)/levelWindow)), i0+1)
		peak := normalizer.SilenceFloor
		for i := i0; i < i1 && i < len(m.levels); i++ {
			peak = math.Max(peak, m.levels[i])
		}
		frac := clamp((peak-m.lo)/(m.hi-m.lo), 0, 1)
		units[c] = int(math.Round(frac * float64(rows*8)))
	}
	styleOf := func(c int) int { // 0 outside, 1 selected, 2 cursor
		switch {
		case c == cursorCol:
			return 2
		case c >= startCol && c <= endCol:
			return 1
		}
		return 0
	}
	render := func(style int, text string) string {
		switch style {
		case 1:
			return selectedStyle.Render(text)
		case 2:
			return cursorStyle.Render(text)
		}
		return subtleStyle.Render(text)
	}
	var s strings.Builder
	for r := 0; r < rows; r++ {
		base := (rows - 1 - r) * 8
		var run strings.Builder
		runStyle := -1
		s.WriteString("  ")
		for c := 0; c < cols; c++ {
			v := min(max(units[c]-base, 0), 8)
			if st := styleOf(c); st != runStyle {
				if runStyle >= 0 {
					s.WriteString(render(runStyle, run.String()))
				}
				run.Reset()
				runStyle = st
			}
			run.WriteRune(blocks[v])
		}
		s.WriteString(render(runStyle, run.String()) + "\n")
	}
	marks := []rune(strings.Repeat(" ", cols))
	for _, sp := range m.silences {
		for c := max(colOf(sp.Start), 0); c <= min(colOf(sp.End), cols-1); c++ {
			marks[c] = '·'
		}
	}
	if startCol >= 0 && startCol < cols {
		marks[startCol] = '['
	}
	if endCol >= 0 && endCol < cols {
		marks[endCol] = ']'
	}
	if cursorCol >= 0 {
		marks[cursorCol] = '▲'
	}
	s.WriteString("  " + string(marks) + "\n")
	axis := []rune(strings.Repeat(" ", cols))
	for c := 0; c < cols; c += 16 {
		label := []rune(clock(colTime(c)))
		if c+len(label) <= cols {
			copy(axis[c:], label)
		}
	}
	s.WriteString("  " + subtleStyle.Render(string(axis)) + "\n")
	return s.String()
}

func (m cutModel) timesView() string {
	var s strings.Builder
	s.WriteString(m.sourceLine() + "\n\n")
	labels := []string{"Start", "End"}
	hints := []string{"empty = from the beginning", "empty = to the end"}
	for i := range m.inputs {
		marker := "  "
		if i == m.focus {
			marker = cursorStyle.Render("› ")
		}
		s.WriteString(fmt.Sprintf("%s%-6s %s  %s\n", marker, labels[i], m.inputs[i].View(), subtleStyle.Render(hints[i])))
	}
	s.WriteString("\n" + subtleStyle.Render("Times as h:mm:ss, mm:ss or seconds (41:20.5). Marks set on the timeline show up here.") + "\n")
	s.WriteString("\n" + helpStyle.Render("↑/↓ field · tab mode · enter continue · esc back") + "\n")
	return s.String()
}

func (m cutModel) editView() string {
	var s strings.Builder
	s.WriteString("Cut the sermon in your audio editor and save it in the date folder as:\n\n")
	s.WriteString("  " + titleStyle.Render(filepath.Join(m.paths.Folder, m.paths.Date+"_edit.flac")) + subtleStyle.Render("  (or .wav)") + "\n\n")
	if m.src.Raw != "" {
		s.WriteString("Open this recording in the editor:\n  " + m.src.Raw + "\n\n")
	}
	switch {
	case m.src.Edit == "":
		s.WriteString(errorStyle.Render("Not found yet.") + " Press r to look again when it is saved.\n")
	default:
		found := "Found " + filepath.Base(m.src.Edit)
		if !m.src.EditInFolder {
			found += " (in the archive)"
		}
		if m.editLength > 0 {
			found += "  (" + clock(m.editLength) + ")"
		}
		s.WriteString(okStyle.Render("✓ "+found) + "\n")
	}
	s.WriteString("\n" + helpStyle.Render("r rescan · tab mode · enter continue · esc back") + "\n")
	return s.String()
}
