package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii) // plain text views
	os.Exit(m.Run())
}

func keys(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyTab   = tea.KeyMsg{Type: tea.KeyTab}
	keyRight = tea.KeyMsg{Type: tea.KeyRight}
	keyDown  = tea.KeyMsg{Type: tea.KeyDown}
	keySpace = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
)

// dateDir makes <tmp>/<date> with the given (empty) files.
func dateDir(t *testing.T, root, date string, files ...string) string {
	t.Helper()
	dir := filepath.Join(root, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func run[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	msg, ok := cmd().(T)
	if !ok {
		t.Fatalf("got %T, want %T", cmd(), *new(T))
	}
	return msg
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(l string) { lines = append(lines, l) }}
	w.Write([]byte("a\nb"))
	w.Write([]byte("c\n\nd"))
	w.flush()
	if strings.Join(lines, "|") != "a|bc||d" {
		t.Errorf("got %q", lines)
	}
}

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", 65: "1:05", 2480: "41:20", 4325.4: "1:12:05", -3: "0:00"} {
		if got := clock(in); got != want {
			t.Errorf("clock(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBrowser(t *testing.T) {
	root := t.TempDir()
	dateDir(t, root, "2026-10-04", "2026-10-04_raw.wav")
	dateDir(t, root, "2026-09-27", "2026-09-27_edit.flac", "2026-09-27.flac", "2026-09-27.mp3", "2026-09-27.opus")
	os.Mkdir(filepath.Join(root, "notes"), 0o755)

	b := newBrowser(root, "")
	var names []string
	for _, e := range b.entries {
		names = append(names, e.name+"="+e.status)
	}
	want := "..=|2026-10-04=raw|2026-09-27=edit · ✓ flac mp3 opus|notes="
	if strings.Join(names, "|") != want {
		t.Errorf("entries:\n got  %s\n want %s", strings.Join(names, "|"), want)
	}
	if !b.entries[1].pending || b.entries[2].pending {
		t.Error("2026-10-04 is pending, 2026-09-27 is done")
	}
	// Enter on a plain folder goes into it; on a date folder it opens it.
	b.cursor = 3
	b, _ = b.update(keyEnter)
	if b.dir != filepath.Join(root, "notes") {
		t.Fatalf("dir %s", b.dir)
	}
	b, _ = b.update(tea.KeyMsg{Type: tea.KeyBackspace})
	if b.dir != root || b.entries[b.cursor].name != "notes" {
		t.Fatalf("back up: dir %s cursor %d", b.dir, b.cursor)
	}
	b.cursor = 1
	_, cmd := b.update(keyEnter)
	if got := run[openFolderMsg](t, cmd); got.path != filepath.Join(root, "2026-10-04") {
		t.Errorf("opened %s", got.path)
	}
	if !strings.Contains(b.view(80, 24), "› 2026-10-04") {
		t.Errorf("view:\n%s", b.view(80, 24))
	}
}

// newTestCut opens the cut screen for a folder, with levels as if read by ffmpeg.
func newTestCut(t *testing.T, dir string, levels []float64) cutModel {
	t.Helper()
	paths, err := normalizer.NewPaths(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := newCut(context.Background(), paths, normalizer.FFmpeg{}, &player{}, 84)
	if levels != nil {
		m, _ = m.update(levelsMsg{path: m.src.Raw, levels: levels})
	}
	return m
}

// testLevels: 100 s at 0.1 s: music, 2 s gap at 19-21 s, speech, gap at 79-81 s, music.
func testLevels() []float64 {
	l := make([]float64, 1000)
	for i := range l {
		t := float64(i) / 10
		switch {
		case (t >= 19 && t < 21) || (t >= 79 && t < 81):
			l[i] = -90
		case t >= 21 && t < 79:
			l[i] = -30
		default:
			l[i] = -10
		}
	}
	return l
}

func TestCutTimeline(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_raw.wav")
	m := newTestCut(t, dir, testLevels())
	if m.mode != modeTimeline || m.duration() != 100 {
		t.Fatalf("mode %v duration %v", m.mode, m.duration())
	}
	// ] jumps to the next silence edge: start of the first gap, then its end.
	m, _ = m.update(keys("]"))
	m, _ = m.update(keys("]"))
	if !near(m.cursor, 21) {
		t.Fatalf("cursor %.2f, want 21", m.cursor)
	}
	m, _ = m.update(keys("s"))
	m, _ = m.update(keys("]"))
	if !near(m.cursor, 79) {
		t.Fatalf("cursor %.2f, want 79", m.cursor)
	}
	m, _ = m.update(keys("e"))
	if !near(m.start, 21) || !near(m.end, 79) {
		t.Fatalf("start %.2f end %.2f", m.start, m.end)
	}
	v := m.view(30)
	for _, want := range []string{"[", "]", "▲", "·", "start 0:21  end 1:19  length 0:58", "no ffplay"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	// One column right; zooming in keeps the cursor in view.
	before := m.cursor
	m, _ = m.update(keyRight)
	if m.cursor <= before {
		t.Error("right should move the cursor")
	}
	m, _ = m.update(keys("+"))
	if m.viewSpan >= 100 || m.cursor < m.viewStart || m.cursor > m.viewStart+m.viewSpan {
		t.Errorf("zoom: view %.1f+%.1f cursor %.1f", m.viewStart, m.viewSpan, m.cursor)
	}
	_, cmd := m.update(keyEnter)
	done := run[cutDoneMsg](t, cmd)
	if done.mode != modeTimeline || !near(done.start, 21) || !near(done.end, 79) {
		t.Errorf("done %+v", done)
	}
	// Playback without ffplay says so instead of failing.
	m, _ = m.update(keySpace)
	if !strings.Contains(m.msg, "ffplay not found") {
		t.Errorf("msg %q", m.msg)
	}
}

func near(a, b float64) bool { return a-b < 0.11 && b-a < 0.11 }

func TestCutTimesSharedWithTimeline(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_raw.flac")
	m := newTestCut(t, dir, testLevels())
	m.start = 21
	m, _ = m.update(keyTab) // to Enter times: the start mark is filled in
	if m.mode != modeTimes || m.inputs[0].Value() != "0:00:21" || m.inputs[1].Value() != "" {
		t.Fatalf("mode %v inputs %q %q", m.mode, m.inputs[0].Value(), m.inputs[1].Value())
	}
	m, _ = m.update(keyDown)
	for _, r := range "1:15" {
		m, _ = m.update(keys(string(r))) // digits go to the field, not mode shortcuts
	}
	_, cmd := m.update(keyEnter)
	done := run[cutDoneMsg](t, cmd)
	if done.mode != modeTimes || done.start != 21 || done.end != 75 {
		t.Errorf("done %+v", done)
	}
	// Back on the timeline, the typed end is the end mark.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.mode != modeTimeline || m.end != 75 {
		t.Errorf("mode %v end %v", m.mode, m.end)
	}
	// Invalid times are reported, not accepted.
	m.setMode(modeTimes)
	m.inputs[0].SetValue("1:99")
	if _, cmd := m.update(keyEnter); cmd != nil {
		t.Error("invalid time accepted")
	}
}

func TestCutEditFile(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_raw.wav")
	m := newTestCut(t, dir, testLevels())
	m, _ = m.update(keys("3"))
	if !strings.Contains(m.view(30), filepath.Join(dir, "2026-10-04_edit.flac")) {
		t.Errorf("edit view should show where to save:\n%s", m.view(30))
	}
	if m, cmd := m.update(keyEnter); cmd != nil || !strings.Contains(m.msg, "No edit file") {
		t.Fatalf("msg %q", m.msg)
	}
	os.WriteFile(filepath.Join(dir, "2026-10-04_edit.wav"), []byte("x"), 0o644)
	m, _ = m.update(keys("r"))
	if !strings.Contains(m.view(30), "✓ Found 2026-10-04_edit.wav") {
		t.Errorf("after rescan:\n%s", m.view(30))
	}
	_, cmd := m.update(keyEnter)
	if done := run[cutDoneMsg](t, cmd); done.mode != modeEdit {
		t.Errorf("done %+v", done)
	}
	// With an edit file in the folder, cutting by times would conflict.
	m, _ = m.update(keys("1"))
	if m, cmd := m.update(keyEnter); cmd != nil || !strings.Contains(m.msg, "would conflict") {
		t.Errorf("msg %q", m.msg)
	}
}

func TestCutDefaultsToEditWithoutRaw(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_edit.flac")
	m := newTestCut(t, dir, nil)
	if m.mode != modeEdit {
		t.Errorf("mode %v, want edit", m.mode)
	}
	m, _ = m.update(keys("1"))
	if m, cmd := m.update(keyEnter); cmd != nil || m.msg == "" {
		t.Error("timeline without raw must not continue")
	}
}

func TestOptions(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_raw.wav", "2026-10-04.mp3")
	paths, _ := normalizer.NewPaths(dir, "")
	m := newOptions(paths, cutDoneMsg{mode: modeTimeline, start: 2480, end: 4325}, normalizer.DefaultOptions(), "")
	v := strings.ReplaceAll(m.view(200), "\n", " ")
	for _, want := range []string{"from 41:20 to 1:12:05", "[x]", "Already there, skipped unless Force: 2026-10-04.mp3",
		"Same as: sermon-normalizer run " + dir + " --start 0:41:20 --end 1:12:05"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	m, _ = m.update(keySpace) // FLAC off
	m.get("fadein").input.SetValue("1.5")
	m.get("archive").input.SetValue("arkiv")
	_, cmd := m.update(keyEnter)
	got := run[startRunMsg](t, cmd)
	if got.opt.Outputs != (normalizer.Outputs{Mp3: true, Opus: true}) || got.opt.FadeIn != 1.5 ||
		got.archive != "arkiv" || got.opt.Start != 2480 || got.opt.End != 4325 {
		t.Errorf("got %+v", got)
	}
	// No outputs, or a bad number, is an error.
	m, _ = m.update(keyDown)
	m, _ = m.update(keySpace)
	m, _ = m.update(keyDown)
	m, _ = m.update(keySpace)
	if m, cmd := m.update(keyEnter); cmd != nil || !strings.Contains(m.msg, "at least one output") {
		t.Errorf("msg %q", m.msg)
	}
	m.get("mp3").on = true
	m.get("noise").input.SetValue("loud")
	if m, cmd := m.update(keyEnter); cmd != nil || !strings.Contains(m.msg, "Noise reduction") {
		t.Errorf("msg %q", m.msg)
	}
}

func TestOptionsEditModeIgnoresTimes(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_edit.flac")
	paths, _ := normalizer.NewPaths(dir, "")
	m := newOptions(paths, cutDoneMsg{mode: modeEdit, start: 5, end: 9}, normalizer.DefaultOptions(), "")
	opt, _, err := m.build()
	if err != nil || opt.Start != 0 || opt.End != 0 {
		t.Errorf("opt %+v err %v", opt, err)
	}
}

func TestCommandLine(t *testing.T) {
	opt := normalizer.DefaultOptions()
	opt.Start, opt.FadeOut, opt.Force = 61.5, 2, true
	opt.Outputs = normalizer.Outputs{Mp3: true}
	got := commandLine("/data/Taler/2026 10", opt, "/mnt/arkiv")
	want := "sermon-normalizer run '/data/Taler/2026 10' --start 0:01:01.500 --formats mp3 --fade-out 2 --archive /mnt/arkiv --force"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRunView(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04.mp3")
	paths, _ := normalizer.NewPaths(dir, "")
	m := runModel{paths: paths, opt: normalizer.Options{Outputs: normalizer.Outputs{Mp3: true}}, events: make(chan tea.Msg, 1)}
	for _, msg := range []tea.Msg{stepMsg("1. Raw recording"), logMsg("== 1. Raw recording"), logMsg("Using x"), stepMsg("3. Master"), progressMsg(0.5)} {
		m, _ = m.update(msg)
	}
	v := m.view(30)
	if !strings.Contains(v, "✓ 1. Raw recording") || !strings.Contains(v, "Using x") || strings.Contains(v, "== 1.") {
		t.Errorf("running view:\n%s", v)
	}
	m, _ = m.update(runDoneMsg{})
	if v := m.view(30); !strings.Contains(v, "2026-10-04 done") || !strings.Contains(v, "2026-10-04.mp3") {
		t.Errorf("done view:\n%s", v)
	}
	m, _ = m.update(runDoneMsg{err: errors.New("boom")})
	if v := m.view(30); !strings.Contains(v, "failed") || !strings.Contains(v, "boom") {
		t.Errorf("failed view:\n%s", v)
	}
}

// ctrl+c during a run cancels it and quits only when the pipeline is done.
func TestQuitWaitsForRun(t *testing.T) {
	cancelled := false
	m := model{player: &player{}, screen: screenRun,
		run: runModel{cancel: func() { cancelled = true }, events: make(chan tea.Msg, 1)}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || !cancelled {
		t.Fatalf("cmd %v cancelled %v", cmd, cancelled)
	}
	if v := next.(model).View(); !strings.Contains(v, "Cancelling") {
		t.Errorf("view:\n%s", v)
	}
	_, cmd = next.Update(runDoneMsg{err: context.Canceled})
	run[tea.QuitMsg](t, cmd) // quits once the run has reported back
}

func TestOpenDateFolderDirectly(t *testing.T) {
	root := t.TempDir()
	dir := dateDir(t, root, "2026-10-04", "2026-10-04_edit.flac")
	m, err := newModel(context.Background(), Config{Dir: dir, Options: normalizer.DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if m.screen != screenCut || m.cut.paths.Folder != dir {
		t.Fatalf("screen %v folder %s", m.screen, m.cut.paths.Folder)
	}
	// esc goes back to the folder list of its parent, on that folder.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next, _ = next.Update(run[backMsg](t, cmd))
	b := next.(model).browser
	if next.(model).screen != screenBrowser || b.dir != root || b.entries[b.cursor].path != dir {
		t.Errorf("browser %s cursor %d", b.dir, b.cursor)
	}
}

// Keys typed faster than a redraw arrive together and must still each count.
func TestBatchedKeys(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_raw.wav")
	m, err := newModel(context.Background(), Config{Dir: dir, Options: normalizer.DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	m.cut, _ = m.cut.update(levelsMsg{path: m.cut.src.Raw, levels: testLevels()})
	next, _ := m.Update(keys("]]s]e"))
	c := next.(model).cut
	if !near(c.start, 21) || !near(c.end, 79) {
		t.Errorf("start %.2f end %.2f", c.start, c.end)
	}
}

func TestWrap(t *testing.T) {
	got := wrap(lipgloss.NewStyle(), "Same as: sermon-normalizer run /x --formats mp3,opus --fade-in 1", 30)
	want := "Same as: sermon-normalizer\nrun /x --formats mp3,opus\n--fade-in 1"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Without any mark there is nothing to cut; the pipeline would look for an edit file.
func TestCutNeedsAMark(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_raw.wav")
	m := newTestCut(t, dir, testLevels())
	if m, cmd := m.update(keyEnter); cmd != nil || !strings.Contains(m.msg, "Mark where") {
		t.Errorf("timeline: msg %q", m.msg)
	}
	m.setMode(modeTimes)
	if m, cmd := m.update(keyEnter); cmd != nil || !strings.Contains(m.msg, "start and/or an end") {
		t.Errorf("times: msg %q", m.msg)
	}
}

func TestCancelledRunView(t *testing.T) {
	paths, _ := normalizer.NewPaths(t.TempDir(), "")
	m := runModel{paths: paths, events: make(chan tea.Msg, 1)}
	m, _ = m.update(runDoneMsg{err: context.Canceled})
	if v := m.view(30); !strings.Contains(v, "cancelled") || strings.Contains(v, "failed") {
		t.Errorf("view:\n%s", v)
	}
}

// Options carry over to the next folder, except the cut and Force.
func TestOptionsRemembered(t *testing.T) {
	dir := dateDir(t, t.TempDir(), "2026-10-04", "2026-10-04_edit.flac")
	m, _ := newModel(context.Background(), Config{Dir: dir, Options: normalizer.DefaultOptions()})
	opt := normalizer.DefaultOptions()
	opt.Outputs, opt.FadeIn, opt.Force, opt.Start = normalizer.Outputs{Mp3: true}, 2, true, 30
	next, _ := m.Update(startRunMsg{opt: opt, archive: "arkiv"})
	nm := next.(model)
	nm.run.stop()
	<-nm.run.finished
	got := nm.cfg.Options
	if got.Outputs != opt.Outputs || got.FadeIn != 2 || got.Force || got.Start != 0 || nm.archive != "arkiv" {
		t.Errorf("remembered %+v archive %q", got, nm.archive)
	}
}

// A clip that ends by itself no longer counts as playing, so the next space
// plays again instead of stopping a process that has already exited.
func TestPlayerClearsFinishedClip(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	p := &player{bin: bin}
	cmd, err := p.play("x.flac", 0, 5)
	if err != nil || cmd == nil || !p.playing() {
		t.Fatalf("cmd %v err %v playing %v", cmd, err, p.playing())
	}
	done := run[playDoneMsg](t, cmd) // waits for the process to exit
	p.done(done)
	if p.playing() {
		t.Error("still playing after the clip ended")
	}
	// A late message from an earlier clip must not clear the current one.
	p.play("x.flac", 0, 5)
	p.done(done)
	if !p.playing() {
		t.Error("an old clip's message cleared the new one")
	}
	p.stop()
}
