# Sermon Studio

**Turn a recording of a church service into publish-ready sermon files.**
Cut the sermon out on a waveform. Get evenly loud FLAC, MP3 and Opus, ready for podcast services.

[![Build](https://github.com/gunnaringe/sermon-studio/actions/workflows/image.yml/badge.svg)](https://github.com/gunnaringe/sermon-studio/actions/workflows/image.yml)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![Go, the standard library, x/sys, OpenID Connect and kong](https://img.shields.io/badge/go-stdlib%20%2B%20x%2Fsys%20%2B%20OIDC%20%2B%20kong-00ADD8)
![English, Norwegian bokmål and nynorsk](https://img.shields.io/badge/UI-English%20%C2%B7%20bokm%C3%A5l%20%C2%B7%20nynorsk-lightgrey)

![The editor: a 14-minute speech as a waveform, two sections kept, a cough cut out](docs/img/hero.png)

## The problem

A church service is recorded in one piece: welcome, songs, prayers, the sermon, more songs. What
you want to publish is the sermon, at a volume people can listen to in the car, in a format every
podcast service accepts. Doing that by hand means an audio editor, a loudness meter and a handful of
ffmpeg commands, every week.

Sermon Studio does it in the browser. Drop in the recording, mark what to keep, press Save.

## What it does

**Cut on a waveform.** See the whole service, jump between silences, drag out the parts to keep.
Keep one section or several, cut out a cough or a song in the middle, and label them. Cut while you
listen: press once where a part starts and again where it ends. Hear the join before you commit, and
undo any step. Nothing is ever cut from the original.

![Sections and cuts, with labels and exact times](docs/img/05-sections-list.png)

**Even, standard loudness.** Speech and music are recorded at very different levels. The output is
mixed to mono (or kept stereo), noise-reduced, and normalised to podcast loudness (-19 LUFS, measured
with EBU R128, true peak limited), then encoded to **FLAC, MP3 and Opus**. A church's own intro and
outro (a jingle, a closing word) go around the sermon at the same loudness, and the sermon alone is
there to download too.

**Recordings arrive however they arrive.** Upload from the browser (large files in chunks, resumable),
let a recorder drop files in a church's inbox folder, or hand a sound technician a **share code** for a
public upload page that needs no login. WAV, AIFF, CAF, FLAC, MP3, M4A, Ogg, Opus, WMA and video files
(the sound is copied out unchanged) all work. A finished sermon goes out the same way: a code that lets
someone download it, as often and for as long as you choose.

![The recordings page: years, status, labels, search](docs/img/01-recordings.png)

![The public upload page, used with a share code and no login](docs/img/16-public-uploaded.png)

**Safe by design.** The original recording is kept, whole, next to the results. A file is deleted
only after its copy is verified identical. Outputs are written to a temporary name and renamed when
complete, so a crash never leaves a half-written file. Everything about a recording lives in one
`recording.json` next to its files.

**Made for several churches and for phones.** One folder per church, each with its own default tags,
names, logo and users. The interface is in English, Norwegian bokmål and nynorsk, in a light or dark
theme, and the editor works with a finger [on a phone](docs/img/18-phone-editor.png).

![The editor in the dark theme](docs/img/19-dark-editor.png)

## How it works

1. **A recording arrives:** by upload, the inbox folder or a share code.
2. **It is made ready:** WAV and AIFF become FLAC, a video becomes its sound, each checked identical
   to what came; other files are used as they are.
3. **You cut it on the waveform:** sections, cuts, labels, fades.
4. **Save makes the files:** mono or stereo, noise reduction, even loudness, as FLAC, MP3 and Opus.
   The original is kept.

A recording is a folder named after its date:

```
2026-10-18/
  2026-10-18.flac   2026-10-18.mp3   2026-10-18.opus     the finished files
  recording.json                                           what it is, the cuts, how each file was made
  original/2026-10-18_raw.flac                             the whole recording, untouched
```

## Under the hood

One Go binary with **few dependencies** (`golang.org/x/sys` for Windows; `coreos/go-oidc` and `golang.org/x/oauth2` for the login; `alecthomas/kong` for the command line) and ffmpeg run as a command. The frontend is plain
JavaScript with [wavesurfer.js](https://wavesurfer.xyz/) for the waveform: no framework and no build
step. State is plain files in the data folder, so there is no database to run or back up. The
studio logs users in with any OpenID Connect provider and keeps who may use which church itself (members join with an
invite link); without that, put it behind your login proxy, or on a trusted network. The
public share page is a separate listener that reaches nothing but itself and the files a download code gives. See
[SECURITY.md](SECURITY.md).

## Documentation

| | |
|---|---|
| [User guide](docs/guide.md) | Uploading, cutting, saving, sharing: with pictures |
| [Running it](docs/running.md) | Docker, the data folder, all flags |
| [The desktop app](docs/desktop.md) | Sermon Studio on one computer, without a server |
| [Access control](docs/access.md) | Which user sees which church |
| [What a run makes](docs/outputs.md) | The files, loudness targets and options |
| [Design](docs/design.md) | Background and decisions |
| [Contributing](CONTRIBUTING.md) | Build, test and pull requests |

## Run it yourself

You need Docker (or Go 1.25 and ffmpeg). The image includes ffmpeg.

```bash
git clone https://github.com/gunnaringe/sermon-studio && cd sermon-studio
docker build -t sermon-studio .
mkdir -p data/my-church/Taler data/my-church/Innboks cache
docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" \
  -v "$PWD/data:/data" -v "$PWD/cache:/cache" \
  sermon-studio --no-login --data-dir /data --cache-dir /cache
```

Open <http://localhost:8080>. `--no-login` means anyone who reaches it can use it: fine on your own machine;
for a server, use the studio's own login ([access](docs/access.md)). Each folder in `data/` is a church; add more from the app. All options
and the public upload page are in [docs/running.md](docs/running.md).

On one computer, without a server: download the desktop app for Windows, macOS or Linux from the
[releases](https://github.com/gunnaringe/sermon-studio/releases/latest) ([docs/desktop.md](docs/desktop.md)).

## License

[Apache 2.0](LICENSE), except the signature font on the About page (see [NOTICE](NOTICE)). The documentation's example recording is President Kennedy's inaugural address
(1961), a US government work in the public domain
([source](https://commons.wikimedia.org/wiki/File:JFK_inaugural_address.ogg)).
