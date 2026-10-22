//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
	"strconv"
)

// ShowError shows an error that stops the program. Started by a double-click there is no terminal to see it
// in: a dialog on macOS, and with zenity on Linux when it is there.
func ShowError(msg string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("osascript", "-e", "display alert "+strconv.Quote(Name)+" message "+strconv.Quote(msg)+" as critical").Run()
	default:
		if p, err := exec.LookPath("zenity"); err == nil {
			exec.Command(p, "--error", "--no-markup", "--title="+Name, "--text="+msg).Run()
		}
	}
}
