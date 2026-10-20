package web

import (
	"strings"
	"testing"
)

func TestCleanSourceName(t *testing.T) {
	for in, want := range map[string]string{
		"R_20261018-104506.wav":      "R_20261018-104506.wav",
		`C:\Recorder\R_1.WAV`:        "R_1.WAV",
		"/tmp/../x/Preken 1 (a).mp3": "Preken 1 (a).mp3",
		"høst: prekenen?.flac":       "høst_ prekenen_.flac",
		".hidden.flac":               "hidden.flac",
		"x.partial.wav":              "x_partial.wav",
		"noextension":                "",
		".wav":                       "",
		"":                           "",
	} {
		if got := cleanSourceName(in); got != want {
			t.Errorf("cleanSourceName(%q) = %q, want %q", in, got, want)
		}
	}
	long := cleanSourceName(strings.Repeat("a", 300) + ".wav")
	if len([]rune(long)) != maxSourceName || !strings.HasSuffix(long, ".wav") {
		t.Errorf("long name: %d runes, %q", len([]rune(long)), long[len(long)-8:])
	}
}
