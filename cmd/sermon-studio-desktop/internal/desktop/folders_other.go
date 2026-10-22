//go:build !windows

package desktop

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir is Documents/Sermon Studio on macOS and ~/Sermon Studio elsewhere.
func DefaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Documents", Name), nil
	}
	return filepath.Join(home, Name), nil
}
