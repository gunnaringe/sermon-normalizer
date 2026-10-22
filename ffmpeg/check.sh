#!/bin/sh
# Checks an ffmpeg build: LGPL (not GPL or nonfree), and the encoders, filters and muxers the studio uses.
#   ffmpeg/check.sh <path to ffmpeg>
set -eu
ff=$1
"$ff" -hide_banner -L | tr -s ' \r\n' ' ' | grep -q "GNU Lesser General Public License" || { echo "$ff: not LGPL" >&2; exit 1; }
if "$ff" -hide_banner -buildconf | grep -qE -- "--enable-(gpl|nonfree)"; then echo "$ff: built with GPL or nonfree parts" >&2; exit 1; fi
encoders=$("$ff" -hide_banner -encoders)
filters=$("$ff" -hide_banner -filters)
for e in flac libmp3lame libopus aac pcm_s16le pcm_s24le; do
	echo "$encoders" | grep -qE "^ *[A-Z.]+ +$e " || { echo "$ff: no encoder $e" >&2; exit 1; }
done
for f in afftdn ebur128 alimiter acompressor dynaudnorm afade aformat aresample asetpts astats concat pan volume volumedetect; do
	echo "$filters" | grep -qE "^ *[A-Z.]+ +$f " || { echo "$ff: no filter $f" >&2; exit 1; }
done
muxers=$("$ff" -hide_banner -muxers)
for m in flac mp3 ogg opus matroska null md5 hash s16le wav; do
	echo "$muxers" | grep -qE "^ *[A-Z.]+ +$m " || { echo "$ff: no muxer $m" >&2; exit 1; }
done
echo "$ff: LGPL, with the encoders, filters and muxers the studio uses"
