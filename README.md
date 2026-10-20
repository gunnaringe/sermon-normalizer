# sermon-studio

<img src="web/static/icons/logo.svg" alt="Sermon Studio logo" width="96">

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
church in the app ("+ Add church…") or just make the folder. The church is the first part of every address
(`#/<church>` the recordings, `#/<church>/folder/<path>` the editor, `#/<church>/settings` the settings), so a link opens
that church whatever the browser used last. The ids `folder`, `folders`, `recording`, `recordings`, `settings`, `upload` and
`shares` are reserved for that. A `church.json` that cannot be read stops
the server from starting rather than being overwritten.

**Logo:** *Settings → Church → Upload logo…* (PNG, JPEG, WebP or SVG, up to 1 MB) puts the church's own
logo in the header instead of the app's; it is kept as `logo.<ext>` in the church folder. A user with one
church and no right to add more sees just its name in the header; otherwise a quiet dropdown switches church.

| Flag | Default | |
|---|---|---|
| `--data-dir` | | the folder with a folder per church (see above) |
| `--root`, `--inbox` | | one church without a church folder, instead of `--data-dir`: its date folders and the inbox where new recordings arrive. Settings are not saved. |
| `--groups-header` | | request header in which the login proxy sends the user's groups; turns on access control (below) |
| `--logout-url` | | the login proxy's logout address; shows a Log out button in the header (posts to it) |
| `--listen` | `:8080` | address to listen on |
| `--cache-dir` | user cache folder | waveform data and listening previews |
| `--cache-days` | 30 | remove cached analyses not used for this long |
| `--layout` | `2006/2006-01-02` | where new recordings go under a church's `Taler`, as a Go time layout |
| `--ffmpeg` | `ffmpeg` | path to ffmpeg |
| `--share-listen` | | address of the public upload page (see [Shares](#shares)); unset = no shares |
| `--share-url` | | the upload page's public address, so the studio can show share links |
| `--share-ip-header` | | header with the client's address, for the rate limits (Cloudflare: `CF-Connecting-IP`) |
| `--share-fails-per-ip`, `--share-fails-total` | 5, 30 | wrong codes in 10 minutes before an address is blocked for an hour / codes are refused for everyone for 10 minutes |
| `--share-requests-per-ip`, `--share-requests-total` | 120, 1200 | requests per minute to the upload page |
| `--share-uploads-per-ip`, `--share-uploads-total` | 3, 10 | chunks received at the same time |

Churches: the dropdown in the header switches church. A request names its church with
`?tenant=<id>` or `X-Tenant`; without one it is the first church the user sees.

### Access (which churches a user sees)

Without `--groups-header` there is no access control: everyone sees every church and can add
churches. With `--groups-header X-Forwarded-Groups` the app reads the user's groups (space-separated)
from that header, which the login proxy must set, and nothing else:

- a church `<id>` is for the members of the group **`sermon_studio_<id>`**, with underscores for the hyphens in the id
  (`norkirken-melhus` → `sermon_studio_norkirken_melhus`, like Pocket ID names groups); two churches
  that would get the same group (`a-b`, `a_b`) are refused
- members of **`sermon_studio_admin`** see every church and are the only ones who can add churches
  (the app tells them the group to create)
- users in no matching group get a 403 page; a church they do not see answers as if it did not exist
- **`admin`** (and `default`) cannot be a church id, so no church's group can ever be the
  administrators' group: the name is refused when adding, and a church folder with that name stops
  the server from starting

The header is only trustworthy when every request comes through the proxy and the proxy replaces it
(Traefik removes the headers named in `authResponseHeaders` before copying the auth response's).
Clients that reach the app directly have no groups and get nothing.

The server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.

## Using it

- **Recordings:** the date folders with what they contain (`raw`, `✓ flac mp3 opus`).
- **Editor:** the whole service as a waveform with a minimap overview, like Audacity or
  ocenaudio. The controls are in three groups. **Navigate:** play, jump between silences
  (grey on the waveform), zoom (slider, `+`/`-`, or scroll over the waveform: zooms at the
  mouse; shift+scroll moves along; *Settings › Waveform* can make it need ctrl/cmd). **Sermon:** drag on the waveform
  to select the sermon and drag the edges to adjust, or set the start and end at the playhead;
  *Hear start*, *Hear end* and *Play sermon* check the cut. The from/to fields follow the
  selection and can be typed in (`h:mm:ss`, `mm:ss` or seconds, with decimals if needed).
  The sermon can be **several sections** (below). **Cut out:** parts of the sermon that should
  go, such as a song in the middle (below).
  Keys: `space` play, `s`/`e` start/end at the playhead (of the section there, or the next/previous one), `c` cut out here, `[`/`]` previous/next
  silence, ←/→ 1 s (shift 0.1 s), `p` play the sermon.
- **On a phone:** every page fits the screen. Drag a finger along the waveform to select (up and
  down still scrolls the page); the section edges are wider to grab; cut with *Cut out here* and
  zoom with the buttons.
- **The page** has four parts: **Input file** (the recording: name, format, how it came, its
  metadata, whether it is stereo or mono in effect, and *Download original*), **Edit** (the
  waveform, sections, cuts and fades), **Options** (mono ↔ stereo, tags, sound) and **Output**.
- **Saved version:** the sections, the cuts and the fades are kept in `recording.json` in the date
  folder (it follows a rename) as you change them: these are the *adjustments*, and they are back
  the next time the sermon is opened. **Save…** (after a confirmation) makes them the *saved
  version*, which stays as it is while you adjust further; **Revert…** (after a confirmation)
  throws the adjustments away and goes back to it. Both versions are in the file. **Regenerate**
  saves what is on the page first.
- **Several sections:** drag on an empty part of the waveform to add another section; the parts
  between sections are left out, as if cut. Use whichever feels natural: one section with cuts,
  or several sections (or both). Sections that come to overlap become one; *Remove* drops one.
- **Cutting parts out:** with the sermon selected, *Cut out here* (or `c`) marks 10 s from the
  playhead as a cut, shown in red; or hold shift and drag on the waveform. Adjust a cut by
  dragging its edges, typing its times, or *Set start* / *Set end* at the playhead. A cut must
  be between the start of the first section and the end of the last. *Hear join* plays 3 s either side of a cut as it will sound, and with
  *Play without the cuts* (on by default) playback jumps over the cuts and the gaps between sections. The outputs are made from
  what is left, joined with a 15 ms fade on each side so the join does not click; the length
  shown is that of the sections after the cuts.
- **Options:** checked as you change them; your choices carry over to the next folder. Mono or
  stereo is a switch; choosing stereo for a recording that is mono in effect (one channel,
  identical channels, or one side silent) shows a warning.
- **Output:** a pill per format (FLAC, MP3, Opus), one under the other: click to choose it,
  green when the file is there (play and download it from the recordings list). `recording.json` keeps what each file was made from (sections, cuts,
  fades, sound, tags, bitrate), so a file made from other settings than the page shows says
  *will regenerate*; files made from the recordings list count too. The choice is the recording's own (not
  carried over): it starts with the formats it has, or the defaults when it has none; a file whose format is no longer chosen says *will be deleted*.
  **Regenerate** saves the edit, makes the chosen formats that are missing or out of date and
  deletes the files of those not chosen (never the recording), asking first when that replaces or
  deletes a file. The run is followed live and keeps going if the browser is closed. Cancelling leaves
  nothing half-written.
- **Newly imported:** a recording that arrived (upload, inbox, share) in the last 7 days is also listed
  on top of the first page, with when it came; the import time is kept in its `recording.json`.
- **Upload** (at the top of the recordings list): drop or pick the recording and its date (suggested from the file name or the file's
  date); it goes straight into the date folder (`<year>/<date>/<date>_raw.*`, see `--layout`).
  Uploads are sent in chunks, so they pass proxies that limit request size (Cloudflare: 100 MB)
  and continue after a dropped connection. No inbox needed.
- **Several sermons a day:** give the second one a name (upload form, optional). Its folder is
  `<year>/<date> <name>` and its files `<date> <name>_raw.flac`, `<date> <name>.mp3`... Without a name
  a date can hold one sermon. **Rename…** on a sermon's page changes its date or name (the folder and
  the files named after it, also in `original/`; files already made keep their tags). A recording
  from the inbox that finds its date taken lands in `<date> 2`; rename it. Names you use are
  remembered per church and suggested next time; **Settings → Sermon names** turns that off and
  lists them, so a one-off can be removed.
- **Inbox** (with `--inbox`): recordings that arrive by other means go into their date folder
  by themselves (see below); there is no inbox page. The recordings list shows a "converting" or
  "preparing" tag while a recording is made ready.

**A WAV is converted to FLAC as soon as it arrives** (upload, inbox, or opening a folder that
has one), verified identical by MD5 of the audio, and then replaces the WAV. Right after, the
server prepares the waveform and the listening preview, so cutting can start at once.

### Shares

With `--share-listen`, people without a login can send recordings to a church's inbox on a
separate, public upload page, with a code. **Settings → Shares** in the studio makes one: a name, the file
types (`.wav`, `.flac`), the largest file, and optionally how many files, how much in all and an
expiry. The code (`XXXX-XXXX-XXXX-XXXX`, 80 bits) and a link with it (`<share-url>#code=...`) are
shown once; the church keeps only a hash, in `<church>/shares.json`, with the uploads made (name,
size, address, time). A share can be revoked (refused at once, also mid-upload) or deleted.

The upload page asks for the code, or takes it from the link and removes it from the address bar
(it is in the fragment, so it never reaches the server's or a proxy's logs). Files go in 16 MB
chunks, so they pass Cloudflare, and continue after a dropped connection, a reload or a restart.
They are written as `Innboks/.share-<id>.partial`, checked to be the type they claim, and renamed
when complete (never over a file: `name (2).wav`); then they are ingested like any inbox file.

Run the upload page on its own host, **not** behind the login proxy, and keep the studio behind
it. The page serves only itself and `api/share/*`: nothing of the studio, nothing to list or
download. Every request carries the code; wrong codes and requests are limited per address and in
all (flags above), and an upload is refused when the disk would get within 1 GB of full.

### Tags

Every output (FLAC, MP3, Opus) gets title, artist, album and date tags. The **Settings** overlay (the gear in the header; closes with ✕, Esc or a click outside) sets
the defaults of the selected church, as templates: the title starts as `{yyyy}-{mm}-{dd}`. Placeholders:
`{yyyy}` `{yy}` `{mm}` `{dd}` `{date}` (2026-10-04) `{weekday}` and `{name}` (the sermon's name, empty
if it has none); an unknown one is refused. They are filled in from the date folder's name
(`2026-10-04` or `2026-10-04 Name`). The sermon's page shows the result and lets you change it for
one run; a field you empty is left out. Saved in the church's `church.json` (with
`--root` alone the settings last until restart).

### Inbox

- **The inbox is any folder**: a synced folder or a file share. A `.wav` or
  `.flac` that has stopped changing for 30 s is moved into its date folder as `<date>_raw.*`
  (and converted and prepared there), without anyone opening it. The date is taken from the
  file name, else the recording's tags, else its modification time. If that date already has a
  raw recording, the new one goes in `<date> 2`, `<date> 3`... Hidden files and `*.partial*`
  are ignored, so write under such a name and rename when done if you can.

When a recording is opened the first time, the server prepares its waveform and a small MP3
preview for listening (about 35 s for 90 minutes), kept in `--cache-dir` and kept
when a recording is moved. The preview is
normalized (up to 40 dB of dynamic gain), so even a very quiet recording can be heard while
cutting; the waveform keeps the original levels, where loud music and quieter speech stand
apart. The browser only streams the preview; the cut is always made from the original.

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
| Noise reduction | 12 dB | 0 = off |
| Target loudness | -19 LUFS | |
| Max true peak | -1.5 dBTP | for the master |
| MP3 / Opus bitrate | 96k / 48k | |

- The fades are applied last, after the loudness processing, so they stay intact in every output.
- MP3 and Opus are encoded, measured and re-encoded with adjusted gain if true peak > -1 dBTP or
  the loudness is off. They are always made from the master; without FLAC it is a temporary
  hidden file in the date folder, removed when the run ends.
- Up-to-date outputs are kept; Regenerate redoes those that are out of date (and cuts again). Output is written to a `.partial` file and
  renamed when done, so an interrupted run never leaves a half-written file behind.
- Mono is the usual choice for speech, and mono and stereo recordings both work either way.
  With stereo, the run warns if the source is mono or its two channels are identical or nearly
  so (difference 30 dB or more below the signal, common when a mixer sends one signal to both
  sides): stereo output would sound the same as mono in bigger files. It also warns if one
  channel is silent or 20 dB or more quieter than the other: stereo output would have the
  sermon on one side only, while mono mixes both channels. It only warns.

### The original recording

A run moves the whole recording into `original/` in the date folder, as FLAC, so the date folder
ends up with the finished files, the saved cuts and that one folder. No cut file is made: the
outputs are made straight from the original, using the sections and cuts (`recording.json`),
so there is nothing else to keep in step.

```
2026-10-04/
  2026-10-04.flac
  2026-10-04.mp3
  2026-10-04.opus
  recording.json                what the recording is, the saved and adjusted sections, cuts
                                and fades, and how each file was made
  original/
    2026-10-04_raw.flac         the whole recording
```

- A WAV is converted to FLAC in `original/`, the audio is verified with MD5, and **the WAV is
  deleted only if the audio is identical**. A FLAC is moved; across disks it is copied, verified
  and only then deleted.
- A recording already in `original/` is never replaced by a run that only adds files; one that redoes files replaces it.
