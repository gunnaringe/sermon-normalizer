# Build: docker build -t sermon-normalizer .
# Web:   docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" -v /path/to/Taler:/data/Taler -v /path/to/cache:/cache \
#          sermon-normalizer --root /data/Taler --cache-dir /cache
# CLI:   docker run --rm -u "$(id -u):$(id -g)" -v /path/to/Taler:/data --entrypoint sermon-normalizer-cli \
#          sermon-normalizer run /data/2026/2026-10-04 --start 41:20 --end 1:12:05

FROM golang:1.25-alpine3.22 AS build
# ffmpeg here too, so go test runs the integration tests against the same
# ffmpeg as the final image
RUN apk add --no-cache ffmpeg
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY normalizer ./normalizer
COPY tui ./tui
COPY web ./web
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sermon-normalizer ./cmd/sermon-normalizer \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sermon-normalizer-cli ./cmd/sermon-normalizer-cli

FROM alpine:3.22
# ffmpeg in Alpine has libmp3lame and libopus
RUN apk add --no-cache ffmpeg \
 && ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /sermon-normalizer /sermon-normalizer-cli /usr/local/bin/
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["sermon-normalizer"]
CMD ["--help"]
