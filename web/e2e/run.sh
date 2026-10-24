#!/bin/sh
# Browser tests of the web UI. Runs inside the Playwright image, with the
# server binary mounted at /work/bin. Start it with: scripts/test.sh e2e
set -eu

command -v ffmpeg >/dev/null || { apt-get update -qq >/dev/null && apt-get install -y -qq ffmpeg >/dev/null; } # in scripts/test.sh's image already
data=$(mktemp -d)
# One church folder, as in production; root/ and inbox/ point into it for the tests.
church="$data/churches/first-church"
mkdir -p "$church/Taler/2026/2026-10-04" "$church/Taler/2026/2026-06-14" "$church/Taler/2026/2026-08-30" "$church/Innboks" "$data/cache"
mkdir -p "$church/Taler/2025/2025-12-21"
ln -s "$church/Taler" "$data/root"
ln -s "$church/Innboks" "$data/inbox"

# gen <file> <ffmpeg arguments>: makes a test recording with ffmpeg. It is kept in fixtures/made/ (named after the
# arguments), so later runs copy it instead of making it again: the 90-minute one takes seconds.
gen() {
	out=$1
	shift
	ext=${out##*.}
	kept="fixtures/made/$(echo "$*" | sha256sum | cut -c1-16).$ext"
	if [ ! -s "$kept" ]; then
		mkdir -p fixtures/made
		ffmpeg -v error "$@" "$kept.part.$ext" && mv "$kept.part.$ext" "$kept"
	fi
	cp "$kept" "$out"
}

# A 100 s service: music, silence at 19-21 s, speech, silence at 79-81 s, music.
expr="if(between(t,19,21)+between(t,79,81), 0, if(between(t,21,79), 0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.5*sin(2*PI*440*t)))"
gen "$data/root/2026/2026-10-04/2026-10-04_raw.wav" -f lavfi -i "aevalsrc=exprs='$expr|$expr':s=48000:d=100" -c:a pcm_s24le
# A minute of speech with silence before and after it: the editor starts with the sermon set to the speech.
quiet="if(between(t,3,56), 0.3*sin(2*PI*330*t)*(0.6+0.4*sin(2*PI*2*t)), 0)"
gen "$data/root/2026/2026-06-14/2026-06-14_raw.flac" -f lavfi -i "aevalsrc=exprs='$quiet':s=8000:d=60" -c:a flac
# A 90-minute service, to check the waveform stays responsive at real length.
long="0.3*sin(2*PI*220*t)*(0.5+0.5*sin(2*PI*t/600))"
gen "$data/root/2026/2026-08-30/2026-08-30_raw.flac" -f lavfi -i "aevalsrc=exprs='$long':s=8000:d=5400" -c:a flac
# An older year, with one unprocessed recording: a year that starts folded.
gen "$data/root/2025/2025-12-21/2025-12-21_raw.flac" -f lavfi -i "aevalsrc=exprs='$expr':s=8000:d=5" -c:a flac
# A recording waiting to be uploaded.
gen "$data/upload.flac" -f lavfi -i "aevalsrc=exprs='$expr':s=48000:d=30" -c:a flac

./bin/sermon-studio --listen 127.0.0.1:8080 --data-dir "$data/churches" --cache-dir "$data/cache" \
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
DATA="$data" npx playwright test --reporter=line "$@" || { echo "---- server log (end) ----"; tail -n 50 "$data/server.log"; exit 1; }
