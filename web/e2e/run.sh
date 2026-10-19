#!/bin/sh
# Browser tests of the web UI. Runs inside the Playwright image, with the
# server binary mounted at /work/bin. Start it with: scripts/test.sh e2e
set -eu

apt-get update -qq >/dev/null && apt-get install -y -qq ffmpeg >/dev/null
data=$(mktemp -d)
# One church folder, as in production; root/ and inbox/ point into it for the tests.
church="$data/churches/first-church"
mkdir -p "$church/Taler/2026/2026-10-04" "$church/Taler/2026/2026-08-30" "$church/Innboks" "$data/cache"
ln -s "$church/Taler" "$data/root"
ln -s "$church/Innboks" "$data/inbox"

# A 100 s service: music, silence at 19-21 s, speech, silence at 79-81 s, music.
expr="if(between(t,19,21)+between(t,79,81), 0, if(between(t,21,79), 0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.5*sin(2*PI*440*t)))"
ffmpeg -v error -f lavfi -i "aevalsrc=exprs='$expr|$expr':s=48000:d=100" -c:a pcm_s24le "$data/root/2026/2026-10-04/2026-10-04_raw.wav"
# A 90-minute service, to check the waveform stays responsive at real length.
long="0.3*sin(2*PI*220*t)*(0.5+0.5*sin(2*PI*t/600))"
ffmpeg -v error -f lavfi -i "aevalsrc=exprs='$long':s=8000:d=5400" -c:a flac "$data/root/2026/2026-08-30/2026-08-30_raw.flac"
# A recording waiting to be uploaded.
ffmpeg -v error -f lavfi -i "aevalsrc=exprs='$expr':s=48000:d=30" -c:a flac "$data/upload.flac"

./bin/sermon-studio --listen 127.0.0.1:8080 --data-dir "$data/churches" --cache-dir "$data/cache" >"$data/server.log" 2>&1 &
server=$!
trap 'kill $server 2>/dev/null; wait $server 2>/dev/null || true' EXIT
for i in $(seq 50); do
	curl -sf http://127.0.0.1:8080/api/config >/dev/null && break
	sleep 0.1
done

npm ci --silent --no-audit --no-fund
DATA="$data" npx playwright test --reporter=line "$@" || { echo "---- server log (end) ----"; tail -n 50 "$data/server.log"; exit 1; }
