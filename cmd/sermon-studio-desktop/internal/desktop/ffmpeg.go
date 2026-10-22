package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FindFFmpeg looks for ffmpeg next to the program, then in PATH, then where Homebrew puts it on macOS (a
// program started from the Finder gets a short PATH).
func FindFFmpeg() (string, error) {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	if p, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, p)
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg")
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	install := map[string]string{"windows": "winget install ffmpeg", "darwin": "brew install ffmpeg"}[runtime.GOOS]
	if install == "" {
		install = "apt install ffmpeg"
	}
	return "", errors.New("ffmpeg is missing: put ffmpeg next to Sermon Studio, or install it (" + install + ")")
}
