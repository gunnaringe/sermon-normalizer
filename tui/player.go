package tui

import (
	"os/exec"
	"path/filepath"
	"runtime"

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

func (p *player) available() bool { return p.bin != "" }

func (p *player) playing() bool { return p.cmd != nil }

// play plays path from start for dur seconds (0 = to the end), stopping
// whatever is playing. ffplay gets no stdin/stdout/stderr so it can never
// draw over the TUI.
func (p *player) play(path string, start, dur float64) error {
	p.stop()
	if !p.available() {
		return nil
	}
	args := []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-ss", secs(start)}
	if dur > 0 {
		args = append(args, "-t", secs(dur))
	}
	cmd := exec.Command(p.bin, append(args, path)...)
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	go cmd.Wait() // reap; stop() may kill it first
	return nil
}

func (p *player) stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.cmd = nil
}
