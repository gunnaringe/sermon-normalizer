package desktop

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// FFmpeg is the ffmpeg to use: the one built into the program, unpacked into cacheDir, else one found next to
// the program, in PATH, or where Homebrew puts it on macOS (a program started from the Finder gets a short
// PATH).
func FFmpeg(cacheDir string) (string, error) {
	if len(embeddedFFmpeg) > 0 {
		return unpackFFmpeg(cacheDir, embeddedFFmpeg)
	}
	name := exeName("ffmpeg")
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	if p, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, p)
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg")
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	install := map[string]string{"windows": "winget install ffmpeg", "darwin": "brew install ffmpeg"}[runtime.GOOS]
	if install == "" {
		install = "apt install ffmpeg"
	}
	return "", errors.New("ffmpeg is missing: put ffmpeg next to Sermon Studio, or install it (" + install + ")")
}

// Licenses are the licences of the ffmpeg built in ("" when none is).
func Licenses() string { return ffmpegLicenses }

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// unpackFFmpeg writes the built-in ffmpeg (gz, gzip) to cacheDir/ffmpeg-<its hash>/, with the SHA-256 of what
// was written next to it. On every start the file is checked against that, and written anew when it is not
// right (half written, changed). Folders of other builds are removed.
func unpackFFmpeg(cacheDir string, gz []byte) (string, error) {
	sum := sha256.Sum256(gz)
	dir := filepath.Join(cacheDir, "ffmpeg-"+hex.EncodeToString(sum[:8]))
	bin := filepath.Join(dir, exeName("ffmpeg"))
	if want, err := os.ReadFile(bin + ".sha256"); err == nil {
		if got, err := fileSHA256(bin); err == nil && got == strings.TrimSpace(string(want)) {
			return bin, nil
		}
	}
	if old, _ := filepath.Glob(filepath.Join(cacheDir, "ffmpeg-*")); len(old) > 0 {
		for _, o := range old {
			if o != dir {
				os.RemoveAll(o)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return "", err
	}
	tmp := bin + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), zr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, bin)
	}
	if err != nil {
		os.Remove(tmp)
		return "", errors.Join(errors.New("unpacking ffmpeg"), err)
	}
	if err := os.WriteFile(bin+".sha256", []byte(hex.EncodeToString(h.Sum(nil))+"\n"), 0o644); err != nil {
		return "", err
	}
	if ffmpegLicenses != "" {
		os.WriteFile(filepath.Join(dir, "LICENSES.txt"), []byte(ffmpegLicenses), 0o644)
	}
	return bin, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
