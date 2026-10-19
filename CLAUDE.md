# sermon-studio

Go web service: cut a church service recording on a waveform, get
loudness-normalised FLAC/MP3/Opus. `README.md` = usage, `PLAN.md` = background.
Standard library only (ffmpeg is run as a command).

## Checks (use these, in this order; output is already short)

```bash
scripts/test.sh                       # unit tests, seconds — run after every change
scripts/test.sh all ./web/ -run X     # with real ffmpeg, one package/test (container)
scripts/test.sh all                   # everything (~3 min, normalizer integration is slow)
scripts/test.sh e2e [-g name]         # browser tests (~1.5 min); after changing web/static
scripts/test.sh image                 # Docker build; before release changes
```

ffmpeg is not on the dev host; `all`/`e2e` run in podman. Don't run `go test -v`
over the whole tree, and don't paste whole files into output: read the part you
need (`grep -n`, offsets). Playwright screenshots land in `web/e2e/screenshots/`
— look at them only when changing visible UI.

## Map

- `cmd/sermon-studio/main.go`: flags, startup, shutdown. No logic.
- `web/app.go`: `App` = the churches (tenants). A church is a folder in
  `--data-dir`: `<id>/church.json` (name, tags, names; optional), `<id>/Taler`,
  `<id>/Innboks`. Picks the `Server` per request (`?tenant=` / `X-Tenant`, default
  = first), shared one-encode/one-decode limits. A `Server` is one church.
  `--root`/`--inbox` = one church without a folder (settings not saved).
- `web/access.go`: `--groups-header` access control: group `sermon-studio-<church id>` = that church,
  `sermon-studio-admin` = all + add churches; `admin`/`default` are reserved ids. Every church request
  goes through `App.tenantFor`, which only picks churches the user sees.
- `web/server.go`: Config, routes, `resolve` (every path parameter: inside the
  root, no `..`, no hidden names, no symlinks out).
- `web/meta.go`: a church's default tags (title/artist/album templates with
  `{yyyy}` `{mm}` `{dd}` `{date}` `{weekday}` `{name}`...), settings API, the tags of a run.
  Stored per church in its `church.json`; `normalizer.Tags` carries them to ffmpeg.
- `web/names.go`: sermon names (`<date> <name>` folders; `cleanName`), rename/change date
  (`POST api/rename`: folder + files named after it, also in `original/`).
- `web/namelist.go`: remembered sermon names per church (suggestions on/off + list, learned on
  upload/rename; `GET/PUT api/names`), stored in `church.json` like the tags.
- `web/sermon.go`: `<folder>/sermon.json` = `saved` (the version the user saved) + `adjusted`
  (what the editor keeps as you edit; null when equal to saved): selection, cuts, fades.
  `PUT api/sermon` (adjust), `POST api/sermon/save`, `POST api/sermon/revert`; returned as
  `sermon` in the folder info.
- `web/folders.go`: folder list/info, run options, `check` (validates a run).
- `web/jobs.go`: runs on the server context, one encode at a time, SSE events.
- `web/analysis.go`: WAV→FLAC conversion, then waveform peaks + normalized MP3
  preview, cached by path+size+mtime; `carry` moves the cache on import.
- `web/upload.go`: chunked upload straight to a date folder.
- `web/inbox.go`: inbox file names, date suggestion, `newRawFolder`.
- `web/inboxwatch.go`: `ingest` moves settled inbox files straight to their date
  folder (convert + prepare happen there).
- `web/static/`: plain JS, no build. `dialog.js` = confirm/alert/prompt in the app's look
  (never use the browser's `confirm`/`alert`/`prompt`). `vendor/wavesurfer/` is wavesurfer.js
  7.12.12, unmodified.
- `normalizer/`: the pipeline (`pipeline.go`; `Options.Cuts` = parts left out of
  Start..End, each kept part is its own ffmpeg input joined by `cutInput`'s filter graph),
  ffmpeg wrapper (`ffmpeg.go`:
  `Run`, `RunTo`), `ToFlac`/`SameAudio` (`flac.go`), `MoveFile` (`files.go`),
  `Validate` (`validate.go`), levels/silences, channel checks.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (`.partial`, renamed on success); existing
  outputs are skipped by Run; Save and generate (Force in the API) redoes them.
- Delete a source only after its copy is verified identical (`SameAudio` for
  WAV→FLAC, byte MD5 for copies). A run moves the whole recording into
  `<date>/original/` (WAV → verified FLAC); the outputs are cut straight from it
  using the selection + cuts, there is no edit file.
- No long work inside a request: it sits behind Cloudflare (100 MB bodies,
  ~100 s timeout). Start background work on `s.ctx` under `s.wg`.
- Measure with `ebur128` (`FFmpeg.Measure`), not `loudnorm`.
- Messages from `normalizer` show in the web UI: use the form's names
  (Start, Save and generate, mono), not flags. English everywhere.
- Keep the Playwright image tag (`scripts/test.sh`) equal to `@playwright/test`
  in `web/e2e/package.json`.

Deploy: push to `main` → GitHub Actions publishes `ghcr.io/gunnaringe/sermon-studio:main-<ts>-<sha>`
→ Flux rolls it out on kate (`clusters/kate/norkirken/`, namespace `norkirken`,
https://sermons.haxxor.xyz).
