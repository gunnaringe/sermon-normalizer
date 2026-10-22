package desktop

import (
	"io"
	"os"
	"path/filepath"
)

// maxLog is the size at which the log is started anew at the next start (the old one kept as .old).
const maxLog = 5 << 20

// OpenLog opens the log file in dir (the cache folder), sermon-studio.log. What is written also goes to
// stderr, when there is one.
func OpenLog(dir string) (io.Writer, func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "sermon-studio.log")
	if st, err := os.Stat(path); err == nil && st.Size() > maxLog {
		os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	// The file first: a program without a console has no stderr to write to.
	return io.MultiWriter(f, os.Stderr), func() { f.Close() }, nil
}
