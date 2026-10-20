# sermon-studio

Agent notes (`CLAUDE.md` imports this file).

Go web service: cut a church service recording on a waveform, get
loudness-normalised FLAC/MP3/Opus. `README.md` = what it is, `docs/` = guide, running, outputs, design (was PLAN.md).
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
- `web/logo.go`: a church's own logo, `<church>/logo.png|jpg|webp|svg` (`PUT/GET/DELETE api/logo`, ≤1 MB, type sniffed; served
  with a sandbox CSP); its version is `logo` in the church list, and the header shows it instead of the app's.
- `web/names.go`: sermon names (`<date> <name>` folders; `cleanName`), rename/change date
  (`POST api/rename`: folder + files named after it, also in `original/`).
- `web/namelist.go`: remembered sermon names per church (suggestions on/off + list, learned on
  upload/rename; `GET/PUT api/names`), stored in `church.json` like the tags.
- `web/recording.go`: `<folder>/recording.json`, the one state file per recording: `recording` (format, metadata,
  channel verdict; redone when the file's size/mtime change), `convertedFrom` (the WAV, AIFF or video it was made from), `imported` (when it arrived: `markImported`; `recentImports` lists the last `newFor` on top of the first page), the sermon, and `outputs`
  (how each file was made: `madeFrom` fingerprint → stale/up to date). Change it only through `updateState` (one lock).
- `web/sermon.go`: the sermon part of it: `saved` (the version the user saved) + `adjusted`
  (what the editor keeps as you edit; null when equal to saved): sections, cuts, fades.
  `cutting` (`folders.go`) turns sections + cuts into the pipeline's Start/End/Cuts (gaps become cuts; a cut outside the
  sermon is clipped to it or dropped, never an error; sections and cuts have an optional `label`, which is not part of `cutOf`).
  `PUT api/sermon` (adjust), `POST api/sermon/save`, `POST api/sermon/revert`; returned as
  `sermon` in the folder info.
- `web/folders.go`: `api/recordings` (every date folder of the church, flat: the page groups/searches it), `api/labels` (a
  recording's labels, `cleanLabels`), folder info, run options, `check` (validates a run).
- `web/make.go`: `POST api/make?path&format`: one missing format from the saved version (a normal run, no Force).
  `POST api/outputs/remove?path&format=…`: deletes outputs (only those) and their records; not during a run.
- `web/jobs.go`: runs on the server context, one encode at a time, SSE events.
- `web/analysis.go`: conversion on arrival (`normalizer.ConvertsTo`: WAV/AIFF→FLAC, video→`.mka`), then waveform peaks + normalized MP3
  preview, cached by path+size+mtime; `carry` moves the cache on import.
- `web/upload.go`: chunked upload straight to a date folder (the form is on top of the recordings page, `uploadBox` in `app.js`).
- `web/inbox.go`: inbox file names, date suggestion, `newRawFolder`.
- `web/cleanup.go`: removes half-written files (`.partial`, hidden `.upload`) untouched for a day, at start and hourly.
- `web/inboxwatch.go`: `ingest` moves settled inbox files straight to their date
  folder (convert + prepare happen there).
- `web/share.go`: shares (codes for the public upload page), `<church>/shares.json`, the studio's
  `api/shares` (only with `--share-listen`; shown as the last section of Settings, `shares.js`). Revoke, and remove the ended ones (`removeShares`; an open share is never removed). `web/sharesite.go`: the public listener
  (`App.ShareHandler`: `share.html` + `api/share/*`, nothing else), chunked upload to
  `Innboks/.share-<id>.partial`. `web/ratelimit.go`: its limits per address and in all.
- `web/static/`: plain JS, no build. `app.js` = the hash router (`#/<church>/…`, old addresses without a church are moved; the
  church comes from the address) and the Settings overlay (`openSettings`, over whichever page is shown). `recordings.js` = the one recordings page (years, search, Newly imported, rows, `open(path)` for links); `sermons.js` = a sermon's details inside it (labels, copy link, play, download,
  make a format); clicking anywhere on a date row expands it; one with no files only offers the editor there. Its status (`badges`): to do / done from `saved` (or files), plus no files / files out of date (`outdated`: an output's `cut` ≠ `cutOf` the saved version). `icons/` = the logo: `logo.svg` is the one source (header, favicon,
  `manifest.webmanifest`); the PNGs (home screen, favicon fallback) come from it via `scripts/make-icons.sh`;
  the public page serves only the ones listed in `sharePage`. `dialog.js` = confirm/alert/prompt in the app's look
  (never use the browser's `confirm`/`alert`/`prompt`). `vendor/wavesurfer/` is wavesurfer.js
  7.12.12, unmodified.
- `web/static/i18n.js` + `locales.js` + `theme.js`: English, Norwegian bokmål (`nb`), nynorsk (`nn`) and the theme (automatic /
  light / dark; `data-theme` on `<html>`, set before the first paint). Every text the UI shows goes through `t('English text', {param})`
  (the English text is the key; `tn(n, 'one', 'other')` for plurals; `data-i18n` in the HTML). `tr(msg)` translates the
  server's English messages (those with a value in them are matched by pattern, e.g. `{size}`). A new text needs its `nb` and `nn`
  entry in `locales.js` (`TestLocales` fails otherwise). Never name a variable `t` where `t()` is called. The language is the
  browser's (`no` = bokmål) unless chosen in Settings → Appearance (or on the public page's header); stored in localStorage.
- `normalizer/`: the pipeline (`pipeline.go`; `Options.Cuts` = parts left out of
  Start..End, each kept part is its own ffmpeg input joined by `cutInput`'s filter graph),
  ffmpeg wrapper (`ffmpeg.go`:
  `Run`, `RunTo`), `ToFlac`/`SameAudio` (`flac.go`), `MoveFile` (`files.go`),
  `Validate` (`validate.go`), levels/silences, channel checks.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (`.partial`, renamed on success); existing
  outputs are skipped by a run; the editor's Save (Force in the API) redoes the stale ones it turns on. Save keeps the edit first, then runs.
- Delete a source only after its copy is verified identical (`FFmpeg.Convert`: `SameAudio` for
  WAV/AIFF→FLAC, the same packets for a video's sound; byte MD5 for copies). A run moves the whole
  recording into `<date>/original/` as it is (any of `normalizer.InputTypes`); the outputs are cut
  straight from it using the sections + cuts, there is no edit file. Every encode maps only `0:a:0`
  (an MP3's cover picture or a video must not end up in the outputs).
- The input types are one table, `normalizer/types.go`; `INPUT_TYPES` in `util.js` and the
  upload page's `magicOK` follow it (tests check both).
- No long work inside a request: it sits behind Cloudflare (100 MB bodies,
  ~100 s timeout). Start background work on `s.ctx` under `s.wg`.
- Measure with `ebur128` (`FFmpeg.Measure`), not `loudnorm`.
- Messages from `normalizer` show in the web UI: use the form's names
  (Start, Save, mono), not flags. English everywhere.
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
