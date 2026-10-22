#!/bin/sh
# Builds the desktop program for every system into dist/ (host Go, or a container without one):
#
#   scripts/build-desktop.sh [version]
#
# On Windows it is a GUI program (-H windowsgui): no console window opens. ffmpeg is not in it yet; it looks
# for ffmpeg next to itself, then in PATH.
set -eu
cd "$(dirname "$0")/.."
version=${1:-dev}
build='
for target in windows/amd64 darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
	os=${target%/*} arch=${target#*/} ext= flags=
	[ "$os" = windows ] && ext=.exe flags=-H=windowsgui
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w $flags -X main.version=$1" \
		-o "dist/sermon-studio-desktop-$os-$arch$ext" ./cmd/sermon-studio-desktop
	echo "dist/sermon-studio-desktop-$os-$arch$ext"
done'
mkdir -p dist
if command -v go >/dev/null; then
	sh -c "$build" sh "$version"
else
	run=$(command -v podman || command -v docker) || { echo "needs Go, podman or docker" >&2; exit 1; }
	"$run" run --rm -v "$PWD":/src:Z -w /src -v gomod:/go/pkg/mod -v gocache:/root/.cache/go-build \
		docker.io/library/golang:1.25-alpine3.22 sh -c "$build" sh "$version"
fi
