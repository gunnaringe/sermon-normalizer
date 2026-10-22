#!/bin/sh
# Packages what scripts/build-desktop.sh built into dist/release/, for a release:
#
#   scripts/package-desktop.sh linux <version>   Linux tar.gz (program, .desktop file, icon, install.sh) and
#                                                the Windows program, plus LICENSES-ffmpeg.txt
#   scripts/package-desktop.sh macos <version>   Sermon Studio.app per Mac, zipped (on a Mac: sips, iconutil,
#                                                codesign, ditto)
set -eu
cd "$(dirname "$0")/.."
what=$1 v=$2
out=dist/release
mkdir -p "$out"
case $what in
linux)
	for arch in amd64 arm64; do
		name=sermon-studio-$v-linux-$arch
		d=dist/pkg/$name
		rm -rf "$d" && mkdir -p "$d"
		cp "dist/sermon-studio-desktop-linux-$arch" "$d/sermon-studio-desktop"
		cp packaging/sermon-studio.desktop packaging/install.sh "$d/"
		cp web/static/icons/icon-512.png "$d/sermon-studio.png"
		cp "dist/ffmpeg/embed/linux-$arch/LICENSES.txt" "$d/LICENSES-ffmpeg.txt"
		chmod 755 "$d/sermon-studio-desktop" "$d/install.sh"
		tar -C dist/pkg -czf "$out/$name.tar.gz" "$name"
	done
	cp dist/sermon-studio-desktop-windows-amd64.exe "$out/Sermon-Studio-$v-windows-amd64.exe"
	cp dist/ffmpeg/embed/linux-amd64/LICENSES.txt "$out/LICENSES-ffmpeg.txt"
	;;
macos)
	iconset=dist/pkg/sermon-studio.iconset
	rm -rf "$iconset" && mkdir -p "$iconset"
	for s in 16 32 128 256 512; do
		sips -z $s $s web/static/icons/icon-512.png --out "$iconset/icon_${s}x${s}.png" >/dev/null
		[ $s = 512 ] || sips -z $((s * 2)) $((s * 2)) web/static/icons/icon-512.png --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
	done
	iconutil -c icns -o dist/pkg/sermon-studio.icns "$iconset"
	for arch in arm64 amd64; do
		app="dist/pkg/macos-$arch/Sermon Studio.app"
		rm -rf "$app" && mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
		cp "dist/sermon-studio-desktop-darwin-$arch" "$app/Contents/MacOS/sermon-studio-desktop"
		chmod 755 "$app/Contents/MacOS/sermon-studio-desktop"
		cp dist/pkg/sermon-studio.icns "$app/Contents/Resources/"
		sed "s/VERSION/$v/g" packaging/Info.plist > "$app/Contents/Info.plist"
		codesign --force --sign - "$app" # ad hoc: not from a known developer, but whole
		ditto -c -k --keepParent "$app" "$out/Sermon-Studio-$v-macos-$arch.zip"
	done
	;;
*) echo "linux or macos" >&2; exit 2 ;;
esac
ls -l "$out"
