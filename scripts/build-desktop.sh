#!/bin/sh
# Builds the desktop program for every system into dist/, with our ffmpeg built in (scripts/fetch-ffmpeg.sh,
# from the release in packaging/ffmpeg-release). Host Go, or a container without one.
#
#   scripts/build-desktop.sh [version]
#
# On Windows it is a GUI program (-H windowsgui): no console window opens.
set -eu
cd "$(dirname "$0")/.."
version=${1:-dev}
targets="windows-amd64 darwin-arm64 darwin-amd64 linux-amd64 linux-arm64"
for t in $targets; do
	scripts/fetch-ffmpeg.sh "$t"
done
build='
embed=cmd/sermon-studio-desktop/internal/desktop/ffmpeg
trap "rm -f $embed/ffmpeg.gz $embed/LICENSES.txt" EXIT
for t in '"$targets"'; do
	os=${t%-*} arch=${t#*-} ext= flags=
	[ "$os" = windows ] && ext=.exe flags=-H=windowsgui
	cp "dist/ffmpeg/embed/$t/ffmpeg.gz" "dist/ffmpeg/embed/$t/LICENSES.txt" "$embed/"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -tags embedffmpeg -trimpath -ldflags="-s -w $flags -X main.version=$1" \
		-o "dist/sermon-studio-desktop-$t$ext" ./cmd/sermon-studio-desktop
	echo "dist/sermon-studio-desktop-$t$ext"
done'
mkdir -p dist
if command -v go >/dev/null; then
	sh -c "$build" sh "$version"
else
	run=$(command -v podman || command -v docker) || { echo "needs Go, podman or docker" >&2; exit 1; }
	"$run" run --rm -v "$PWD":/src:Z -w /src -v gomod:/go/pkg/mod -v gocache:/root/.cache/go-build \
		docker.io/library/golang:1.25-alpine3.22 sh -c "$build" sh "$version"
fi
