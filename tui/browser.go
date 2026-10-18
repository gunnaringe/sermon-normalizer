package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// entry is one row in the folder list.
type entry struct {
	name    string
	path    string
	date    bool   // a date folder: has sources or outputs
	status  string // e.g. "raw · edit · flac mp3 opus"
	pending bool   // has sources but not all outputs
}

type browserModel struct {
	dir     string
	archive string
	entries []entry
	cursor  int
	err     error
}

// openFolderMsg asks to open a date folder on the cut screen.
type openFolderMsg struct{ path string }

func newBrowser(dir, archive string) browserModel {
	b := browserModel{dir: dir, archive: archive}
	b.refresh()
	return b
}

// folderStatus describes a folder; date reports whether it is a date folder.
func folderStatus(dir, archive string) (status string, date, pending bool) {
	paths, err := normalizer.NewPaths(dir, archive)
	if err != nil {
		return "", false, false
	}
	src := paths.Sources()
	var parts, outs []string
	if src.Raw != "" {
		parts = append(parts, "raw")
	}
	if src.Edit != "" {
		parts = append(parts, "edit")
	}
	for _, o := range []struct{ name, path string }{{"flac", paths.Master}, {"mp3", paths.Mp3}, {"opus", paths.Opus}} {
		if _, err := os.Stat(o.path); err == nil {
			outs = append(outs, o.name)
		}
	}
	if len(outs) > 0 {
		parts = append(parts, "✓ "+strings.Join(outs, " "))
	}
	hasSource := src.Raw != "" || src.Edit != ""
	return strings.Join(parts, " · "), len(parts) > 0, hasSource && len(outs) < 3
}

func (b *browserModel) refresh() {
	b.err = nil
	b.entries = nil
	des, err := os.ReadDir(b.dir)
	if err != nil {
		b.err = err
		return
	}
	if parent := filepath.Dir(b.dir); parent != b.dir {
		b.entries = append(b.entries, entry{name: "..", path: parent})
	}
	var dirs []entry
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		path := filepath.Join(b.dir, de.Name())
		status, date, pending := folderStatus(path, b.archive)
		dirs = append(dirs, entry{name: de.Name(), path: path, date: date, status: status, pending: pending})
	}
	// Date folders first, then other folders (e.g. years); newest (highest
	// name) first in both, as dates are named YYYY-MM-DD.
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].date != dirs[j].date {
			return dirs[i].date
		}
		return dirs[i].name > dirs[j].name
	})
	b.entries = append(b.entries, dirs...)
	if b.cursor >= len(b.entries) {
		b.cursor = max(len(b.entries)-1, 0)
	}
}

func (b *browserModel) enter(path string) {
	b.dir = path
	b.cursor = 0
	b.refresh()
}

func (b browserModel) update(msg tea.KeyMsg) (browserModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if b.cursor > 0 {
			b.cursor--
		}
	case "down", "j":
		if b.cursor < len(b.entries)-1 {
			b.cursor++
		}
	case "home", "g":
		b.cursor = 0
	case "end", "G":
		b.cursor = max(len(b.entries)-1, 0)
	case "backspace", "left", "h":
		if parent := filepath.Dir(b.dir); parent != b.dir {
			child := b.dir
			b.enter(parent)
			for i, e := range b.entries {
				if e.path == child {
					b.cursor = i
				}
			}
		}
	case "r":
		b.refresh()
	case "enter", "right", "l":
		if len(b.entries) == 0 {
			return b, nil
		}
		e := b.entries[b.cursor]
		if e.date {
			path := e.path
			return b, func() tea.Msg { return openFolderMsg{path} }
		}
		b.enter(e.path)
	}
	return b, nil
}

func (b browserModel) view(width, height int) string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("sermon-normalizer-cli") + subtleStyle.Render("  "+b.dir) + "\n\n")
	if b.err != nil {
		s.WriteString(errorStyle.Render(b.err.Error()) + "\n")
	}
	rows := max(height-6, 3)
	first := 0
	if b.cursor >= rows {
		first = b.cursor - rows + 1
	}
	nameWidth := 12
	for _, e := range b.entries {
		nameWidth = max(nameWidth, len(e.name)+1)
	}
	for i := first; i < len(b.entries) && i < first+rows; i++ {
		e := b.entries[i]
		name := e.name
		if !e.date {
			name += "/"
		}
		line := fmt.Sprintf("%-*s %s", nameWidth, name, e.status)
		switch {
		case i == b.cursor:
			s.WriteString(cursorStyle.Render("› " + line))
		case e.pending:
			s.WriteString("  " + line)
		case e.date:
			s.WriteString("  " + subtleStyle.Render(line))
		default:
			s.WriteString("  " + line)
		}
		s.WriteString("\n")
	}
	if len(b.entries) == 0 {
		s.WriteString(subtleStyle.Render("  (empty)") + "\n")
	}
	s.WriteString("\n" + helpStyle.Render("↑/↓ move · enter open · ← up · r refresh · q quit"))
	return s.String()
}
