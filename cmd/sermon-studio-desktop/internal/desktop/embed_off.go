//go:build !embedffmpeg

package desktop

// Without -tags embedffmpeg no ffmpeg is built in: it is looked for (FindFFmpeg).
var (
	embeddedFFmpeg []byte
	ffmpegLicenses string
)
