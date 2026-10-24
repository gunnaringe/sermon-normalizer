#!/usr/bin/env bash
# Makes the images that style Pocket ID for the studio (docs/login-pocket-id.md)
# from web/static/icons/logo.svg, the one source of the logo. Run it after
# changing the SVG, and commit the results in docs/pocket-id/.
#   needs: rsvg-convert (librsvg), magick (ImageMagick), cwebp; oxipng if installed
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
src="$root/web/static/icons/logo.svg"
out="$root/docs/pocket-id"
mkdir -p "$out"
cd "$out"

# The logo is a dark green tile, which reads on a light page as it is. On a
# dark page it gets a thin light green rim, so its edge does not get lost.
cp "$src" logo-light.svg
sed 's|<rect width="1254" height="1254" rx="250" fill="url(#bg)"/>|&\n  <rect x="12" y="12" width="1230" height="1230" rx="240" fill="none" stroke="#3fd19f" stroke-opacity="0.35" stroke-width="18"/>|' \
  "$src" > logo-dark.svg
grep -q 'stroke-opacity="0.35"' logo-dark.svg || { echo "logo-dark.svg: the rim was not added" >&2; exit 1; }

png() { rsvg-convert -w "$2" -h "$2" "$1" -o "$3"; }
# Application Configuration → Images: Light Mode Logo, Dark Mode Logo (shown
# at 40 px on the login page; 512 px stays sharp anywhere).
png logo-light.svg 512 logo-light.png
png logo-dark.svg 512 logo-dark.png
# OIDC Clients → the client's logo (PNG, at most 2 MB).
png "$src" 256 client-logo.png
# Favicon (SVG, PNG or ICO): 16, 32 and 48 px in one .ico.
for s in 16 32 48; do png "$src" "$s" "favicon-$s.tmp.png"; done
magick favicon-16.tmp.png favicon-32.tmp.png favicon-48.tmp.png favicon.ico
rm favicon-*.tmp.png

# Background Image: a calm gradient in the studio's green (the --grad of
# style.css: #047857, #059669, #0f766e), with soft light and the logo's
# soundwave faint at the bottom. On a wide screen it fills the panel beside
# the login card (cropped to fit), on a phone it is behind the card.
cat > background.tmp.svg <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080">
  <defs>
    <linearGradient id="base" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#063326"/>
      <stop offset="0.5" stop-color="#065f46"/>
      <stop offset="1" stop-color="#0f766e"/>
    </linearGradient>
    <radialGradient id="glow1" cx="0.25" cy="0.2" r="0.6">
      <stop offset="0" stop-color="#059669" stop-opacity="0.55"/>
      <stop offset="1" stop-color="#059669" stop-opacity="0"/>
    </radialGradient>
    <radialGradient id="glow2" cx="0.85" cy="0.85" r="0.55">
      <stop offset="0" stop-color="#14b8a6" stop-opacity="0.35"/>
      <stop offset="1" stop-color="#14b8a6" stop-opacity="0"/>
    </radialGradient>
    <radialGradient id="warm" cx="0.5" cy="0.62" r="0.35">
      <stop offset="0" stop-color="#fdb52a" stop-opacity="0.16"/>
      <stop offset="1" stop-color="#fdb52a" stop-opacity="0"/>
    </radialGradient>
    <linearGradient id="fade" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#fff" stop-opacity="0"/>
      <stop offset="1" stop-color="#fff" stop-opacity="1"/>
    </linearGradient>
    <mask id="bars"><rect width="1920" height="1080" fill="url(#fade)"/></mask>
  </defs>
  <rect width="1920" height="1080" fill="url(#base)"/>
  <rect width="1920" height="1080" fill="url(#glow1)"/>
  <rect width="1920" height="1080" fill="url(#glow2)"/>
  <rect width="1920" height="1080" fill="url(#warm)"/>
  <g mask="url(#bars)" stroke="#a9e6cd" stroke-opacity="0.08" stroke-width="22" stroke-linecap="round">
SVG
# The soundwave: bars across the bottom, tallest in the middle.
awk 'BEGIN { for (i = 0; i < 40; i++) { x = 60 + i * 46; d = (i - 19.5) / 19.5;
  h = 60 + 300 * exp(-d * d * 3) * (0.85 + 0.15 * sin(i * 1.7));
  printf "    <path d=\"M%d %dV%d\"/>\n", x, 1040 - h, 1040 } }' >> background.tmp.svg
echo '  </g>
</svg>' >> background.tmp.svg
rsvg-convert -w 1920 -h 1080 background.tmp.svg -o background.tmp.png
cwebp -quiet -q 82 background.tmp.png -o background.webp
rm background.tmp.svg background.tmp.png

if command -v oxipng >/dev/null; then oxipng -q -o 4 --strip safe ./*.png; fi
ls -l
