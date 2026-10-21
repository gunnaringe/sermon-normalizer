# The browser tests' image: Playwright with ffmpeg in it, built once by scripts/test.sh
# (instead of installing ffmpeg on every run). BASE is the Playwright image test.sh names.
ARG BASE
FROM ${BASE}
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ffmpeg >/dev/null && rm -rf /var/lib/apt/lists/*
