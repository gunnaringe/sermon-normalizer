#!/bin/sh
# Builds a small LGPL ffmpeg (and ffprobe, for the tests) for one system, with what the studio uses: FLAC,
# libmp3lame and libopus to encode, ffmpeg's own decoders, demuxers, muxers and filters (afftdn, ebur128,
# alimiter ...), zlib. No --enable-gpl or --enable-nonfree, so it is LGPL; lame is LGPL, opus BSD, zlib zlib.
#
#   ffmpeg/build.sh <target>    linux-amd64 linux-arm64 (in Alpine, static), windows-amd64 (mingw-w64 on
#                               Linux), darwin-arm64 darwin-amd64 (on a Mac of that kind)
#
# Writes out/ffmpeg-<target>[.exe] and out/ffprobe-<target>[.exe]; the sources are kept in out/sources/.
set -eu
cd "$(dirname "$0")"
target=$1
. ./versions
here=$PWD
out=$here/out
work=$here/work/$target
prefix=$work/prefix
mkdir -p "$out/sources" "$work" "$prefix"
jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu)

fetch() { # fetch <url> <sha256>: into out/sources, checked
	file=$out/sources/$(basename "$1")
	if [ ! -f "$file" ]; then
		curl -fsSL --retry 3 -o "$file.part" "$1"
		mv "$file.part" "$file"
	fi
	echo "$2  $file" | { sha256sum -c - 2>/dev/null || shasum -a 256 -c -; } >/dev/null || { echo "bad checksum: $file" >&2; rm -f "$file"; exit 1; }
	tar -xf "$file" -C "$work"
}
fetch "https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz" "$FFMPEG_SHA256"
fetch "https://downloads.sourceforge.net/project/lame/lame/$LAME_VERSION/lame-$LAME_VERSION.tar.gz" "$LAME_SHA256"
fetch "https://downloads.xiph.org/releases/opus/opus-$OPUS_VERSION.tar.gz" "$OPUS_SHA256"
fetch "https://zlib.net/fossils/zlib-$ZLIB_VERSION.tar.gz" "$ZLIB_SHA256"

host= ext= cross= extra_ldflags= extra_cflags=
case $target in
linux-*) extra_ldflags=-static ;;
windows-amd64)
	host=x86_64-w64-mingw32 ext=.exe
	cross="--enable-cross-compile --target-os=mingw32 --arch=x86_64 --cross-prefix=$host-"
	extra_ldflags="-static -static-libgcc" ;;
darwin-*)
	extra_cflags=-mmacosx-version-min=11.0
	extra_ldflags=-mmacosx-version-min=11.0 ;;
*) echo "unknown target $target" >&2; exit 2 ;;
esac
export CFLAGS="-O2 $extra_cflags" LDFLAGS="$extra_ldflags" PKG_CONFIG_PATH="$prefix/lib/pkgconfig"
hostflag=${host:+--host=$host}

# lame's config.guess is from 2017 and does not know every system (an arm64 Mac): opus' newer ones are used.
cp "$work/opus-$OPUS_VERSION/config.guess" "$work/opus-$OPUS_VERSION/config.sub" "$work/lame-$LAME_VERSION/"

# zlib: PNG cover pictures, compressed MP4 and Matroska headers.
(cd "$work/zlib-$ZLIB_VERSION" &&
	CROSS_PREFIX=${host:+$host-} ./configure --static --prefix="$prefix" &&
	make -s -j"$jobs" libz.a && make -s install) >/dev/null
(cd "$work/lame-$LAME_VERSION" &&
	./configure -q $hostflag --prefix="$prefix" --disable-shared --enable-static --disable-frontend --disable-decoder &&
	make -s -j"$jobs" && make -s install) >/dev/null
(cd "$work/opus-$OPUS_VERSION" &&
	./configure -q $hostflag --prefix="$prefix" --disable-shared --enable-static --disable-doc --disable-extra-programs &&
	make -s -j"$jobs" && make -s install) >/dev/null

cd "$work/ffmpeg-$FFMPEG_VERSION"
# shellcheck disable=SC2086
./configure --prefix="$prefix" $cross --pkg-config=pkg-config --pkg-config-flags=--static \
	--extra-cflags="-I$prefix/include $extra_cflags" --extra-ldflags="-L$prefix/lib $extra_ldflags" \
	--disable-debug --disable-doc --disable-ffplay --disable-network --disable-autodetect \
	--disable-indevs --enable-indev=lavfi --disable-outdevs \
	--enable-zlib --enable-libmp3lame --enable-libopus --enable-static --disable-shared >/dev/null ||
	{ tail -40 ffbuild/config.log >&2; exit 1; }
make -s -j"$jobs" "ffmpeg$ext" "ffprobe$ext" >/dev/null
cp "ffmpeg$ext" "$out/ffmpeg-$target$ext"
cp "ffprobe$ext" "$out/ffprobe-$target$ext"
strip "$out/ffmpeg-$target$ext" "$out/ffprobe-$target$ext" 2>/dev/null || "${host:+$host-}strip" "$out/ffmpeg-$target$ext" "$out/ffprobe-$target$ext"
ls -l "$out/ffmpeg-$target$ext" "$out/ffprobe-$target$ext"
