package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FirstChurch makes a church in dataDir if it has none, so the studio has one to open: a church folder with
// its Taler folder and a church.json with the name (see web/app.go). It reports whether it made one.
func FirstChurch(dataDir string) (bool, error) {
	des, err := os.ReadDir(dataDir)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		for _, marker := range []string{"church.json", "Taler"} {
			if _, err := os.Stat(filepath.Join(dataDir, de.Name(), marker)); err == nil {
				return false, nil
			}
		}
	}
	dir := filepath.Join(dataDir, "my-church")
	if err := os.MkdirAll(filepath.Join(dir, "Taler"), 0o755); err != nil {
		return false, err
	}
	b, _ := json.MarshalIndent(map[string]string{"name": "My church"}, "", "  ")
	return true, os.WriteFile(filepath.Join(dir, "church.json"), append(b, '\n'), 0o644)
}
