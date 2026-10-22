#!/bin/sh
# Fetches our ffmpeg for one system from the release named in ffmpeg/versions, checks it against
# ffmpeg/release.sha256 (in git, so a changed release asset is refused), and prepares it for the desktop
# program: ffmpeg/out/embed/<target>/ffmpeg.gz and LICENSES.txt.
#
#   scripts/fetch-ffmpeg.sh <target>     linux-amd64 linux-arm64 windows-amd64 darwin-arm64 darwin-amd64
set -eu
cd "$(dirname "$0")/.."
. ffmpeg/versions
target=$1
ext=
[ "${target%%-*}" = windows ] && ext=.exe
cache=ffmpeg/out/release/$RELEASE
out=ffmpeg/out/embed/$target
mkdir -p "$cache" "$out"
sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}
for f in "ffmpeg-$target$ext" LICENSES.txt; do
	want=$(grep "  $f\$" ffmpeg/release.sha256 | cut -d' ' -f1)
	[ -n "$want" ] || { echo "$f is not in ffmpeg/release.sha256" >&2; exit 1; }
	if [ ! -f "$cache/$f" ] || [ "$(sha256 "$cache/$f")" != "$want" ]; then
		curl -fsSL --retry 3 -o "$cache/$f.part" "https://github.com/gunnaringe/sermon-studio/releases/download/$RELEASE/$f"
		mv "$cache/$f.part" "$cache/$f"
	fi
	[ "$(sha256 "$cache/$f")" = "$want" ] || { echo "$f from $RELEASE does not match ffmpeg/release.sha256" >&2; rm -f "$cache/$f"; exit 1; }
done
gzip -9 -n -c "$cache/ffmpeg-$target$ext" > "$out/ffmpeg.gz"
cp "$cache/LICENSES.txt" "$out/"
