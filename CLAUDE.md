# sermon-normalizer

Go CLI that turns a church service recording into publish-ready sermon files
(cut, mono, loudness-normalised FLAC/MP3/Opus). `README.md` covers usage,
`PLAN.md` the background and design decisions.

## Layout

- `cmd/sermon-normalizer/`: CLI only (flags, usage, Ctrl+C). Keep logic out of it.
- `normalizer/`: the pipeline as an importable package. All audio work is
  ffmpeg, called with an argument list via `FFmpeg.Run`/`RunTo`, never a shell.
- Standard library only; no external Go dependencies.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (writes `.partial`, renames on success).
  Existing outputs are skipped without `--force`, so a half-written file
  would never be redone.
- Never delete a source unless its audio is verified identical (MD5 of the
  decoded stream). Without `--archive`, sources are never touched.
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
Unit tests that don't need ffmpeg run anywhere with `go test ./...`.

There is no CI or deploy yet.
