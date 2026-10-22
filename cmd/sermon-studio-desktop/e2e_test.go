package main

// The desktop program from the outside, as a volunteer uses it, on whatever system the test runs on: it is
// built (or DESKTOP_BIN is used, a release build), started with folders of its own (never the user's), and
// then used through its address: the page, the Host check, a second start, an upload of a WAV, a sermon made
// as FLAC, MP3 and Opus, and stopping by itself. With DESKTOP_EMBEDDED set, it must use the ffmpeg built into
// it. Without ffmpeg in PATH (and nothing built in) it is skipped, or fails with FFMPEG_REQUIRED.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end to end (-short)")
	}
	embedded := os.Getenv("DESKTOP_EMBEDDED") != ""
	if _, err := exec.LookPath("ffmpeg"); err != nil && !embedded {
		if os.Getenv("FFMPEG_REQUIRED") != "" {
			t.Fatal("ffmpeg not in PATH")
		}
		t.Skip("ffmpeg not in PATH")
	}
	bin := os.Getenv("DESKTOP_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "sermon-studio-desktop"+exeSuffix())
		if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
	}

	// Folders of its own: the config and cache folders by every system's variables, the data by --data-dir.
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"), "APPDATA="+filepath.Join(home, "config"), "LOCALAPPDATA="+filepath.Join(home, "cache"))
	configDir, err := userDir(home, "config")
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "Sermon Studio")
	port := freePort(t)
	settings, _ := json.Marshal(map[string]any{"port": port, "dataDir": data, "token": "e2e-token"})
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/", port)
	start := func(args ...string) *exec.Cmd {
		cmd := exec.Command(bin, append([]string{"--no-window"}, args...)...)
		cmd.Env = env
		return cmd
	}

	first := start("--idle-after", "3s")
	var log bytes.Buffer
	first.Stderr = &log
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- first.Wait() }()
	t.Cleanup(func() {
		first.Process.Kill()
		if t.Failed() {
			t.Logf("the program's log:\n%s", log.String())
		}
	})
	waitFor(t, 20*time.Second, "the studio to answer", func() bool {
		resp, err := http.Get(base)
		if err == nil {
			resp.Body.Close()
		}
		return err == nil
	})

	// The page, with the script that keeps the program running while a window is open.
	if page := get(t, base); !strings.Contains(page, "/_desktop/alive.js") {
		t.Error("the page has no alive script")
	}
	// Only this computer's address: another name for it (DNS rebinding) is refused.
	if code := status(t, base, "evil.example:"+fmt.Sprint(port)); code != http.StatusMisdirectedRequest {
		t.Errorf("another Host: %d, want 421", code)
	}
	if !strings.Contains(get(t, base+"api/tenants"), `"name":"My church"`) {
		t.Error("the first church was not made")
	}
	// A second start finds this one and leaves.
	if out, err := start().CombinedOutput(); err != nil {
		t.Errorf("second start: %v\n%s", err, out)
	}

	// Upload a recording (a WAV, made here: no ffmpeg needed for it), then make the sermon.
	wav := sineWAV(40)
	resp, err := http.Post(fmt.Sprintf("%sapi/upload?date=2026-10-04&name=service.wav&size=%d&offset=0", base, len(wav)), "application/octet-stream", bytes.NewReader(wav))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	run := `{"sections":[{"start":5,"end":35}],"cuts":[],"flac":true,"mp3":true,"opus":true,"fadeIn":1,"fadeOut":1,
		"noiseReduction":6,"leveling":"normal","compression":"off","targetLufs":-19,"truePeak":-1.5,"mp3Bitrate":"96k","opusBitrate":"48k"}`
	var job struct{ ID, Error string }
	// The WAV is still being converted to FLAC for a moment after the upload: until then a run is refused.
	waitFor(t, 60*time.Second, "the run to start", func() bool {
		resp, err := http.Post(base+"api/run?path=2026/2026-10-04", "application/json", strings.NewReader(run))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(&job)
		return resp.StatusCode == http.StatusAccepted
	})
	if state := lastState(t, base+"api/jobs/"+job.ID+"/events"); state != "done" {
		t.Fatalf("the run ended %s", state)
	}
	folder := filepath.Join(data, "my-church", "Taler", "2026", "2026-10-04")
	for _, f := range []string{"2026-10-04.flac", "2026-10-04.mp3", "2026-10-04.opus", "original/service.flac"} {
		if st, err := os.Stat(filepath.Join(folder, filepath.FromSlash(f))); err != nil || st.Size() == 0 {
			t.Errorf("%s: %v", f, err)
		}
	}

	// No window, nothing running: it stops by itself.
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("stopped with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("still running 30 s after it became idle")
	}
	logged := readLog(t, home)
	for _, want := range []string{"already running", "converted", "no window open"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log has no %q", want)
		}
	}
	if embedded && !strings.Contains(logged, string(filepath.Separator)+"ffmpeg-") {
		t.Errorf("not the ffmpeg built in:\n%s", logged)
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// userDir is where the program's config or cache folder is under the variables set for it.
func userDir(home, kind string) (string, error) {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		dir = map[string]string{"config": "Library/Application Support", "cache": "Library/Caches"}[kind]
		dir = filepath.Join(home, filepath.FromSlash(dir))
	default:
		dir = filepath.Join(home, kind)
	}
	return filepath.Join(dir, "Sermon Studio"), nil
}

func readLog(t *testing.T, home string) string {
	t.Helper()
	cache, _ := userDir(home, "cache")
	b, err := os.ReadFile(filepath.Join(cache, "sermon-studio.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); !ok(); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func status(t *testing.T, url, host string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// lastState follows a run's event stream to its end and returns the last state.
func lastState(t *testing.T, url string) string {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var s struct {
				State string `json:"state"`
				Error string `json:"error"`
			}
			json.Unmarshal([]byte(data), &s)
			state = s.State
			if s.Error != "" {
				t.Log("run:", s.Error)
			}
		}
	}
	return state
}

// sineWAV is a mono 48 kHz 16-bit WAV of a quiet tone, seconds long.
func sineWAV(seconds int) []byte {
	const rate = 48000
	n := rate * seconds
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + 2*n))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(rate))
	w(uint32(2 * rate))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(2 * n))
	for i := range n {
		w(int16(3000 * math.Sin(2*math.Pi*220*float64(i)/rate)))
	}
	return b.Bytes()
}
