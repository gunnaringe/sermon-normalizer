#!/bin/sh
# End-to-end test of the TUI: drives it in tmux with real ffmpeg and checks
# the screen and the files it leaves. Runs inside the Go + ffmpeg image:
#
#   podman run --rm -v "$PWD":/src:Z -w /src docker.io/library/golang:1.25-alpine3.22 sh scripts/tui-e2e.sh
set -eu

apk add -q --no-cache ffmpeg tmux
go build -o /tmp/sermon-normalizer ./cmd/sermon-normalizer

root=$(mktemp -d)
day="$root/2026/2026-10-04"
mkdir -p "$day" "$root/2026/notes"
# Music, 2 s silence at 19-21 s, quieter speech, silence at 79-81 s, music.
expr="if(between(t,19,21)+between(t,79,81), 0, if(between(t,21,79), 0.05*sin(2*PI*220*t)*(0.6+0.4*sin(2*PI*3*t)), 0.5*sin(2*PI*440*t)))"
ffmpeg -v error -f lavfi -i "aevalsrc=exprs='$expr|$expr':s=48000:d=100" -c:a pcm_s24le "$day/2026-10-04_raw.wav"

export TERM=xterm-256color
tmux new-session -d -s e2e -x 120 -y 40 "/tmp/sermon-normalizer tui $root/2026; echo TUI-EXITED; sleep 60"

screen() { tmux capture-pane -p -t e2e; }
send() { tmux send-keys -t e2e "$@"; }
fail() {
	echo "FAIL: $*"
	echo "---- screen ----"
	screen
	exit 1
}
# wait_for TEXT: poll the screen (up to 60 s) instead of sleeping blindly.
wait_for() {
	i=0
	until screen | grep -qF -- "$1"; do
		i=$((i + 1))
		[ "$i" -gt 300 ] && fail "timed out waiting for: $1"
		sleep 0.2
	done
	echo "ok: $1"
}

# Folder list: newest date first, plain folders marked with /.
wait_for "2026-10-04"
wait_for "notes/"
send Down
wait_for "› 2026-10-04"
send Enter

# Cut on the timeline: jump to the silences around the sermon.
wait_for "Recording: 2026-10-04_raw.wav  (1:40)"
wait_for "cursor 0:00"
send "]" "]" s "]" e
wait_for "start 0:21  end 1:19  length 0:58"
send Enter

# Options: MP3 + Opus only, 1 s fade in.
wait_for "Same as: sermon-normalizer run"
wait_for "--start 0:00:21 --end 0:01:19"
send Space Down Down Down BSpace 1
wait_for "--formats mp3,opus"
wait_for "--fade-in 1"
send Enter

wait_for "2026-10-04 done"
wait_for "2026-10-04.mp3"
files=$(cd "$day" && ls -A | tr '\n' ' ')
[ "$files" = "2026-10-04.mp3 2026-10-04.opus 2026-10-04_raw.wav " ] || fail "files: $files"
echo "ok: files $files"
dur=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$day/2026-10-04.mp3")
awk -v d="$dur" 'BEGIN { exit !(d > 57.8 && d < 58.2) }' || fail "mp3 is $dur s, want 58"
echo "ok: mp3 is $dur s"

# Back to the list, which shows the new status.
send Enter
wait_for "› 2026-10-04   raw · ✓ mp3 opus"

# Run again with Force and cancel straight away: nothing half-written stays.
send Enter
wait_for "cursor 0:00"
send Enter
wait_for "Mark where the sermon starts"
send "]" "]" s
wait_for "start 0:21  end end"
send Enter
wait_for "Same as:"
# FLAC stays off from the last run; Force does not carry over.
screen | grep -qF "FLAC master             [ ]" || fail "the last run's outputs should be remembered"
send Up Space
wait_for "--force"
send Enter
wait_for "Processing 2026-10-04"
send Escape
wait_for "2026-10-04 cancelled"
wait_for "Nothing half-written was left behind"
partial=$(find "$day" -name '*.partial.*' -o -name '.*.master.flac')
[ -z "$partial" ] || fail "left behind: $partial"
echo "ok: no partial files after cancel"

send Enter
wait_for "2026-10-04"
send q
wait_for "TUI-EXITED"
echo "PASS"
