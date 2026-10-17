# Bygg: docker build -t sermon-normalizer .
# Kjor:  docker run --rm -u "$(id -u):$(id -g)" -v /sti/til/Taler:/data sermon-normalizer run /data/2026/2026-10-04

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY normalizer ./normalizer
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sermon-normalizer ./cmd/sermon-normalizer

FROM alpine:3.22
# ffmpeg i Alpine har libmp3lame og libopus
RUN apk add --no-cache ffmpeg \
 && ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /sermon-normalizer /usr/local/bin/sermon-normalizer
WORKDIR /data
ENTRYPOINT ["sermon-normalizer"]
CMD ["--help"]
