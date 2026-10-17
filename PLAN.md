# Plan: sermon-normalizer, processing of sermon recordings

## Goal

One command-line tool that takes a recording of a sermon (speech + music, uneven volume)
and produces finished files for publishing on SoundCloud, Spotify and other podcast services.
Must run on Linux (server), but must be testable on Windows.

## Previous flow (manual + `behandle-tale.ps1`)

1. The recorder produces a `.wav`. `sox` converts it to `.flac`.
2. Manual cutting in an audio editor (only the sermon is kept), temporarily normalised to make cutting easier.
3. `behandle-tale.ps1` makes the master (mono, -19 LUFS), MP3 and Opus.

## Folder layout per recording

```
<date>\                      e.g. 2026-10-04
  <date>.flac                master, mono, 48 kHz / 24-bit, -19 LUFS
  <date>.mp3                 mono, 96 kbps  (publishing)
  <date>.opus                mono, 48 kbps
  arkiv\                     only with --archive arkiv
    <date>_raw.flac          the whole recording, unedited
    <date>_edit.flac         cut version (input to the master)
```

Input placed in the folder: `<date>_raw.wav` (or `_raw.flac`), plus `<date>_edit.flac` unless `--start`/`--end` is used.
Without `--archive` the sources stay in the date folder untouched.

## Choice of language

**Recommendation: Go.** The audio work itself is done by `ffmpeg` whatever the language. The code is orchestration:
finding files, calling ffmpeg, interpreting measurements, moving/deleting files, reporting.

| Language | For | Against |
|---|---|---|
| **Go** | One static binary, `GOOS=linux go build` from Windows, no runtime or venv on the server, good `os/exec`, the standard library covers flags/regex/tests | A bit more code than Python |
| Python | Fastest to write | Needs Python + venv/packages on the server, harder to ship as one file |
| Bash | Least code for plain ffmpeg calls | Fragile for parsing, error handling and tests; not portable to Windows |
| Rust | Same deliverable as Go | More work for no gain here |

Go also fits since it was already chosen, and it is easy to extend later
(for example watching a folder or a web API).

## Architecture

Module `github.com/gunnaringe/sermon-normalizer`. The `normalizer` package uses the standard library
only; the TUI uses Bubble Tea, Bubbles and Lip Gloss.

- `cmd/sermon-normalizer/main.go`: CLI (subcommands, flags, Ctrl+C cancels cleanly)
- `tui/`: the interactive front end (folders, cut, options, run)
- `normalizer/levels.go`: volume per 100 ms and silence detection, for the TUI timeline
- `normalizer/outputs.go`: which outputs to make (`--formats`)
- `normalizer/ffmpeg.go`: thin wrapper around `ffmpeg` (`-nostdin`, captures stdout and stderr)
- `normalizer/measure.go`: measurement with `ebur128` and parsing of the summary (LUFS, true peak, LRA)
- `normalizer/pipeline.go`: the steps (raw, edit, master, lossy, report)
- `normalizer/timecode.go`: parsing and formatting of `--start`/`--end` times
- `*_test.go`: tests for parsing, file names and gain calculation

## CLI

```
sermon-normalizer tui [folder] [flags]   interactive: pick a folder, cut, set options, run
sermon-normalizer run <folder> [flags]   run the whole flow for a date folder
sermon-normalizer measure <file>...      measure LUFS / true peak / LRA
sermon-normalizer version

run flags:
  --start / --end          cut the edit from raw (one segment); otherwise supply _edit yourself
  --fade-in / --fade-out   seconds of fade at the start/end of the sermon (0 = off)
  --formats flac,mp3,opus  which outputs to make (default all)
  --archive <folder>       keep raw and edit as FLAC there (default: sources stay in place)
  --target-lufs  -19       -19 for mono, -16 for stereo
  --true-peak    -1.5
  --mp3-bitrate  96k
  --opus-bitrate 48k
  --noise-reduction 12     0 = off
  --keep-wav               do not delete the WAV after verification
  --force                  recreate the output files
```

## Important rules (learned the hard way)

- The WAV is deleted **only** after the audio in `raw.flac` is verified bit-identical (MD5 over the audio stream).
- MP3 and Opus are encoded, **measured and re-encoded with adjusted gain** if peak > -1 dBTP
  or the loudness is off. The gain is limited by the peak ceiling.
- Measure with `ebur128` (fast), not `loudnorm` (upsamples to 192 kHz and is slow).
- Call ffmpeg with an argument list, never through a shell (avoids quoting bugs like `-c:a` in PowerShell).
- Existing output files are skipped without `--force`.

## Phases

1. **Port the ps1 to Go:** `run` and `measure`, tests, builds for Windows and Linux.
2. **Test on real recordings:** 2026-09-20 and 2026-10-04 give the same result as the ps1.
3. **Run on Linux:** install ffmpeg (`apt install ffmpeg`), copy the binary, run it on a folder.
4. **Later (optional):**
   - `run --all <year folder>` to process all date folders without a master
   - mono/stereo detection with the right target LUFS
   - metadata (ID3 tags: title, date, church) on the MP3
   - watching a folder (Resilio Sync) and processing automatically

## Risks and open questions

- **Opus true peak** overshoots easily; handled by the measurement step, but consider setting the master peak to -2 dBTP.
- **Title in file names** is not decided (date only for now).
- **Noise reduction** (`afftdn`) has not been listened to; tune `--noise-reduction` by ear.
- Resilio Sync can create `.rsls` placeholders; the tool ignores them.
