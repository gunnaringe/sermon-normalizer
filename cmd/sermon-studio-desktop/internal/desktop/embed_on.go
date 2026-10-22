//go:build embedffmpeg

package desktop

import _ "embed"

// The ffmpeg built into the program (gzip) and its licences, put in ffmpeg/ by scripts/fetch-ffmpeg.sh.
var (
	//go:embed ffmpeg/ffmpeg.gz
	embeddedFFmpeg []byte
	//go:embed ffmpeg/LICENSES.txt
	ffmpegLicenses string
)
