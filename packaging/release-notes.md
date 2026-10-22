Sermon Studio on one computer: download the file for your system, start it, and the studio opens in a window of
its own. ffmpeg is built in. See [the desktop app](https://github.com/gunnaringe/sermon-studio/blob/main/docs/desktop.md).

| System | File |
|---|---|
| Windows | `Sermon-Studio-{version}-windows-amd64.exe` |
| Mac with Apple silicon (M1 and later) | `Sermon-Studio-{version}-macos-arm64.zip` |
| Mac with Intel | `Sermon-Studio-{version}-macos-amd64.zip` |
| Linux | `sermon-studio-{version}-linux-amd64.tar.gz` (or `arm64`): unpack, run `./install.sh` |

The programs are not signed yet:

- **Windows** asks on the first start ("Windows protected your PC"): **More info → Run anyway**.
- **macOS**: unzip and move **Sermon Studio** to Applications. On the first start macOS refuses it; open
  **System Settings → Privacy & Security** and press **Open Anyway**.

On a server, use the Docker image `ghcr.io/gunnaringe/sermon-studio:{version}` instead.

ffmpeg is LGPL: its licences are in `LICENSES-ffmpeg.txt`, its exact sources in the release
[ffmpeg-lgpl {ffmpeg}](https://github.com/gunnaringe/ffmpeg-lgpl/releases/tag/{ffmpeg}). `SHA256SUMS` has
the checksums.
