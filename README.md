# sermon-studio

A web app that turns a recording of a church service into publish-ready sermon files:
cut out the sermon on a waveform, then it mixes to mono (or keeps stereo), evens out the volume
(speech and music are recorded at very different levels), normalises to podcast loudness and
encodes FLAC, MP3 and Opus for SoundCloud, Spotify and other podcast services.
See `PLAN.md` for background and design decisions.

## Requirements

- `ffmpeg` in PATH (Linux: `sudo apt install ffmpeg`), or use the Docker image
- Go 1.25+ to build

## Build and test

```bash
go build -o sermon-studio ./cmd/sermon-studio
scripts/test.sh            # unit tests, seconds
scripts/test.sh all        # vet + all tests with real ffmpeg (container)
scripts/test.sh e2e        # browser tests (Playwright, container)
```

The integration tests run the whole pipeline with real ffmpeg on synthetic recordings;
`docker build .` runs them too.

## Docker (ffmpeg included)

ffmpeg is a separate C program and cannot be built into the Go binary, so the image packages both.

```bash
docker build -t sermon-studio .
docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" \
  -v /path/to/data:/data -v /path/to/cache:/cache \
  sermon-studio --data-dir /data --cache-dir /cache
```

`-u` makes the created files owned by your user, not root.
The build runs `go vet` and `go test`, and checks that ffmpeg has libopus and libmp3lame.

## Running

```bash
sermon-studio --data-dir /path/to/data --listen :8080
```

The data dir has a folder per church (a church is a folder with a `Taler` folder in it):

```
data/norkirken-melhus/
  Taler/<year>/<date>[ <name>]/   the sermons
  Innboks/                        the inbox
  church.json                     optional: name, default tags, remembered names (written by the app)
```

The folder name is the church's id and, until it is renamed on the Settings page, its name. Add a
church in the app ("+ Add church…") or just make the folder. A `church.json` that cannot be read stops
the server from starting rather than being overwritten.

| Flag | Default | |
|---|---|---|
| `--data-dir` | | the folder with a folder per church (see above) |
| `--root`, `--inbox` | | one church without a church folder, instead of `--data-dir`: its date folders and the inbox where new recordings arrive. Settings are not saved. |
| `--listen` | `:8080` | address to listen on |
| `--cache-dir` | user cache folder | waveform data and listening previews |
| `--cache-days` | 30 | remove cached analyses not used for this long |
| `--archive` | | the default archive folder for runs (see below) |
| `--layout` | `2006/2006-01-02` | where new recordings go under a church's `Taler`, as a Go time layout |
| `--ffmpeg` | `ffmpeg` | path to ffmpeg |

Churches: the dropdown in the header switches church (it only selects, there is no access control yet;
a request names its church with `?tenant=<id>` or `X-Tenant`, and the first church is the default, so
existing scripts such as `PUT api/inbox/files/<name>` keep working). Earlier versions kept the churches in
`<data-dir>/tenants.json`; it is imported into the church folders once and renamed `tenants.json.migrated`.

The server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.

## Using it

- **Folders:** the date folders with what they contain (`raw`, `edit`, `✓ flac mp3 opus`).
- **Editor:** the whole service as a waveform with a minimap overview, like Audacity or
  ocenaudio. Drag to select the sermon and drag the edges to adjust; zoom with the slider,
  `+`/`-` or ctrl+scroll; silences are marked grey. Listen from anywhere, to the selection,
  or just around its start and end. The start/end fields follow the selection and can be typed
  in (`h:mm:ss`, `mm:ss` or seconds, with decimals if needed). Keys: `space` play, `s`/`e`
  start/end at the playhead, `[`/`]` previous/next silence, ←/→ 1 s (shift 0.1 s), `p` play
  the selection.
- **Options and run:** the options below, checked as you change them; your choices carry over
  to the next folder (except Force). The run is followed live and keeps going if the browser is
  closed. Cancelling leaves nothing half-written.
- **Files:** each date's page lists its finished files to play and download, and links to
  the sources (whole recording and cut).
- **Upload:** pick the recording and its date (suggested from the file name or the file's
  date); it goes straight into the date folder (`<year>/<date>/<date>_raw.*`, see `--layout`).
  Uploads are sent in chunks, so they pass proxies that limit request size (Cloudflare: 100 MB)
  and continue after a dropped connection. No inbox needed.
- **Several sermons a day:** give the second one a name (upload form, optional). Its folder is
  `<year>/<date> <name>` and its files `<date> <name>_raw.flac`, `<date> <name>.mp3`... Without a name
  a date can hold one sermon. **Rename…** on a sermon's page changes its date or name (the folder and
  the files named after it, also in the archive; files already made keep their tags). A recording
  from the inbox that finds its date taken lands in `<date> 2`; rename it. Names you use are
  remembered per church and suggested next time; **Settings → Sermon names** turns that off and
  lists them, so a one-off can be removed.
- **Inbox** (with `--inbox`): recordings that arrive by other means go into their date folder
  by themselves (see below); there is no inbox page. The folder list shows a "converting" or
  "preparing" tag while a recording is made ready.

**A WAV is converted to FLAC as soon as it arrives** (upload, inbox, or opening a folder that
has one), verified identical by MD5 of the audio, and then replaces the WAV. Right after, the
server prepares the waveform and the listening preview, so cutting can start at once.

### Tags

Every output (FLAC, MP3, Opus) gets title, artist, album and date tags. The **Settings** page sets
the defaults of the selected church, as templates: the title starts as `{yyyy}-{mm}-{dd}`. Placeholders:
`{yyyy}` `{yy}` `{mm}` `{dd}` `{date}` (2026-10-04) `{weekday}` and `{name}` (the sermon's name, empty
if it has none); an unknown one is refused. They are filled in from the date folder's name
(`2026-10-04` or `2026-10-04 Name`). The sermon's page shows the result and lets you change it for
one run; a field you empty is left out. Saved in the church's `church.json` (with
`--root` alone the settings last until restart).

### Inbox

- **The inbox is any folder**: a synced folder, a file share, or the API. A `.wav` or
  `.flac` that has stopped changing for 30 s is moved into its date folder as `<date>_raw.*`
  (and converted and prepared there), without anyone opening it. The date is taken from the
  file name, else the recording's tags, else its modification time. If that date already has a
  raw recording, the new one goes in `<date> 2`, `<date> 3`... Hidden files and `*.partial*`
  are ignored, so write under such a name and rename when done if you can.
- **`PUT api/inbox/files/<name>`** (optional `?modified=<ms>`) streams a recording into the
  inbox, for other services, e.g. a job forwarding uploads from a file-sharing app. It is moved
  into its date folder at once. A name still waiting in the inbox gets 409.

When a recording is opened the first time, the server prepares its waveform and a small MP3
preview for listening (about 35 s for 90 minutes), kept in `--cache-dir` and kept
when a recording is moved. The preview is
normalized (up to 40 dB of dynamic gain), so even a very quiet recording can be heard while
cutting; the waveform keeps the original levels, where loud music and quieter speech stand
apart. The browser only streams the preview; the cut is always made from the original.

**More than one segment** (for example a song in the middle of the sermon that should go):
cut it yourself in an audio editor and put the result in the date folder as `<date>_edit.flac`
(or `.wav`). The editor then offers to use it instead of a cut.

## What a run makes

```
2026-10-04/
  2026-10-04_raw.flac  the whole recording, untouched by runs
  2026-10-04.flac      master: mono, 48 kHz / 24-bit, -19 LUFS
  2026-10-04.mp3       mono, 96 kbps
  2026-10-04.opus      mono, 48 kbps
```

| Option | Default | |
|---|---|---|
| Outputs | FLAC, MP3, Opus | which files to make |
| Stereo | off (mono) | stereo moves the defaults to -16 LUFS, MP3 128k, Opus 96k |
| Fade in / out | 0 | seconds of fade at the cut points, 0 = off |
| Archive folder | `--archive` | keep the sources as FLAC there (default: leave them in place) |
| Keep a hand-cut edit WAV | off | when archiving, keep `<date>_edit.wav` next to its FLAC (raw WAVs always become FLAC) |
| Force | off | redo existing outputs (and cut again) |
| Noise reduction | 12 dB | 0 = off |
| Target loudness | -19 LUFS | |
| Max true peak | -1.5 dBTP | for the master |
| MP3 / Opus bitrate | 96k / 48k | |

- The fades are applied last, after the loudness processing, so they stay intact in every output.
- MP3 and Opus are encoded, measured and re-encoded with adjusted gain if true peak > -1 dBTP or
  the loudness is off. They are always made from the master; without FLAC it is a temporary
  hidden file in the date folder, removed when the run ends.
- Existing outputs are skipped without Force. Output is written to a `.partial` file and
  renamed when done, so an interrupted run never leaves a half-written file behind.
- Mono is the usual choice for speech, and mono and stereo recordings both work either way.
  With stereo, the run warns if the source is mono or its two channels are identical or nearly
  so (difference 30 dB or more below the signal, common when a mixer sends one signal to both
  sides): stereo output would sound the same as mono in bigger files. It also warns if one
  channel is silent or 20 dB or more quieter than the other: stereo output would have the
  sermon on one side only, while mono mixes both channels. It only warns.

### Archive

With an archive folder the sources are kept as FLAC there, and the date folder ends up with
just the finished files. A relative path is relative to the date folder; an absolute path
(e.g. on another disk) is used as is, and can be shared by all dates.

```
2026-10-04/
  2026-10-04.flac
  2026-10-04.mp3
  2026-10-04.opus
  arkiv/
    2026-10-04_raw.flac       the whole recording
    2026-10-04_edit.flac      the sermon as cut, the input to the master
```

- A WAV is converted to FLAC in the archive, the audio is verified with MD5, and **the WAV is
  deleted only if the audio is identical** (unless Keep WAV). A FLAC is moved; across disks it
  is copied, verified and only then deleted.
- A file already in the archive is never replaced without Force.
