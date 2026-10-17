# Bygg: docker build -t talenorm .
# Kjor:  docker run --rm -u "$(id -u):$(id -g)" -v /sti/til/Taler:/data talenorm run /data/2026/2026-10-04

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /talenorm .

FROM alpine:3.22
# ffmpeg i Alpine har libmp3lame og libopus
RUN apk add --no-cache ffmpeg \
 && ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /talenorm /usr/local/bin/talenorm
WORKDIR /data
ENTRYPOINT ["talenorm"]
CMD ["--help"]
