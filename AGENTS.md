# sermon-studio

Agent notes (`CLAUDE.md` imports this file).

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
- `web/access.go`: `--groups-header` access control: group `sermon_studio_<church id, - → _>` = that church,
  `sermon_studio_admin` = all + add churches; `admin`/`default` are reserved ids. Every church request
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
- `web/recording.go`: `<folder>/recording.json`, the one state file per recording: `recording` (format, metadata,
  channel verdict; redone when the file's size/mtime change), `convertedFrom` (the WAV), the sermon, and `outputs`
  (how each file was made: `madeFrom` fingerprint → stale/up to date). Change it only through `updateState` (one lock).
- `web/sermon.go`: the sermon part of it: `saved` (the version the user saved) + `adjusted`
  (what the editor keeps as you edit; null when equal to saved): sections, cuts, fades.
  `cutting` (`folders.go`) turns sections + cuts into the pipeline's Start/End/Cuts (gaps become cuts).
  `PUT api/sermon` (adjust), `POST api/sermon/save`, `POST api/sermon/revert`; returned as
  `sermon` in the folder info.
- `web/folders.go`: folder list/info, run options, `check` (validates a run).
- `web/make.go`: `POST api/make?path&format`: one missing format from the saved version (a normal run, no Force).
- `web/jobs.go`: runs on the server context, one encode at a time, SSE events.
- `web/analysis.go`: WAV→FLAC conversion, then waveform peaks + normalized MP3
  preview, cached by path+size+mtime; `carry` moves the cache on import.
- `web/upload.go`: chunked upload straight to a date folder.
- `web/inbox.go`: inbox file names, date suggestion, `newRawFolder`.
- `web/inboxwatch.go`: `ingest` moves settled inbox files straight to their date
  folder (convert + prepare happen there).
- `web/share.go`: shares (codes for the public upload page), `<church>/shares.json`, the studio's
  `api/shares` (only with `--share-listen`). `web/sharesite.go`: the public listener
  (`App.ShareHandler`: `share.html` + `api/share/*`, nothing else), chunked upload to
  `Innboks/.share-<id>.partial`. `web/ratelimit.go`: its limits per address and in all.
- `web/static/`: plain JS, no build. `sermons.js` = a sermon's details inside the folder list (play, download,
  make a format); clicking anywhere on a date row expands it; an unprocessed recording only offers the editor there. `icons/` = the logo: `logo.svg` is the one source (header, favicon,
  `manifest.webmanifest`); the PNGs (home screen, favicon fallback) come from it via `scripts/make-icons.sh`;
  the public page serves only the ones listed in `sharePage`. `dialog.js` = confirm/alert/prompt in the app's look
  (never use the browser's `confirm`/`alert`/`prompt`). `vendor/wavesurfer/` is wavesurfer.js
  7.12.12, unmodified.
- `normalizer/`: the pipeline (`pipeline.go`; `Options.Cuts` = parts left out of
  Start..End, each kept part is its own ffmpeg input joined by `cutInput`'s filter graph),
  ffmpeg wrapper (`ffmpeg.go`:
  `Run`, `RunTo`), `ToFlac`/`SameAudio` (`flac.go`), `MoveFile` (`files.go`),
  `Validate` (`validate.go`), levels/silences, channel checks.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (`.partial`, renamed on success); existing
  outputs are skipped by a run; Regenerate (Force in the API) redoes the stale ones it turns on.
- Delete a source only after its copy is verified identical (`SameAudio` for
  WAV→FLAC, byte MD5 for copies). A run moves the whole recording into
  `<date>/original/` (WAV → verified FLAC); the outputs are cut straight from it
  using the sections + cuts, there is no edit file.
- No long work inside a request: it sits behind Cloudflare (100 MB bodies,
  ~100 s timeout). Start background work on `s.ctx` under `s.wg`.
- Measure with `ebur128` (`FFmpeg.Measure`), not `loudnorm`.
- Messages from `normalizer` show in the web UI: use the form's names
  (Start, Regenerate, mono), not flags. English everywhere.
- The public upload page is on the internet without a login: add nothing to `ShareHandler`
  that reads files or reaches the studio; `TestSharePublicIsSeparate` checks this.
- Keep the Playwright image tag (`scripts/test.sh`) equal to `@playwright/test`
  in `web/e2e/package.json`.

## Pull requests, with screenshots

Every change goes in a PR (branch, push, `gh pr create`), never straight to `main`:
merging deploys. **You handle all of the git yourself**: branch from a fresh `origin/main`, commit, push,
open the PR, and keep it mergeable (rebase on `main`, fix conflicts). The user only reviews and presses
merge; never ask them to check out a branch, pull, rebase or retarget. Prefer one PR per change, off
`main`. Stack one on another only when it must be, and then say so, because a PR merged into its parent's
branch never reaches `main`: once the parent is merged, retarget the child (`gh pr edit --base main`)
before it is merged, or open a new PR from the same branch. After a merge, check that the change is
really in `origin/main`, and bring the local checkout up to date (`git switch main && git pull`). A PR that changes anything visible includes screenshots of it:

```bash
scripts/test.sh e2e [-g name]                 # saves web/e2e/screenshots/<name>.png
scripts/pr-shots.sh --pr <name|png>...        # adds them to this branch's PR
```

`pr-shots.sh` shrinks the images and keeps them on the orphan `pr-shots` branch (nothing
lands in `main`). Show only the screens that changed; for a new screen, add a
`page.screenshot` to an e2e test so it can be shown again later.

Deploy: merging to `main` → GitHub Actions publishes `ghcr.io/gunnaringe/sermon-studio:main-<ts>-<sha>`
→ Flux rolls it out on kate (`clusters/kate/norkirken/`, namespace `norkirken`,
https://sermons.haxxor.xyz).
