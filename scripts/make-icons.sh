#!/usr/bin/env bash
# Makes the PNG icons from web/static/icons/logo.svg, the one source of the
# logo. Run it after changing the SVG, and commit the PNGs.
#   needs: rsvg-convert (librsvg), magick (ImageMagick); oxipng if installed
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/web/static/icons"

png() { rsvg-convert -w "$1" -h "$1" logo.svg -o "$2"; }
png 32 favicon-32.png
png 192 icon-192.png
png 512 icon-512.png
# iOS rounds the corners itself and turns transparent ones black: fill them.
png 180 apple-touch-icon.png
magick apple-touch-icon.png -background '#11055a' -flatten apple-touch-icon.png
if command -v oxipng >/dev/null; then oxipng -q -o 4 --strip safe ./*.png; fi
ls -l ./*.png
