package desktop

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultDataDir is Documents\Sermon Studio. Documents is asked of Windows: it is often moved, into OneDrive
// among other places.
func DefaultDataDir() (string, error) {
	docs, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(docs, Name), nil
}
