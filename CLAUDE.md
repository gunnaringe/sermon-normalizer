# sermon-normalizer

Go CLI and TUI that turn a church service recording into publish-ready sermon files
(cut, mono, loudness-normalised FLAC/MP3/Opus). `README.md` covers usage,
`PLAN.md` the background and design decisions.

## Layout

- `cmd/sermon-normalizer/`: CLI only (flags, usage, Ctrl+C). Keep logic out of it.
- `normalizer/`: the pipeline as an importable package. All audio work is
  ffmpeg, called with an argument list via `FFmpeg.Run`/`RunTo`, never a shell.
- `tui/`: Bubble Tea front end over `normalizer` (screens: browser, cut,
  options, run). It finds sources with `Paths.Sources` and reports through
  `Pipeline.OnStep`/`OnProgress` and `Out`; nothing may write to the
  terminal directly while it runs.
- `normalizer/` stays standard library only; dependencies belong in `tui/`.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (writes `.partial`, renames on success).
  Existing outputs are skipped without `--force`, so a half-written file
  would never be redone.
- Never delete a source unless its copy is verified identical: MD5 of the
  decoded audio for WAV -> FLAC, MD5 of the bytes for a move across disks.
  Without `--archive`, sources are never touched.
- Measure with `ebur128` (`FFmpeg.Measure`), not `loudnorm` (slow).
- Code, CLI output and docs are in English.

## Testing

ffmpeg is usually not installed on the dev host. Run everything in Docker/podman:

```bash
podman build -t sermon-normalizer .        # go vet + all tests incl. integration
podman run --rm -v $PWD:/src:Z -w /src docker.io/library/golang:1.25-alpine3.22 \
  sh -c 'apk add -q ffmpeg && go test -v ./...'   # faster iteration
```

Integration tests (`normalizer/integration_test.go`) skip without ffmpeg/ffprobe.
TUI end to end (tmux + ffmpeg in the container):

```bash
podman run --rm -v $PWD:/src:Z -w /src docker.io/library/golang:1.25-alpine3.22 sh scripts/tui-e2e.sh
```

Bubble Tea v1 delivers keys typed faster than a redraw as one multi-rune
message; the top-level model splits them, so tests can send `keys("]]s")`.
Unit tests that don't need ffmpeg run anywhere with `go test ./...`.

There is no CI or deploy yet.
