#!/bin/sh
# The website's pictures and video, made with the real studio (--no-login, in podman) from the sample speech:
#
#   scripts/site-shots.sh          both (~10 min)
#   scripts/site-shots.sh shots    site/img/shots/<en|nb|nn>/*.webp: the studio in each of the site's languages
#   scripts/site-shots.sh video    site/media/demo.mp4, demo.webm and demo-poster.webp: the studio in use, in English
#
# Each run makes them all again from nothing (scripts/test.sh site-shots, web/e2e/site-shots/). The speech is the
# one the guide's pictures use (scripts/fetch-sample.sh), taken from ~/.cache/sermon-studio-samples/ when it is there.
set -eu
cd "$(dirname "$0")/.."
case ${1:-all} in all | shots | video) ;; *) sed -n '2,9p' "$0"; exit 2 ;; esac

cache=$HOME/.cache/sermon-studio-samples/jfk-inaugural.ogg
if [ ! -s web/e2e/fixtures/jfk-inaugural.ogg ] && [ -s "$cache" ]; then
	mkdir -p web/e2e/fixtures
	cp "$cache" web/e2e/fixtures/jfk-inaugural.ogg
fi
scripts/fetch-sample.sh
exec scripts/test.sh site-shots "${1:-all}"
