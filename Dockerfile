# Build: docker build -t sermon-studio .
# Run:   docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" -v /path/to/Taler:/data/Taler -v /path/to/cache:/cache \
#          sermon-studio --root /data/Taler --cache-dir /cache

# The build stage runs on the builder's own platform: the tests run once, natively, and Go cross-compiles
# for each target (linux/amd64, linux/arm64). Only the final stage is per platform.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine3.22 AS build
# ffmpeg here too, so go test runs the integration tests against the same
# ffmpeg as the final image
RUN apk add --no-cache ffmpeg
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY normalizer ./normalizer
COPY web ./web
RUN go vet ./... && go test ./...
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /sermon-studio ./cmd/sermon-studio

FROM alpine:3.22
# ffmpeg in Alpine has libmp3lame and libopus
RUN apk add --no-cache ffmpeg \
 && ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /sermon-studio /usr/local/bin/
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["sermon-studio"]
CMD ["--help"]
