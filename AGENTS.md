# sermon-studio

Agent notes (`CLAUDE.md` imports this file).

Go web service: cut a church service recording on a waveform, get
loudness-normalised FLAC/MP3/Opus. `README.md` = what it is, `docs/` = guide, running, outputs, design (was PLAN.md).
The standard library, `golang.org/x/sys` (for Windows), `coreos/go-oidc` + `golang.org/x/oauth2` (the login), `alecthomas/kong`
(the command line); ffmpeg is run as a command. Keep dependencies few.

## Checks (use these, in this order; output is already short)

```bash
scripts/test.sh                       # unit tests, seconds — run after every change
scripts/test.sh all ./web/ -run X     # with real ffmpeg, one package/test (container)
scripts/test.sh all                   # everything (~3 min, normalizer integration is slow)
scripts/test.sh e2e [-g name]         # browser tests (~1.5 min); after changing web/static
scripts/test.sh desktop               # the desktop program as released, from the outside (~15 s); after
                                      # changing cmd/sermon-studio-desktop or what it uses of web
scripts/test.sh image                 # Docker build; before release changes
scripts/fetch-sample.sh && scripts/test.sh guide   # redo docs/img (the guide's pictures) when the UI changes
scripts/check-links.sh                # relative links and pictures in the Markdown files
```

ffmpeg is not on the dev host; `all`/`e2e` run in podman, in images with ffmpeg that `test.sh` builds
once (`scripts/test-images/`, retagged when the file or the base image changes). The browser tests keep
`node_modules` until the lock file changes, and the test recordings in `web/e2e/fixtures/made/` (delete the
folder to make them again). With `FFMPEG_REQUIRED` set (CI, the image build, `all`) a missing ffmpeg fails
the tests instead of skipping them; a test run much faster than usual still means something was skipped.

**Never start the desktop program against the real home folder**: it makes `~/Sermon Studio`, settings and a
cache, and on an error it opens a dialog on the user's screen. `TestEndToEnd` (`cmd/sermon-studio-desktop`)
starts it with folders of its own (and `--no-window`, which also keeps error dialogs away); use it, or the
same variables (`HOME`, `XDG_*`, `APPDATA`, `LOCALAPPDATA`) in a temporary folder, for one-off checks. It runs
on Windows, macOS and Linux in CI, and on every release program before it is published.

Don't run `go test -v`
over the whole tree, and don't paste whole files into output: read the part you
need (`grep -n`, offsets). Playwright screenshots land in `web/e2e/screenshots/`
— look at them only when changing visible UI.

## Map

- `cmd/sermon-studio/main.go`: the command line (kong: `serve`, the default, and `admin`), startup, shutdown. No logic. Every flag
  is also `SERMON_STUDIO_<FLAG>` (`kong.DefaultEnvars`); an unknown `SERMON_STUDIO_*` is a warning, not an error
  (`warnUnknownEnv`). The login is required to be chosen: `--oidc-issuer` xor `--no-login`. `admin.go`: `sermon-studio admin
  invite` (prints an invite link).
- `cmd/sermon-studio-desktop/`: the desktop app (docs/desktop.md), a wrapper around `web.App`: `main.go`
  starts it; `internal/desktop/` has its folders and `settings.json`, the Host check (`Guard`), the window
  (`OpenWindow`), one copy at a time (`/_desktop/hello` with the token) and stopping when idle (`Presence`:
  `/_desktop/alive`, an event stream held by a script it adds to the page, plus `App.Busy`). ffmpeg is built in
  with `-tags embedffmpeg` (`FFmpeg`: unpacked into the cache, checked by its hash); `scripts/build-desktop.sh`
  does that, with the release of github.com/gunnaringe/ffmpeg-lgpl pinned in `packaging/ffmpeg-release` and
  `packaging/ffmpeg.sha256` (that repo builds it and runs this repo's Go tests against every build).
  A tag `v*` releases the desktop app (`release.yml`, `scripts/package-desktop.sh`, `packaging/`) and tags the
  image `vX.Y.Z`.
- `web/app.go`: `App` = the churches (tenants). A church is a folder in
  `--data-dir`: `<id>/church.json` (name, tags; optional), `<id>/Taler`,
  `<id>/Innboks`. Picks the `Server` per request (`?tenant=` / `X-Tenant`, default
  = first), shared limits: `--parallel-runs` encodes (runs, samples; 1 by default), one decode. A `Server` is one church.
  `--root`/`--inbox` = one church without a folder (settings not saved).
- `web/login.go`: the studio's own login (`--oidc-issuer`, `AppConfig.OIDC`): authorization code + PKCE, the ID token checked by
  go-oidc (+ nonce), then a session of its own (cookie `studio_session`, 30 days; `<data-dir>/sessions.json` keeps the cookie's hash and
  the ID token, for logging out;
  `users.json` = issuer + sub, with name/email for display only, never for access). `require` wraps the whole handler: no session
  = a page goes to `auth/login?next=`, `api/…` answers 401 (`util.js` `toLogin`); only `auth/*` and static files other than
  `index.html` are public. Logout (`POST auth/logout`) ends the session, then, if the provider has an `end_session_endpoint`, sends
  the browser there (RP-initiated logout, with `id_token_hint` and a `state` in the cookie `studio_logout`), which comes back to
  `auth/logout/callback`; then, or without one, `loggedout.html`. The provider is found on the first login, so the studio starts
  while it is down. Pocket ID, the recommended provider: docs/login-pocket-id.md, its styling assets in `docs/pocket-id/`
  (`scripts/make-pocket-id-assets.sh`).
- `web/members.go`: with the login, a church is for its members (`<church>/members.json`: issuer, sub, role admin/member). They
  join by an invite link `invite.html#<church>.<secret>` (`addInvite`: from Settings → Members, `POST api/invites`, or
  `sermon-studio admin invite`, `CreateInvite`; `<church>/invites.json` keeps hashes, single use, an expiry; revoke by `id`, the
  hash's start). `api/invite` (GET what it is for, POST accept) needs a session but no church. Admin-only requests are listed in
  `adminOnly` and stopped by `requireAdmin` before any handler (settings, logo, clips, ended-days, members, invites); a member
  gets 403. `changeMembers` keeps an admin. The church list carries the user's `role`; `util.js` `isAdmin()` hides the admin
  parts of Settings (`members.js` is the Members section) and the editor's Add to the church's clips.
- `web/access.go`: who sees which church: with the login, its members; without, everyone (and only then are churches added in
  the studio, `canAdd`). `default` and the page words are reserved ids. Every church request goes through `App.tenantFor`,
  which only picks churches the user sees.
- `web/server.go`: Config, routes, `resolve` (every path parameter: inside the
  root, no `..`, no hidden names, no symlinks out).
- `web/meta.go`: a church's default tags (title/artist/album templates with
  `{yyyy}` `{mm}` `{dd}` `{date}` `{weekday}` `{name}`...), settings API, the tags of a run.
  Stored per church in its `church.json`; `normalizer.Tags` carries them to ffmpeg.
- `web/processing.go`: a church's default processing (`processing` in `church.json`; `PUT api/processing`, null = the built-in),
  what a recording starts with (`config.defaults.start`, plus `mono`/`stereo` for the switch); a recording's own is the sermon's `sound`.
  Settings → Processing is `processing.js`.
- `web/logo.go`: a church's own logo, `<church>/logo.png|jpg|webp|svg` (`PUT/GET/DELETE api/logo`, ≤1 MB, type sniffed; served
  with a sandbox CSP); its version is `logo` in the church list, and the header shows it instead of the app's, always in a square
  (another shape shrunk to fit; Settings warns), so the header is the same for every church.
- `web/clips.go`: a church's intro and outro clips (#63): `<church>/clips/<id>.flac`, the list, the default intro/outro and the
  pause in `church.json` (`clips`). An upload (`POST api/clips?name=`, `.../{id}/replace`) is converted, measured and given a
  `version` in the background (`prepareClip`, the decode limit); until then it is `pending` (with its error if it failed).
  Archive/restore/rename (`PUT api/clips/{id}`), delete (`POST .../delete`), defaults (`PUT api/clips/defaults`); the default
  intro or outro cannot be archived or deleted. A sermon's choice is `intro`/`outro` in `sermonSettings` (`default`, `own`, a
  clip id, "" = none): only its form is validated, never that the clip exists (a saved sermon must not be dropped).
  `clipUse` counts the sermons (saved or draft) that chose each clip. Settings → Intro and outro is `clips.js`.
- `web/sermonclips.go`: a sermon's clips: `check` resolves its choice (`applyClips`: the files into `Options.Intro/Outro`, `plan.clips`
  for the page; a chosen clip that is gone refuses the run). Its own clips are `<folder>/clips/intro|outro.flac` (`ownClips` in
  `recording.json`; `POST api/sermon/clip?which=`, `/dismiss`, `/add` copies one to the church, `GET api/sermon/clips` is what
  the editor's Intro and outro step shows). The clips' ids and versions are in `madeFrom` (`runClips.key`, "" without clips);
  the sermon-only files are recorded as `<format>_sermon`, and a run without clips removes them (`dropSermonOnly`). Folder info lists
  them as outputs `<format>_sermon` and says what the files were made with (`madeWith`, `changed` when the saved choice would
  now give other clips); deleting a format deletes both versions; a download share can give the sermon only (`sermonOnly`).
  `POST api/sample/join?which=` plays a join (`normalizer.FFmpeg.JoinSample`: 10 s of the sermon made as the files are, with
  6 s of the clip and the pause), in the request like a sample.
- `web/names.go`: sermon names (`<date> <name>` folders; `cleanName`), rename/change date
  (`POST api/rename`: folder + files named after it, also in `original/`).
- `web/trash.go`: deleting a recording moves its date folder to `<root>/.trash/<id>/` (+ `trash.json`: where from, when); `GET api/trash`,
  restore, remove for good, and the purge after `--trash-days` (hourly, with the leftovers sweep). Hidden, so no path of the API reaches it.
  `POST api/recordings/delete-original` deletes only the original (for good, with its analysis cache), keeping the files: only once
  files are made, never during a run; `originalDeleted` in `recording.json`, and `check` then says the files cannot be made again.
  Both are on the edit page (Delete), not the recordings page.
- `web/recording.go`: `<folder>/recording.json`, the recording's state: `recording` (format, metadata,
  channel verdict; redone when the file's size/mtime change), `convertedFrom` (the WAV, AIFF or video it was made from), `imported` (when it arrived: `markImported`; `recentImports` lists the last `newFor` on top of the first page), the sermon, and `outputs`
  (how each file was made: `madeFrom` fingerprint → stale/up to date). Change it only through `updateState` (one lock;
  not written when nothing changed). It must stay quiet while someone edits: it is what a sync tool will carry (#50).
- `web/sermon.go`, `web/draft.go`: the sermon: `saved` (in recording.json, the version the user saved) + `adjusted`
  (`<folder>/draft.json`, what the editor keeps as you edit, per recording, with when and on which machine; removed
  when equal to saved, by Save and by Revert): sections, cuts, fades. The API gives both as one (`sermonFile`).
  The editor's undo/redo steps are kept in the browser (localStorage `history:<church>:<path>`, the last 20
  recordings), used only while their last step is the edit the page opens with.
  `cutting` (`folders.go`) turns sections + cuts into the pipeline's Start/End/Cuts (gaps become cuts; a cut outside the
  sermon is clipped to it or dropped, never an error; sections and cuts have an optional `label`, which is not part of `cutOf`, and an optional `pause`: silence put in where a section ends or a cut starts, which is; sections that only touch stay two).
  `PUT api/sermon` (adjust), `POST api/sermon/save`, `POST api/sermon/revert`; returned as
  `sermon` in the folder info.
- `web/folders.go`: `api/recordings` (every date folder of the church, flat: the page groups/searches it), `api/labels` (a
  recording's labels, `cleanLabels`), folder info, run options, `check` (validates a run).
- `web/make.go`: `POST api/make?path&format`: one missing format from the saved version (a normal run, no Force).
  `POST api/outputs/remove?path&format=…`: deletes outputs (only those) and their records; not during a run.
- `web/jobs.go`: runs on the server context, `--parallel-runs` at a time (never two of one recording), SSE events.
- `web/analysis.go`: conversion on arrival (`normalizer.ConvertsTo`: WAV/AIFF→FLAC, video→`.mka`), then waveform peaks + normalized MP3
  preview, cached by path+size+mtime; `carry` moves the cache on import.
- `web/upload.go`: chunked upload straight to a date folder (the form is on top of the recordings page, `uploadBox` in `app.js`).
- `web/inbox.go`: inbox file names, date suggestion, `newRawFolder`.
- `web/cleanup.go`: removes half-written files (`.partial`, hidden `.upload`) untouched for a day, at start and hourly.
- `web/inboxwatch.go`: `ingest` moves settled inbox files straight to their date
  folder (convert + prepare happen there).
- `web/share.go`: shares (codes for the public share page: upload shares, and download shares, `sharedownload.go`), `<church>/shares.json`, the studio's
  `api/shares` (only with `--share-listen`; its own overlay from the share icon in the header, `#/<church>/shares`, `shares.js`; the icon's number is `api/shares/active`). Pause/resume (the same code works again), remove a paused or ended one (`removeShares`; an open share is never removed; remove-ended leaves the paused). `web/sharesite.go`: the public listener
  (`App.ShareHandler`: `share.html` + `api/share/*`, nothing else), chunked upload to
  `Innboks/.share-<id>.partial`. `web/ratelimit.go`: its limits per address and in all.
- `web/sharedownload.go`: a download share gives one recording's finished files (`kind: "download"`, made with Share for download… in
  `sermons.js`). It finds its recording by `id` in `recording.json` (given when the share is made; the path is a hint that follows a
  rename, a recording in the trash is not found). `POST api/share/downloads` counts one and hands out a ticket (`GET api/share/downloads/{id}`,
  no code, Range, `ticketFor`); the same file from the same address while its ticket lasts counts nothing.
- `web/static/`: plain JS, no build. `app.js` = the hash router (`#/<church>/…`, old addresses without a church are moved; the
  church comes from the address), the Settings overlay (`openSettings`, over whichever page is shown) and About (`openAbout`,
  from the footer, `#/<church>/about`, signed with `fonts/signature.woff2`: the author's handwriting, only the letters of the
  name, not under the licence, see `NOTICE`; never add letters to it or use it for anything else; the footer sits at the bottom of a short
  page: `body` is a flex column). `recordings.js` = the one recordings page (years, search, Newly imported, rows, `open(path)` for links); `sermons.js` = a sermon's details inside it (labels, copy link, play, the Download menu,
  make a format); clicking anywhere on a date row expands it; one with no files only offers the editor there. Its status (`badges`): to do / done from `saved` (or files), plus no files / files out of date (`outdated`: an output's `cut` ≠ `cutOf` the saved version) / not saved (`adjusted`: a draft.json). `icons/` = the logo: `logo.svg` is the one source (header, favicon,
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
  Start..End, each kept part is its own ffmpeg input joined by `cutInput`'s filter graph; `Intro`/`Outro` clips are
  joined around the finished sermon by `join`, making the main files with them and `<date>_sermon.*` without, `versions`),
  ffmpeg wrapper (`ffmpeg.go`:
  `Run`, `RunTo`), `ToFlac`/`SameAudio` (`flac.go`), `MoveFile` (`files.go`),
  `Validate` (`validate.go`), levels/silences, channel checks.

## Rules that matter

- Outputs go through `FFmpeg.RunTo` (`.partial`, renamed on success); existing
  outputs are skipped by a run; the editor's Save (Force in the API) redoes the stale ones it turns on. Save keeps the edit first, then runs.
- Delete a source only after its copy is verified identical (`FFmpeg.Convert`: `SameAudio` for
  WAV/AIFF→FLAC, the same packets for a video's sound; byte MD5 for copies). A run moves the whole
  recording into `<date>/original/` as it is (any of `normalizer.InputTypes`), named as it arrived (`sourceName` in `recording.json`; else `<date>_raw.<ext>`; `Paths.RawOriginal` finds either); the outputs are cut
  straight from it using the sections + cuts, there is no edit file. Every encode maps only `0:a:0`
  (an MP3's cover picture or a video must not end up in the outputs).
- The input types are one table, `normalizer/types.go`; `INPUT_TYPES` in `util.js` and the
  upload page's `magicOK` follow it (tests check both).
- No long work inside a request: it sits behind Cloudflare (100 MB bodies,
  ~100 s timeout). Start background work on `s.ctx` with `s.background` (Close waits for it, `Busy` reports it).
- `web/` and `normalizer/` know nothing of the desktop app: no desktop flags, settings or branches. It uses
  the studio's public API only (`NewApp`, `Handler`, `Busy`, `Close`); what it needs more goes in
  `cmd/sermon-studio-desktop/`, so it can be removed by deleting that folder (and its docs and build script).
- Rename and remove with `normalizer.Rename`/`Remove`/`RemoveAll`, not the `os` ones: on Windows they try
  again for a few seconds while a file is in use (a preview playing, a virus scan). A sermon folder's path
  must fit Windows' 259 characters with its longest file (`folderFits`); names cannot end in a dot, and
  Windows' device names (`nul`, `com1` ...) are not churches or kept file names (`windowsDevice`).
- Measure with `ebur128` (`FFmpeg.Measure`), not `loudnorm`.
- Messages from `normalizer` show in the web UI: use the form's names
  (Start, Save, mono), not flags. English everywhere.
- The public share page is on the internet without a login: add nothing to `ShareHandler`
  that reads files or reaches the studio (the two exceptions: `api/share/logo` sends the church's logo to a valid code, and a download
  share's ticket sends its recording's finished files, by format, never by a path); `TestSharePublicIsSeparate` checks this.
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
really in `origin/main`, and bring the local checkout up to date (`git switch main && git pull`).

```bash
scripts/ci-wait.sh [pr]     # wait for every check; one line each, the end of a failed job's log; exit 1 on failure
scripts/pr-land.sh [pr]     # only when the user has said to merge: ci-wait, squash-merge, check main has the
                            # PR's files, delete the branch, update the local main
```

Don't hand-roll waiting loops around `gh run watch`/`gh run view`: they have stalled and spun on empty run ids. A PR that changes anything visible includes screenshots of it:

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
