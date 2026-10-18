package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

type fieldKind int

const (
	fieldToggle fieldKind = iota
	fieldText
)

type field struct {
	key   string
	group string
	label string
	hint  string
	kind  fieldKind
	on    bool
	input textinput.Model
}

type optionsModel struct {
	paths  normalizer.Paths
	cut    cutDoneMsg
	fields []field
	focus  int
	msg    string
}

// startRunMsg starts the pipeline.
type startRunMsg struct {
	opt     normalizer.Options
	archive string
}

// backToCutMsg returns to the cut screen.
type backToCutMsg struct{}

func newOptions(paths normalizer.Paths, cut cutDoneMsg, def normalizer.Options, archive string) optionsModel {
	text := func(key, group, label, value, hint string) field {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
		ti.Width = 30
		ti.SetValue(value)
		return field{key: key, group: group, label: label, hint: hint, kind: fieldText, input: ti}
	}
	toggle := func(key, group, label string, on bool, hint string) field {
		return field{key: key, group: group, label: label, hint: hint, kind: fieldToggle, on: on}
	}
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	m := optionsModel{paths: paths, cut: cut, fields: []field{
		toggle("flac", "Output", "FLAC master", def.Outputs.Flac, "48 kHz / 24-bit"),
		toggle("mp3", "Output", "MP3", def.Outputs.Mp3, ""),
		toggle("opus", "Output", "Opus", def.Outputs.Opus, ""),
		toggle("stereo", "Sound", "Stereo", def.Stereo, "off = mono, usual for speech"),
		text("fadein", "Sound", "Fade in (s)", num(def.FadeIn), "0 = off"),
		text("fadeout", "Sound", "Fade out (s)", num(def.FadeOut), "0 = off"),
		text("noise", "Sound", "Noise reduction (dB)", strconv.Itoa(def.NoiseReduction), "0 = off"),
		text("lufs", "Sound", "Target loudness (LUFS)", num(def.TargetLUFS), "-19 mono, -16 stereo"),
		text("peak", "Sound", "Max true peak (dBTP)", num(def.TruePeak), ""),
		text("mp3br", "Sound", "MP3 bitrate", def.Mp3Bitrate, ""),
		text("opusbr", "Sound", "Opus bitrate", def.OpusBitrate, ""),
		text("archive", "Files", "Archive folder", archive, "empty = leave sources in place; relative to the date folder"),
		toggle("keepwav", "Files", "Keep WAV", def.KeepWav, "with an archive: keep the WAV after converting"),
		toggle("force", "Files", "Force", def.Force, "redo files that already exist"),
	}}
	m.focusField(0)
	return m
}

func (m *optionsModel) focusField(i int) {
	if f := &m.fields[m.focus]; f.kind == fieldText {
		f.input.Blur()
	}
	m.focus = i
	if f := &m.fields[m.focus]; f.kind == fieldText {
		f.input.Focus()
		f.input.CursorEnd()
	}
}

func (m optionsModel) get(key string) *field {
	for i := range m.fields {
		if m.fields[i].key == key {
			return &m.fields[i]
		}
	}
	panic("no field " + key)
}

// build turns the form into options for the pipeline.
func (m optionsModel) build() (normalizer.Options, string, error) {
	opt := normalizer.DefaultOptions()
	opt.Start, opt.End = 0, 0
	if m.cut.mode != modeEdit {
		opt.Start, opt.End = m.cut.start, m.cut.end
	}
	opt.Stereo = m.get("stereo").on
	opt.Outputs = normalizer.Outputs{Flac: m.get("flac").on, Mp3: m.get("mp3").on, Opus: m.get("opus").on}
	if opt.Outputs == (normalizer.Outputs{}) {
		return opt, "", fmt.Errorf("choose at least one output")
	}
	floats := []struct {
		key string
		dst *float64
		min float64
	}{{"fadein", &opt.FadeIn, 0}, {"fadeout", &opt.FadeOut, 0}, {"lufs", &opt.TargetLUFS, -70}, {"peak", &opt.TruePeak, -20}}
	for _, f := range floats {
		fd := m.get(f.key)
		v, err := strconv.ParseFloat(strings.TrimSpace(fd.input.Value()), 64)
		if err != nil || v < f.min {
			return opt, "", fmt.Errorf("%s: not a valid number", fd.label)
		}
		*f.dst = v
	}
	nr, err := strconv.Atoi(strings.TrimSpace(m.get("noise").input.Value()))
	if err != nil || nr < 0 || nr > 97 {
		return opt, "", fmt.Errorf("Noise reduction: a whole number from 0 to 97")
	}
	opt.NoiseReduction = nr
	opt.Mp3Bitrate = strings.TrimSpace(m.get("mp3br").input.Value())
	opt.OpusBitrate = strings.TrimSpace(m.get("opusbr").input.Value())
	opt.KeepWav = m.get("keepwav").on
	opt.Force = m.get("force").on
	return opt, strings.TrimSpace(m.get("archive").input.Value()), nil
}

func (m optionsModel) update(msg tea.KeyMsg) (optionsModel, tea.Cmd) {
	m.msg = ""
	f := &m.fields[m.focus]
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return backToCutMsg{} }
	case "up", "shift+tab":
		m.focusField((m.focus + len(m.fields) - 1) % len(m.fields))
		return m, nil
	case "down", "tab":
		m.focusField((m.focus + 1) % len(m.fields))
		return m, nil
	case "enter":
		opt, archive, err := m.build()
		if err != nil {
			m.msg = err.Error()
			return m, nil
		}
		return m, func() tea.Msg { return startRunMsg{opt, archive} }
	case " ", "x":
		if f.kind == fieldToggle {
			f.on = !f.on
			if f.key == "stereo" {
				m.followChannels(!f.on, f.on)
			}
			return m, nil
		}
	}
	if f.kind == fieldText {
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// followChannels moves the loudness target and bitrates to the new channel
// defaults, unless they were changed from the old ones.
func (m optionsModel) followChannels(oldStereo, stereo bool) {
	old, def := normalizer.DefaultsFor(oldStereo), normalizer.DefaultsFor(stereo)
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for _, f := range []struct{ key, old, def string }{
		{"lufs", num(old.TargetLUFS), num(def.TargetLUFS)},
		{"mp3br", old.Mp3Bitrate, def.Mp3Bitrate},
		{"opusbr", old.OpusBitrate, def.OpusBitrate},
	} {
		if in := &m.get(f.key).input; strings.TrimSpace(in.Value()) == f.old {
			in.SetValue(f.def)
		}
	}
}

func (m optionsModel) view(width int) string {
	var s strings.Builder
	s.WriteString(titleStyle.Render(m.paths.Date) + subtleStyle.Render("  options") + "\n\n")
	s.WriteString("Cut: " + m.cutText() + "\n")
	group := ""
	for i, f := range m.fields {
		if f.group != group {
			group = f.group
			s.WriteString("\n" + subtleStyle.Render(group) + "\n")
		}
		marker := "  "
		label := f.label
		if i == m.focus {
			marker = cursorStyle.Render("› ")
			label = cursorStyle.Render(label)
		}
		var value string
		if f.kind == fieldToggle {
			value = "[ ]"
			if f.on {
				value = selectedStyle.Render("[x]")
			}
		} else {
			value = f.input.View()
		}
		pad := strings.Repeat(" ", max(24-len(f.label), 1))
		hint := ""
		if f.hint != "" {
			hint = "  " + subtleStyle.Render(f.hint)
		}
		s.WriteString(marker + label + pad + value + hint + "\n")
	}
	opt, archive, err := m.build()
	if err == nil {
		for _, n := range m.notices(opt, archive) {
			s.WriteString("\n" + n)
		}
		s.WriteString("\n\n" + wrap(subtleStyle, "Same as: "+commandLine(m.paths.Folder, opt, archive), width) + "\n")
	}
	if m.msg != "" {
		s.WriteString("\n" + errorStyle.Render(m.msg) + "\n")
	} else if err != nil {
		s.WriteString("\n" + errorStyle.Render(err.Error()) + "\n")
	}
	s.WriteString("\n" + helpStyle.Render("↑/↓ move · space toggle · enter run · esc back") + "\n")
	return s.String()
}

func (m optionsModel) cutText() string {
	switch m.cut.mode {
	case modeEdit:
		return "edit file " + m.paths.Date + "_edit"
	default:
		from, to := "the beginning", "the end"
		if m.cut.start > 0 {
			from = clock(m.cut.start)
		}
		if m.cut.end > 0 {
			to = clock(m.cut.end)
		}
		return "from " + from + " to " + to
	}
}

// notices warns about what the run will skip.
func (m optionsModel) notices(opt normalizer.Options, archive string) []string {
	if opt.Force {
		return nil
	}
	var out []string
	var existing []string
	for _, o := range []struct {
		on   bool
		path string
	}{{opt.Outputs.Flac, m.paths.Master}, {opt.Outputs.Mp3, m.paths.Mp3}, {opt.Outputs.Opus, m.paths.Opus}} {
		if _, err := os.Stat(o.path); o.on && err == nil {
			existing = append(existing, filepath.Base(o.path))
		}
	}
	if len(existing) > 0 {
		out = append(out, errorStyle.Render("Already there, skipped unless Force: "+strings.Join(existing, ", ")))
	}
	if m.cut.mode != modeEdit && archive != "" {
		if paths, err := normalizer.NewPaths(m.paths.Folder, archive); err == nil {
			if _, err := os.Stat(paths.EditArchive()); err == nil {
				out = append(out, errorStyle.Render("An earlier cut is in the archive; it is used instead of these times unless Force."))
			}
		}
	}
	return out
}

// commandLine is the CLI command for the same run (non-default flags only).
func commandLine(folder string, opt normalizer.Options, archive string) string {
	def := normalizer.DefaultOptions()
	def.SetStereo(opt.Stereo) // compare with the defaults for the chosen channels
	args := []string{"sermon-normalizer", "run", shellQuote(folder)}
	add := func(flag, value string) { args = append(args, flag, shellQuote(value)) }
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	if opt.Start > 0 {
		add("--start", normalizer.FormatTime(opt.Start))
	}
	if opt.End > 0 {
		add("--end", normalizer.FormatTime(opt.End))
	}
	if opt.Outputs != def.Outputs {
		add("--formats", opt.Outputs.String())
	}
	if opt.Stereo {
		add("--channels", "stereo")
	}
	if opt.FadeIn > 0 {
		add("--fade-in", num(opt.FadeIn))
	}
	if opt.FadeOut > 0 {
		add("--fade-out", num(opt.FadeOut))
	}
	if opt.NoiseReduction != def.NoiseReduction {
		add("--noise-reduction", strconv.Itoa(opt.NoiseReduction))
	}
	if opt.TargetLUFS != def.TargetLUFS {
		add("--target-lufs", num(opt.TargetLUFS))
	}
	if opt.TruePeak != def.TruePeak {
		add("--true-peak", num(opt.TruePeak))
	}
	if opt.Mp3Bitrate != def.Mp3Bitrate {
		add("--mp3-bitrate", opt.Mp3Bitrate)
	}
	if opt.OpusBitrate != def.OpusBitrate {
		add("--opus-bitrate", opt.OpusBitrate)
	}
	if archive != "" {
		add("--archive", archive)
	}
	if opt.KeepWav {
		args = append(args, "--keep-wav")
	}
	if opt.Force {
		args = append(args, "--force")
	}
	return strings.Join(args, " ")
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " '\"\\$`&|;<>()*?!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
