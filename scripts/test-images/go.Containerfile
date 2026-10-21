# The Go tests' image with ffmpeg in it, built once by scripts/test.sh (instead of installing
# ffmpeg on every "scripts/test.sh all"). BASE is the Go image test.sh names.
ARG BASE
FROM ${BASE}
RUN apk add -q --no-cache ffmpeg
