# sermon-normalizer

CLI that turns a recording of a church service into publish-ready sermon files:
it cuts out the sermon, mixes it to mono, evens out the volume (speech and music are
recorded at very different levels), normalises to podcast loudness and encodes
FLAC, MP3 and Opus for SoundCloud, Spotify and other podcast services.
See `PLAN.md` for background and design decisions.

## Requirements

- `ffmpeg` in PATH (Linux: `sudo apt install ffmpeg`, Windows: `winget install Gyan.FFmpeg`)
- Go 1.22+ to build (no external Go dependencies)

## Build

```bash
go build -o sermon-normalizer ./cmd/sermon-normalizer                                   # for this machine
GOOS=linux GOARCH=amd64 go build -o dist/sermon-normalizer-linux-amd64 ./cmd/sermon-normalizer   # for Linux x86-64
GOOS=linux GOARCH=arm64 go build -o dist/sermon-normalizer-linux-arm64 ./cmd/sermon-normalizer   # for Linux ARM
go test ./...
```

PowerShell: set `$env:GOOS="linux"; $env:GOARCH="amd64"` before `go build`.

`go test ./...` runs the unit tests everywhere, and the integration tests (the whole pipeline
with real ffmpeg on synthetic recordings) when `ffmpeg` and `ffprobe` are in PATH.
`docker build .` always runs both. `go test -short ./...` skips the integration tests.

## Docker (ffmpeg included)

ffmpeg is a separate C program and cannot be built into the Go binary, so the container packages both.

```bash
docker build -t sermon-normalizer .
docker run --rm -u "$(id -u):$(id -g)" -v /path/to/Taler:/data sermon-normalizer run /data/2026/2026-10-04
```

`-u` makes the created files owned by your user, not root.
The build runs `go vet` and `go test`, and checks that ffmpeg has libopus and libmp3lame.

## Usage

Put the recording of the whole service in the date folder, and give where the sermon starts and ends:

```
2026-10-04/
  2026-10-04_raw.wav          (or _raw.flac)    the whole recording
```

```bash
sermon-normalizer run /path/to/Taler/2026/2026-10-04 --start 41:20 --end 1:12:05
```

Result:

```
2026-10-04/
  2026-10-04_raw.wav   untouched
  2026-10-04.flac      master: mono, 48 kHz / 24-bit, -19 LUFS
  2026-10-04.mp3       mono, 96 kbps
  2026-10-04.opus      mono, 48 kbps
```

Times can be written as `h:mm:ss`, `mm:ss` or seconds, with decimals if needed (`41:20.5`).
Without `--start` the cut starts at the beginning, without `--end` it runs to the end.
To cut again with different times, run again with `--force`.

**More than one segment** (for example a song in the middle of the sermon that should go):
cut it yourself in an audio editor and put the result in the folder as `<date>_edit.flac`
(or `.wav`). It is then used instead, and `--start`/`--end` is left out (giving both is an error).

### Archive

With `--archive <folder>` the sources are kept as FLAC in an archive folder, and the date folder
ends up with just the finished files. A relative path is relative to the date folder; an absolute
path (e.g. on another disk) is used as is, and can be shared by all dates.

```bash
sermon-normalizer run /path/to/Taler/2026/2026-10-04 --start 41:20 --end 1:12:05 --archive arkiv
```

```
2026-10-04/
  2026-10-04.flac
  2026-10-04.mp3
  2026-10-04.opus
  arkiv/
    2026-10-04_raw.flac       the whole recording
    2026-10-04_edit.flac      the sermon as cut, the input to the master
```

- A WAV is converted to FLAC in the archive, the audio is verified with MD5, and **the WAV is deleted only if the audio is identical** (unless `--keep-wav`). A FLAC is moved.
- A file already in the archive is never replaced without `--force`.
- Folders processed by older versions (raw already in `arkiv/`) need `--archive arkiv`.

### Notes

- MP3 and Opus are encoded, measured and re-encoded with adjusted gain if true peak > -1 dBTP or the loudness is off.
- Existing output files are skipped without `--force`. Output is written to a `.partial` file and
  renamed when done, so an interrupted run never leaves a half-written file behind.
- Mono and stereo recordings both work; everything is mixed down to mono first.
- The old names `_1-raw.*` and `_2-edit.*` are accepted.

Flags for `run` (may go before or after the folder):

| Flag | Default | |
|---|---|---|
| `--start` | | where the sermon starts in raw (cuts the edit automatically) |
| `--end` | | where the sermon ends in raw |
| `--archive` | | keep the sources as FLAC in this folder (default: leave them in place) |
| `--target-lufs` | -19 | -19 for mono, -16 for stereo |
| `--true-peak` | -1.5 | max true peak for the master |
| `--mp3-bitrate` | 96k | |
| `--opus-bitrate` | 48k | |
| `--noise-reduction` | 12 | dB, 0 = off |
| `--keep-wav` | off | do not delete the WAV |
| `--force` | off | recreate the output files |
| `--ffmpeg` | ffmpeg | path to ffmpeg |

Measuring files:

```bash
sermon-normalizer measure file1.mp3 file2.flac
```

`measure` also takes `--ffmpeg`.
