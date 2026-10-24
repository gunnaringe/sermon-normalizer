#!/bin/sh
# The website's screenshots (site/img/shots/<lang>/) and its video (site/media/), made with the real studio from
# a real speech. Runs inside the Playwright image, with site/ at /site and web/static at /static (for the
# translations); start it with: scripts/site-shots.sh [shots|video]
set -eu
cd "$(dirname "$0")/.."

what=${1:-all}
speech=fixtures/jfk-inaugural.ogg
[ -s "$speech" ] || { echo "no $speech: run scripts/fetch-sample.sh first" >&2; exit 1; }
langs="en nb nn"
tmp=$(mktemp -d)
server=
stop() { [ -z "$server" ] || { kill "$server" 2>/dev/null; wait "$server" 2>/dev/null || true; }; server=; }
trap 'stop; rm -rf "$tmp"' EXIT

# node_modules is kept between runs (it is in the mounted folder): installed again only when the lock file changes.
lock=$(sha256sum package-lock.json | cut -c1-64)
[ "$(cat node_modules/.lock-sha 2>/dev/null)" = "$lock" ] || { npm ci --silent --no-audit --no-fund && echo "$lock" >node_modules/.lock-sha; }

# church <data> <id> <name> <album>: a church folder with five earlier Sundays, two minutes each from other parts
# of the speech, not cut yet.
church() {
	dir="$1/churches/$2"
	mkdir -p "$dir/Taler/2026" "$dir/Innboks"
	printf '{"name":"%s","meta":{"title":"{yyyy}-{mm}-{dd} {name}","artist":"%s","album":"%s"}}\n' "$3" "$3" "$4" >"$dir/church.json"
	n=0
	for day in 2026-09-06 2026-09-13 2026-09-20 2026-09-27 2026-10-04; do
		mkdir -p "$dir/Taler/2026/$day"
		cp "$1/sunday-$n.flac" "$dir/Taler/2026/$day/${day}_raw.flac"
		n=$((n + 1))
	done
}

# serve <data>: the studio without a login, as scripts/test.sh guide runs it.
serve() {
	mkdir -p "$1/cache"
	./bin/sermon-studio --no-login --listen 127.0.0.1:8080 --data-dir "$1/churches" --cache-dir "$1/cache" \
		--share-listen 127.0.0.1:8081 --share-url http://localhost:8081/ >"$1/server.log" 2>&1 &
	server=$!
	for i in $(seq 50); do
		curl -sf http://127.0.0.1:8080/api/config >/dev/null && return
		sleep 0.1
	done
	echo "the studio did not start" >&2
	cat "$1/server.log" >&2
	exit 1
}

# run <spec>: the spec against the studio; the end of its log if it fails.
run() {
	DATA="$data" OUT="$tmp/out" npx playwright test --config site-shots/playwright.config.js --reporter=line "$1" ||
		{ echo "---- server log (end) ----"; tail -n 50 "$data/server.log"; exit 1; }
}

# What every run uses: the earlier Sundays, an intro clip (a few seconds of tone, so there is no licence to think
# about), and a short recording for the public upload page.
data=/data # a folder like a real one, for the paths the log shows (as guide.sh)
rm -rf "$data"
mkdir -p "$data" "$tmp/out"
for n in 0 1 2 3 4; do
	ffmpeg -v error -ss $((60 + n * 150)) -t 120 -i "$speech" -c:a flac "$data/sunday-$n.flac"
done
ffmpeg -v error -f lavfi -i "sine=frequency=523:duration=1.5" -f lavfi -i "sine=frequency=784:duration=2.5" \
	-filter_complex "[0][1]concat=n=2:v=0:a=1,afade=t=in:d=0.2,afade=t=out:st=3:d=1,volume=0.5" -ac 1 -ar 44100 -c:a pcm_s16le "$data/Welcome.wav"

if [ "$what" = all ] || [ "$what" = shots ]; then
	# One church per language, each used in its own language; the whole speech as a recorder makes it (a WAV).
	church "$data" grace-chapel "Grace Chapel" "Sunday services"
	church "$data" bykirken "Bykirken" "Gudstjenester"
	church "$data" bykyrkja "Bykyrkja" "Gudstenester"
	ffmpeg -v error -i "$speech" -ac 1 -ar 44100 -c:a pcm_s16le "$data/R_20261018-104506.wav"
	serve "$data"
	run shots.spec.js
	stop
	for l in $langs; do
		rm -rf "/site/img/shots/$l"
		mkdir -p "/site/img/shots/$l"
		for png in "$tmp/out/$l"/*.png; do
			ffmpeg -v error -i "$png" -c:v libwebp -quality 72 -compression_level 6 "/site/img/shots/$l/$(basename "$png" .png).webp"
		done
	done
	echo "screenshots: $(du -sh /site/img/shots | cut -f1) in site/img/shots"
fi

if [ "$what" = all ] || [ "$what" = video ]; then
	# A fresh studio, so the video starts before anything is done. Three minutes of the speech, ready quickly.
	rm -rf "$data/churches" "$data/cache"
	church "$data" grace-chapel "Grace Chapel" "Sunday services"
	ffmpeg -v error -i "$speech" -t 180 -ac 1 -ar 44100 -c:a pcm_s16le "$data/R_20261018-104506.wav"
	serve "$data"
	run video.spec.js
	stop
	start=$(cat "$tmp/out/video-start")
	media=/site/media
	rm -f "$media"/demo.* "$media"/demo-poster.*
	mkdir -p "$media"
	ffmpeg -v error -ss "$start" -i "$tmp/out/video.webm" -an -vf "fps=25,format=yuv420p" -c:v libx264 -preset slow -crf 27 \
		-profile:v high -movflags +faststart "$media/demo.mp4"
	ffmpeg -v error -ss "$start" -i "$tmp/out/video.webm" -an -vf "fps=25" -c:v libvpx-vp9 -b:v 0 -crf 42 -row-mt 1 \
		-deadline good -cpu-used 2 "$media/demo.webm"
	ffmpeg -v error -ss "$(cat "$tmp/out/video-poster")" -i "$tmp/out/video.webm" -frames:v 1 -c:v libwebp -quality 75 "$media/demo-poster.webp"
	echo "video: $(ffprobe -v error -show_entries format=duration -of csv=p=0 "$media/demo.mp4" | cut -d. -f1) s;" \
		"$(du -h "$media/demo.mp4" | cut -f1) MP4, $(du -h "$media/demo.webm" | cut -f1) WebM, $(du -h "$media/demo-poster.webp" | cut -f1) poster"
fi
