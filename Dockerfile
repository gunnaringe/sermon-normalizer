# Build: docker build -t sermon-normalizer .
# Run:   docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" -v /path/to/Taler:/data/Taler -v /path/to/cache:/cache \
#          sermon-normalizer --root /data/Taler --cache-dir /cache

FROM golang:1.25-alpine3.22 AS build
# ffmpeg and sqlite3 here too, so go test runs the integration tests against
# the same tools as the final image
RUN apk add --no-cache ffmpeg sqlite
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY normalizer ./normalizer
COPY web ./web
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sermon-normalizer ./cmd/sermon-normalizer

FROM alpine:3.22
# ffmpeg in Alpine has libmp3lame and libopus; sqlite3 reads Pingvin Share's database
RUN apk add --no-cache ffmpeg sqlite \
 && ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /sermon-normalizer /usr/local/bin/
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["sermon-normalizer"]
CMD ["--help"]
