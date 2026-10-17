package tui

import (
	"os/exec"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// player plays audio with ffplay, one clip at a time.
type player struct {
	bin string // "" = playback unavailable
	cmd *exec.Cmd
}

// findFFplay looks next to ffmpeg first, then in PATH.
func findFFplay(ff normalizer.FFmpeg) string {
	name := "ffplay"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if ff.Bin != "" {
		if p, err := exec.LookPath(filepath.Join(filepath.Dir(ff.Bin), name)); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("ffplay"); err == nil {
		return p
	}
	return ""
}

// playDoneMsg reports that a clip has finished or was stopped.
type playDoneMsg struct{ cmd *exec.Cmd }

func (p *player) available() bool { return p.bin != "" }

func (p *player) playing() bool { return p.cmd != nil }

// play plays path from start for dur seconds (0 = to the end), stopping
// whatever is playing. ffplay gets no stdin/stdout/stderr so it can never
// draw over the TUI. The returned command waits for ffplay to exit and
// reports it, so a clip that ends by itself no longer counts as playing.
func (p *player) play(path string, start, dur float64) (tea.Cmd, error) {
	p.stop()
	if !p.available() {
		return nil, nil
	}
	args := []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-ss", secs(start)}
	if dur > 0 {
		args = append(args, "-t", secs(dur))
	}
	cmd := exec.Command(p.bin, append(args, path)...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p.cmd = cmd
	return func() tea.Msg {
		cmd.Wait()
		return playDoneMsg{cmd}
	}, nil
}

// done handles playDoneMsg: only the clip still playing is cleared.
func (p *player) done(msg playDoneMsg) {
	if p.cmd == msg.cmd {
		p.cmd = nil
	}
}

func (p *player) stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.cmd = nil
}
