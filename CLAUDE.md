# sermon-normalizer

Go web service: cut a church service recording on a waveform, get
loudness-normalised FLAC/MP3/Opus. `README.md` = usage, `PLAN.md` = background.
Standard library only (ffmpeg and sqlite3 are run as commands).

## Checks (use these, in this order; output is already short)

```bash
scripts/test.sh                       # unit tests, seconds — run after every change
scripts/test.sh all ./web/ -run X     # with real ffmpeg/sqlite3, one package/test (container)
scripts/test.sh all                   # everything (~3 min, normalizer integration is slow)
scripts/test.sh e2e [-g name]         # browser tests (~1.5 min); after changing web/static
scripts/test.sh image                 # Docker build; before release changes
```

ffmpeg is not on the dev host; `all`/`e2e` run in podman. Don't run `go test -v`
over the whole tree, and don't paste whole files into output: read the part you
need (`grep -n`, offsets). Playwright screenshots land in `web/e2e/screenshots/`
— look at them only when changing visible UI.

## Map

- `cmd/sermon-normalizer/main.go`: flags, startup, shutdown. No logic.
- `web/server.go`: Config, routes, `resolve` (every path parameter: inside the
  root, no `..`, no hidden names, no symlinks out).
- `web/folders.go`: folder list/info, run options, `check` (validates a run).
- `web/jobs.go`: runs on the server context, one encode at a time, SSE events.
- `web/analysis.go`: WAV→FLAC conversion, then waveform peaks + normalized MP3
  preview, cached by path+size+mtime; `carry` moves the cache on import.
- `web/upload.go`: chunked upload straight to a date folder.
- `web/inbox.go`: inbox list/import, date suggestion, `newRawFolder`.
- `web/pingvin.go`: polls Pingvin Share's SQLite (read-only) for reverse-share
  uploads, converts them into the inbox. Schema fixture in `pingvin_test.go`.
- `web/static/`: plain JS, no build. `vendor/wavesurfer/` is wavesurfer.js
  7.12.12, unmodified.
- `normalizer/`: the pipeline (`pipeline.go`), ffmpeg wrapper (`ffmpeg.go`:
  `Run`, `RunTo`), `ToFlac`/`SameAudio` (`flac.go`), `MoveFile` (`files.go`),
  `Validate` (`validate.go`), levels/silences, channel checks.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (`.partial`, renamed on success); existing
  outputs are skipped without Force.
- Delete a source only after its copy is verified identical (`SameAudio` for
  WAV→FLAC, byte MD5 for copies). Raw WAVs are replaced by verified FLAC; with
  no archive folder, runs never touch sources. Pingvin's files are never changed.
- No long work inside a request: it sits behind Cloudflare (100 MB bodies,
  ~100 s timeout). Start background work on `s.ctx` under `s.wg`.
- Measure with `ebur128` (`FFmpeg.Measure`), not `loudnorm`.
- Messages from `normalizer` show in the web UI: use the form's names
  (Start, Force, mono), not flags. English everywhere.
- Keep the Playwright image tag (`scripts/test.sh`) equal to `@playwright/test`
  in `web/e2e/package.json`.

There is no CI or deploy yet.
