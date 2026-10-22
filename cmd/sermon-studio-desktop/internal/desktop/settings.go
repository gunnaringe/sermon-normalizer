// Package desktop is what the desktop program adds around the studio (web.App): its folders and settings, the
// window, one copy at a time, and stopping when no window is open. The studio itself knows nothing of it.
package desktop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Name is the program's name, also the name of its folders.
const Name = "Sermon Studio"

// DefaultPort is the port tried first. The port is kept once chosen: the browser keeps language, theme and
// open years per address.
const DefaultPort = 41917

// Settings are the settings of this machine, kept in the user's config folder, outside the data.
type Settings struct {
	Port    int    `json:"port"`
	DataDir string `json:"dataDir"`
	// Token lets a second start of the program know that the one on the port is this user's.
	Token string `json:"token"`

	path string
}

// LoadSettings reads the settings in dir (the program's config folder), with defaults for what is missing,
// and saves them when something was filled in.
func LoadSettings(dir string) (*Settings, error) {
	s := &Settings{path: filepath.Join(dir, "settings.json")}
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, s); err != nil {
			return nil, errors.New(s.path + ": " + err.Error())
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	changed := false
	if s.Port <= 0 || s.Port > 65535 {
		s.Port, changed = DefaultPort, true
	}
	if s.DataDir == "" {
		if s.DataDir, err = DefaultDataDir(); err != nil {
			return nil, err
		}
		changed = true
	}
	if s.Token == "" {
		b := make([]byte, 16)
		rand.Read(b)
		s.Token, changed = hex.EncodeToString(b), true
	}
	if changed {
		return s, s.Save()
	}
	return s, nil
}

// Save writes the settings, readable by this user only (the token is in them).
func (s *Settings) Save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
