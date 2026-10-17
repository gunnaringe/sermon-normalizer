# Build: docker build -t sermon-normalizer .
# Run:   docker run --rm -u "$(id -u):$(id -g)" -v /path/to/Taler:/data sermon-normalizer run /data/2026/2026-10-04

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
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sermon-normalizer ./cmd/sermon-normalizer

FROM alpine:3.22
# ffmpeg in Alpine has libmp3lame and libopus
RUN apk add --no-cache ffmpeg \
 && ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /sermon-normalizer /usr/local/bin/sermon-normalizer
WORKDIR /data
ENTRYPOINT ["sermon-normalizer"]
CMD ["--help"]
