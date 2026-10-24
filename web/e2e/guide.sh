#!/bin/sh
# The pictures of the documentation (docs/img), made from a real speech with the real app.
# Runs inside the Playwright image; start it with: scripts/test.sh guide
set -eu

command -v ffmpeg >/dev/null || { apt-get update -qq >/dev/null && apt-get install -y -qq ffmpeg >/dev/null; } # in scripts/test.sh's image already
speech=fixtures/jfk-inaugural.ogg
[ -s "$speech" ] || { echo "no $speech: run scripts/fetch-sample.sh first" >&2; exit 1; }

data=/data # a folder like a real one, for the paths shown in the log
mkdir -p "$data"
church="$data/churches/grace-chapel"
mkdir -p "$church/Taler/2026" "$church/Innboks" "$data/cache"
ln -s "$church/Taler" "$data/root"
cat >"$church/church.json" <<JSON
{"name":"Grace Chapel","meta":{"title":"{yyyy}-{mm}-{dd} {name}","artist":"Grace Chapel","album":"Sunday services"}}
JSON

# A service as a recorder makes it: a WAV of the whole speech to upload, named like a Behringer X32's
# (R_<date>-<time>.wav); a few earlier Sundays,
# two minutes each from other parts of it; and a short file for the public upload page.
ffmpeg -v error -i "$speech" -ac 1 -ar 44100 -c:a pcm_s16le "$data/R_20261018-104506.wav"
n=0
for day in 2026-09-06 2026-09-13 2026-09-20 2026-09-27 2026-10-04; do
	mkdir -p "$church/Taler/2026/$day"
	ffmpeg -v error -ss $((60 + n * 150)) -t 120 -i "$speech" -c:a flac "$church/Taler/2026/$day/${day}_raw.flac"
	n=$((n + 1))
done
mkdir -p "$church/Taler/2025/2025-12-21"
ffmpeg -v error -ss 400 -t 90 -i "$speech" -c:a flac "$church/Taler/2025/2025-12-21/2025-12-21_raw.flac"
ffmpeg -v error -ss 200 -t 40 -i "$speech" -c:a flac "$data/guest.flac"

./bin/sermon-studio --no-login --listen 127.0.0.1:8080 --data-dir "$data/churches" --cache-dir "$data/cache" \
	--share-listen 127.0.0.1:8081 --share-url http://localhost:8081/ >"$data/server.log" 2>&1 &
server=$!
trap 'kill $server 2>/dev/null; wait $server 2>/dev/null || true' EXIT
for i in $(seq 50); do
	curl -sf http://127.0.0.1:8080/api/config >/dev/null && break
	sleep 0.1
done

# node_modules is kept between runs (it is in the mounted folder): installed again only when the lock file changes.
lock=$(sha256sum package-lock.json | cut -c1-64)
[ "$(cat node_modules/.lock-sha 2>/dev/null)" = "$lock" ] || { npm ci --silent --no-audit --no-fund && echo "$lock" >node_modules/.lock-sha; }
DATA="$data" OUT=/out npx playwright test --config guide/playwright.config.js --reporter=line "$@" || { echo "---- server log (end) ----"; tail -n 50 "$data/server.log"; exit 1; }
