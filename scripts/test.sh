#!/bin/sh
# One entry point for checks, with short output (ok lines and failures only).
#
#   scripts/test.sh            unit tests (no ffmpeg; seconds; host Go or container)
#   scripts/test.sh all [args] vet + all tests with ffmpeg, in a container;
#                              args go to "go test" (default ./...), e.g.
#                              scripts/test.sh all ./web/ -run Inbox
#   scripts/test.sh e2e [args] browser tests (Playwright); args go to playwright,
#                              e.g. scripts/test.sh e2e -g upload
#   scripts/test.sh image      the Docker image (vet + all tests inside)
#
# Go modules and build cache live in named volumes, so repeat runs are fast.
set -eu
cd "$(dirname "$0")/.."
run=$(command -v podman || command -v docker) || { echo "needs podman or docker" >&2; exit 1; }
golang=docker.io/library/golang:1.25-alpine3.22
playwright=mcr.microsoft.com/playwright:v1.64.1-noble # keep equal to web/e2e/package.json
incontainer() {
	"$run" run --rm -v "$PWD":/src:Z -w /src -v gomod:/go/pkg/mod -v gocache:/root/.cache/go-build \
		-e CGO_ENABLED=0 "$golang" sh -c "$1"
}

mode=${1:-unit}
[ $# -gt 0 ] && shift
case $mode in
unit)
	if command -v go >/dev/null; then
		test -z "$(gofmt -l cmd normalizer web)" || { gofmt -l cmd normalizer web; echo "gofmt needed" >&2; exit 1; }
		go vet ./... && go test -short ./...
	else
		incontainer 'test -z "$(gofmt -l cmd normalizer web)" || { gofmt -l cmd normalizer web; exit 1; }; go vet ./... && go test -short ./...'
	fi
	;;
all)
	incontainer "apk add -q ffmpeg && test -z \"\$(gofmt -l cmd normalizer web)\" && go vet ./... && go test ${*:-./...}"
	;;
e2e)
	incontainer 'go build -o web/e2e/bin/sermon-normalizer ./cmd/sermon-normalizer'
	"$run" run --rm --ipc=host -v "$PWD/web/e2e":/work:Z -w /work "$playwright" sh run.sh "$@" 2>&1 |
		grep -v -e '^debconf' -e '^$'
	;;
image)
	"$run" build -q -t sermon-normalizer .
	;;
*)
	sed -n '2,13p' "$0"
	exit 2
	;;
esac
