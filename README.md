# sermon-normalizer

CLI that turns a recording of a church service into publish-ready sermon files:
it cuts out the sermon, mixes it to mono (or keeps stereo), evens out the volume (speech and music are
recorded at very different levels), normalises to podcast loudness and encodes
FLAC, MP3 and Opus for SoundCloud, Spotify and other podcast services.
See `PLAN.md` for background and design decisions.

## Requirements

- `ffmpeg` in PATH (Linux: `sudo apt install ffmpeg`, Windows: `winget install Gyan.FFmpeg`)
- Go 1.24+ to build
- Optional: `ffplay` (ships with ffmpeg on Windows and most Linux packages) for playback in the TUI

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
`scripts/tui-e2e.sh` drives the TUI in tmux with real ffmpeg (see the script for the command).

## Docker (ffmpeg included)

ffmpeg is a separate C program and cannot be built into the Go binary, so the container packages both.

```bash
docker build -t sermon-normalizer .
docker run --rm -u "$(id -u):$(id -g)" -v /path/to/Taler:/data sermon-normalizer run /data/2026/2026-10-04
```

`-u` makes the created files owned by your user, not root.
The build runs `go vet` and `go test`, and checks that ffmpeg has libopus and libmp3lame.

## TUI

```bash
sermon-normalizer tui /path/to/Taler/2026      # a folder of date folders
sermon-normalizer tui /path/to/Taler/2026/2026-10-04
```

1. **Folders:** date folders, newest first, with what they contain (`raw`, `edit`, `✓ flac mp3 opus`).
2. **Cut:** choose how to cut out the sermon (tab switches):
   - **Timeline:** the volume of the whole recording. Music is louder than speech, so the sermon
     stands out; `·` marks silences. Move with ←/→ (shift for 1 s, PgUp/PgDn for a minute), zoom
     with `+`/`-`, jump between silences with `[`/`]`, mark with `s` (start) and `e` (end).
     With `ffplay` available, `space` plays from the cursor and `S`/`E` play around the marks.
   - **Enter times:** type the start and end. Marks set on the timeline show up here and vice versa.
   - **Edit file:** cut in your own audio editor; the screen shows where to save `<date>_edit.flac`,
     and `r` looks for it again.
3. **Options:** which outputs to make (FLAC master, MP3, Opus; all by default), mono or stereo, fades and the other
   flags, with the equivalent `run` command. Your choices carry over to the next folder (except Force).
4. **Run:** progress per step, then the files made. Cancelling leaves nothing half-written.

`--archive <folder>` sets the default archive folder, `--ffmpeg` the ffmpeg to use (`ffplay` is looked
for next to it). In Docker the TUI works with `-it`, but without playback:

```bash
docker run -it --rm -u "$(id -u):$(id -g)" -v /path/to/Taler:/data sermon-normalizer tui /data/2026
```

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

Add `--fade-in 1 --fade-out 2` (seconds) to fade the sermon in and out instead of starting
and stopping abruptly at the cut points. The fades are applied last, after the loudness
processing, so they stay intact in the master, MP3 and Opus.

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

### Notes

- MP3 and Opus are encoded, measured and re-encoded with adjusted gain if true peak > -1 dBTP or the loudness is off.
- Existing output files are skipped without `--force`. Output is written to a `.partial` file and
  renamed when done, so an interrupted run never leaves a half-written file behind.
- `--formats mp3` makes only the MP3. MP3 and Opus are always made from the master; without
  `flac` it is a temporary hidden file in the date folder, removed when the run ends.
- Output is mono by default, the usual choice for speech. `--channels stereo` keeps two channels
  and moves the defaults to -16 LUFS, MP3 128k and Opus 96k; any of those given explicitly win.
  Mono and stereo recordings both work either way. With `stereo`, the tool warns if the sermon
  is mono or its two channels are identical or nearly so (difference 30 dB or more below the
  signal, common when a mixer sends one signal to both sides): stereo output would then sound
  the same as mono in bigger files. It only warns; the output is still stereo.

Flags for `run` (may go before or after the folder):

| Flag | Default | |
|---|---|---|
| `--start` | | where the sermon starts in raw (cuts the edit automatically) |
| `--end` | | where the sermon ends in raw |
| `--fade-in` | 0 | seconds of fade in at the start of the sermon, 0 = off |
| `--fade-out` | 0 | seconds of fade out at the end of the sermon, 0 = off |
| `--formats` | flac,mp3,opus | which outputs to make; any of `flac` (master), `mp3`, `opus` |
| `--archive` | | keep the sources as FLAC in this folder (default: leave them in place) |
| `--channels` | mono | `mono` or `stereo` |
| `--target-lufs` | -19 | -16 with `--channels stereo` |
| `--true-peak` | -1.5 | max true peak for the master |
| `--mp3-bitrate` | 96k | 128k with `--channels stereo` |
| `--opus-bitrate` | 48k | 96k with `--channels stereo` |
| `--noise-reduction` | 12 | dB, 0 = off |
| `--keep-wav` | off | do not delete the WAV |
| `--force` | off | recreate the output files |
| `--ffmpeg` | ffmpeg | path to ffmpeg |

Measuring files:

```bash
sermon-normalizer measure file1.mp3 file2.flac
```

`measure` also takes `--ffmpeg`.
