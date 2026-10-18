# Plan: sermon-normalizer, processing of sermon recordings

## Goal

One web service that takes a recording of a sermon (speech + music, uneven volume)
and produces finished files for publishing on SoundCloud, Spotify and other podcast services.
It runs on Linux (a server or the home cluster); the cutting happens in the browser.

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
  arkiv\                     only with the archive folder set to arkiv
    <date>_raw.flac          the whole recording, unedited
    <date>_edit.flac         cut version (input to the master)
```

Input placed in the folder: `<date>_raw.wav` (or `_raw.flac`), plus `<date>_edit.flac` if the sermon is cut by hand instead of in the editor.
Without an archive folder the sources stay in the date folder untouched.

## Choice of language

**Recommendation: Go.** The audio work itself is done by `ffmpeg` whatever the language. The code is orchestration:
finding files, calling ffmpeg, interpreting measurements, moving/deleting files, reporting.

| Language | For | Against |
|---|---|---|
| **Go** | One static binary, `GOOS=linux go build` from Windows, no runtime or venv on the server, good `os/exec`, the standard library covers flags/regex/tests | A bit more code than Python |
| Python | Fastest to write | Needs Python + venv/packages on the server, harder to ship as one file |
| Bash | Least code for plain ffmpeg calls | Fragile for parsing, error handling and tests; not portable to Windows |
| Rust | Same deliverable as Go | More work for no gain here |

Go also fits since it was already chosen, and its standard library has the web server too.

## Architecture

Module `github.com/gunnaringe/sermon-normalizer`, standard library only.

- `cmd/sermon-normalizer/`: the server (flags, startup, clean shutdown)
- `web/`: JSON API, jobs, waveform analysis, inbox, and the embedded front end
  (`web/static/`, plain JS with wavesurfer.js bundled)
- `normalizer/ffmpeg.go`: thin wrapper around `ffmpeg` (`-nostdin`, captures stdout and stderr)
- `normalizer/measure.go`: measurement with `ebur128` and parsing of the summary (LUFS, true peak, LRA)
- `normalizer/pipeline.go`: the steps (raw, edit, master, lossy, report)
- `normalizer/levels.go`: volume per window and silence detection, for the waveform
- `normalizer/channels.go`: the mono/one-sided checks behind the stereo warnings
- `normalizer/outputs.go`: which outputs to make
- `*_test.go`: unit and integration tests; `web/e2e/`: browser tests

Run options (from the web form): cut start/end, fades, outputs (FLAC, MP3, Opus), mono or
stereo (stereo defaults: -16 LUFS, 128k MP3, 96k Opus), archive folder, keep WAV, force,
noise reduction (12 dB), target loudness (-19 LUFS), max true peak (-1.5 dBTP),
MP3 bitrate (96k), Opus bitrate (48k).

## Important rules (learned the hard way)

- The WAV is deleted **only** after the audio in `raw.flac` is verified bit-identical (MD5 over the audio stream).
- MP3 and Opus are encoded, **measured and re-encoded with adjusted gain** if peak > -1 dBTP
  or the loudness is off. The gain is limited by the peak ceiling.
- Measure with `ebur128` (fast), not `loudnorm` (upsamples to 192 kHz and is slow).
- Call ffmpeg with an argument list, never through a shell (avoids quoting bugs like `-c:a` in PowerShell).
- Existing output files are skipped without Force.

## Phases

1. **Port the ps1 to Go** and build the web editor (done).
2. **Test on real recordings:** 2026-09-20 and 2026-10-04 give the same result as the ps1.
3. **Release and deploy:** container image, running on the home cluster behind Traefik and Pocket ID.
4. **Later (optional):**
   - metadata (ID3 tags: title, date, church) on the MP3
   - picking up new recordings from a synced folder (Resilio Sync) automatically

## Risks and open questions

- **Opus true peak** overshoots easily; handled by the measurement step, but consider setting the master peak to -2 dBTP.
- **Title in file names** is not decided (date only for now).
- **Noise reduction** (`afftdn`) has not been listened to; tune the noise reduction by ear.
- Resilio Sync can create `.rsls` placeholders; the tool ignores them.
