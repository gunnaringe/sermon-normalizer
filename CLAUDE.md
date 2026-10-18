# sermon-normalizer

Go CLI and TUI that turn a church service recording into publish-ready sermon files
(cut, mono or stereo, loudness-normalised FLAC/MP3/Opus). `README.md` covers usage,
`PLAN.md` the background and design decisions.

## Layout

- `cmd/sermon-normalizer-cli/`: the CLI (flags, usage, Ctrl+C), and the TUI
  with `--interactive`. Keep logic out of it.
- `normalizer/`: the pipeline as an importable package. All audio work is
  ffmpeg, called with an argument list via `FFmpeg.Run`/`RunTo`, never a shell.
- `tui/`: Bubble Tea front end over `normalizer` (screens: browser, cut,
  options, run). It finds sources with `Paths.Sources` and reports through
  `Pipeline.OnStep`/`OnProgress` and `Out`; nothing may write to the
  terminal directly while it runs.
- `cmd/sermon-normalizer/` + `web/`: the web app (the main binary; also the
  Docker image's entrypoint). Jobs run on the
  server's context (not the request's); `Server.Close` cancels and waits.
  Every path parameter goes through `Server.resolve` (inside the root, no
  `..`, no hidden names, no symlinks out). `web/static/` is plain JS with no
  build step; `web/static/vendor/wavesurfer/` is wavesurfer.js 7.12.12,
  unmodified (see its README) — upgrade by replacing the files.
- `normalizer/` stays standard library only; dependencies belong in `tui/`.
  Shared rules (validation, the CLI command line, verified moves) live in
  `normalizer` so the TUI and the web cannot drift.

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

Web browser tests (Playwright in its pinned image, real ffmpeg, a
90-minute recording for responsiveness):

```bash
CGO_ENABLED=0 go build -o web/e2e/bin/sermon-normalizer ./cmd/sermon-normalizer
podman run --rm --ipc=host -v "$PWD/web/e2e":/work:Z -w /work \
  mcr.microsoft.com/playwright:v1.64.1-noble sh run.sh
```

Keep the image tag equal to `@playwright/test` in `web/e2e/package.json`.
Screenshots land in `web/e2e/screenshots/` (ignored by git).

Bubble Tea v1 delivers keys typed faster than a redraw as one multi-rune
message; the top-level model splits them, so tests can send `keys("]]s")`.
Unit tests that don't need ffmpeg run anywhere with `go test ./...`.

There is no CI or deploy yet.
