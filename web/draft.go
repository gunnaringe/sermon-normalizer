package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// draftFile is <folder>/draft.json: the sermon as it is being edited, not saved yet (the "adjusted" version).
// It is a file of its own, next to recording.json, because it changes all the time while someone edits, and
// recording.json should not: it is the one a sync tool must carry safely. Save and Revert remove it.
type draftFile struct {
	Sermon    *sermonSettings `json:"sermon"`
	UpdatedAt time.Time       `json:"updatedAt"`
	// Machine is where it was edited (the host name), for when several machines share the folder.
	Machine string `json:"machine,omitempty"`
}

const draftName = "draft.json"

// readDraft returns the folder's draft, or nil if there is none (or it is not valid).
func readDraft(folder string) *sermonSettings {
	data, err := os.ReadFile(filepath.Join(folder, draftName))
	if err != nil {
		return nil
	}
	var d draftFile
	if json.Unmarshal(data, &d) != nil {
		return nil
	}
	return validSermon(d.Sermon)
}

// writeDraft keeps c as the folder's draft; nil removes it.
func writeDraft(folder string, c *sermonSettings) error {
	path := filepath.Join(folder, draftName)
	if c == nil {
		if err := normalizer.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	host, _ := os.Hostname()
	data, _ := json.MarshalIndent(draftFile{Sermon: c, UpdatedAt: time.Now().UTC(), Machine: host}, "", "  ")
	return writeFileAtomic(path, append(data, '\n'))
}
