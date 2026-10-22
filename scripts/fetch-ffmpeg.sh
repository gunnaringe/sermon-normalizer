#!/bin/sh
# Fetches the ffmpeg for one system from the release in packaging/ffmpeg-release (gunnaringe/ffmpeg-lgpl),
# checks it against packaging/ffmpeg.sha256 (in git, so a changed release asset is refused), and prepares it
# for the desktop program: dist/ffmpeg/embed/<target>/ffmpeg.gz and LICENSES.txt.
#
#   scripts/fetch-ffmpeg.sh <target>     linux-amd64 linux-arm64 windows-amd64 darwin-arm64 darwin-amd64
set -eu
cd "$(dirname "$0")/.."
. packaging/ffmpeg-release
target=$1
ext=
[ "${target%%-*}" = windows ] && ext=.exe
cache=dist/ffmpeg/$FFMPEG_RELEASE
out=dist/ffmpeg/embed/$target
mkdir -p "$cache" "$out"
sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}
for f in "ffmpeg-$target$ext" LICENSES.txt; do
	want=$(grep "  $f\$" packaging/ffmpeg.sha256 | cut -d' ' -f1)
	[ -n "$want" ] || { echo "$f is not in packaging/ffmpeg.sha256" >&2; exit 1; }
	if [ ! -f "$cache/$f" ] || [ "$(sha256 "$cache/$f")" != "$want" ]; then
		curl -fsSL --retry 3 -o "$cache/$f.part" "https://github.com/$FFMPEG_REPO/releases/download/$FFMPEG_RELEASE/$f"
		mv "$cache/$f.part" "$cache/$f"
	fi
	[ "$(sha256 "$cache/$f")" = "$want" ] || { echo "$f from $FFMPEG_RELEASE does not match packaging/ffmpeg.sha256" >&2; rm -f "$cache/$f"; exit 1; }
done
gzip -9 -n -c "$cache/ffmpeg-$target$ext" > "$out/ffmpeg.gz"
cp "$cache/LICENSES.txt" "$out/"
