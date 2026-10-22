package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenWindow opens the studio in a window of its own: Edge, Chrome or Chromium in app mode (no tabs or
// address bar), else the default browser.
func OpenWindow(url string) error {
	if b := appBrowser(); b != "" {
		return start(exec.Command(b, "--app="+url))
	}
	switch runtime.GOOS {
	case "windows":
		return start(exec.Command("rundll32", "url.dll,FileProtocolHandler", url))
	case "darwin":
		return start(exec.Command("open", url))
	default:
		return start(exec.Command("xdg-open", url))
	}
}

// start starts the browser and leaves it running: it may outlive this program.
func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// appBrowser is a browser with an app mode (--app=), or "". Edge is on every Windows.
func appBrowser() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			if dir := os.Getenv(env); dir != "" {
				candidates = append(candidates,
					filepath.Join(dir, "Microsoft", "Edge", "Application", "msedge.exe"),
					filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		for _, apps := range []string{"/Applications", filepath.Join(home, "Applications")} {
			candidates = append(candidates,
				filepath.Join(apps, "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
				filepath.Join(apps, "Chromium.app", "Contents", "MacOS", "Chromium"),
				filepath.Join(apps, "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"))
		}
	default:
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser",
			"microsoft-edge-stable", "microsoft-edge"} {
			if p, err := exec.LookPath(name); err == nil {
				return p
			}
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}
