# The desktop app

Sermon Studio on one computer: double-click it, and the studio opens in a window of its own. No server, no
login, no Docker. The studio is the same as on a server; only the way in differs.

## Starting it

Start `sermon-studio-desktop` (on Windows `sermon-studio-desktop-windows-amd64.exe`). It runs the studio on
this computer only (`http://127.0.0.1:41917/`) and opens it in Edge, Chrome or Chromium as an app: a window
without tabs or address bar. Without one of those it opens in the default browser.

- **Starting it again** opens another window to the one already running.
- **It stops by itself** five minutes after the last window closed, when nothing is running (a save, a
  conversion, an upload). Close the window while a sermon is being made, and it is finished first.
- **The first time**, it makes a church called "My church" and opens **Settings**, to give it its name and logo.

For now ffmpeg is not built in: put `ffmpeg` (`ffmpeg.exe`) next to the program, or install it
(Windows: `winget install ffmpeg`, macOS: `brew install ffmpeg`, Linux: `sudo apt install ffmpeg`).

## Where things are

| | Windows | macOS | Linux |
|---|---|---|---|
| Data: the churches, recordings and sermons | Documents\Sermon Studio | ~/Documents/Sermon Studio | ~/Sermon Studio |
| Settings of this computer (`settings.json`) | %AppData%\Sermon Studio | ~/Library/Application Support/Sermon Studio | ~/.config/Sermon Studio |
| Cache (waveforms, previews) and the log (`sermon-studio.log`) | %LocalAppData%\Sermon Studio | ~/Library/Caches/Sermon Studio | ~/.cache/Sermon Studio |

The data folder is laid out as on a server (see [running.md](running.md)): a folder per church, with its
sermons in `Taler` and an inbox, `Innboks`. A recording put in the inbox is taken in as on a server.

`settings.json` has the port, the data folder and a token (which lets a second start know the running copy is
this user's). Change the data folder there, or start once with `--data-dir <folder>` (not kept).

## Options

| Flag | |
|---|---|
| `--data-dir` | the data folder, instead of the one in the settings (not kept) |
| `--idle-after` | how long without a window before it stops (default `5m`) |
| `--no-window` | do not open a window |
| `--version` | print the version |

## Safety

The studio has no login of its own, so the desktop app answers only on `127.0.0.1`, and only to requests for
`127.0.0.1:<port>` or `localhost:<port>`: a web page cannot reach it by pointing a name of its own at this
computer. Posts from other sites are refused by the studio, as on a server.

## Building it

```bash
scripts/build-desktop.sh [version]   # dist/sermon-studio-desktop-<os>-<arch>, for every system
```
