# ffmpeg for the desktop app

The desktop app brings its own ffmpeg. It is built here, for each system the app runs on, by
`.github/workflows/ffmpeg.yml`:

- `versions`: the pinned sources (ffmpeg, LAME, Opus, zlib, each with its SHA-256) and `RELEASE`, the name of
  the GitHub release the builds are published as.
- `build.sh <target>`: builds a static ffmpeg and ffprobe (ffprobe for the tests only) into `out/`. Targets:
  `linux-amd64` and `linux-arm64` (in Alpine), `windows-amd64` (mingw-w64 on Linux), `darwin-arm64` and
  `darwin-amd64` (on a Mac of that kind).
- `check.sh <ffmpeg>`: LGPL (built without `--enable-gpl` and `--enable-nonfree`), with the encoders, filters
  and muxers the studio uses.

The workflow builds every target, checks it and runs all Go tests against it on its own system. On `main` it
publishes the release once, with the ffmpeg binaries, the exact sources, `LICENSES.txt` and `SHA256SUMS`. For a
new build (another version, other options), change `RELEASE` too.

ffmpeg is LGPL 2.1 or later, LAME LGPL, Opus BSD, zlib the zlib licence. They are separate programs and
libraries, not under the studio's Apache 2.0.

Locally, the Linux build in a container:

```bash
podman run --rm -v "$PWD/ffmpeg:/f:Z" -w /f alpine:3.22 sh -c \
  'apk add -q build-base nasm pkgconf curl xz perl coreutils && ./build.sh linux-amd64'
```
