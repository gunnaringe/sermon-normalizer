package normalizer

import (
	"errors"

	"golang.org/x/sys/windows"
)

// inUse reports whether Windows refused because another program has the file open. Access denied is in it
// too: it is what a file being deleted or scanned gives, and a folder with a file open in it.
func inUse(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
