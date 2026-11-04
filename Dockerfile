ARG FFMPEG_VERSION=7.1

FROM mwader/static-ffmpeg:$FFMPEG_VERSION AS ffmpeg

FROM golang:1.26-alpine AS builder

# A cached golang:1.26-alpine can be older than go.mod's go line: auto
# downloads the right toolchain instead of failing, as core's build does.
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly go build -trimpath -o /app ./cmd/app

# One image runs all three commands: `serve` (the default), `worker` and
# `migrate`. Alpine, the same as golang:1.26-alpine above, instead of
# distroless, because the worker runs yt-dlp.
FROM alpine:3.24

# The worker downloads videos with yt-dlp and ffmpeg, the way core's image
# did. yt-dlp needs a JavaScript runtime (node) to get YouTube's video
# formats, and only uses node when its config turns it on. It runs as root,
# as core's worker did, because the videos volume belongs to root.
RUN apk add --no-cache python3 py3-pip nodejs && \
	pip install --break-system-packages requests brotli websockets certifi yt-dlp[default,curl-cffi]
COPY --from=ffmpeg /ffmpeg /ffprobe /usr/bin/
RUN wget -qO /usr/bin/yt-dlp https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp && chmod 755 /usr/bin/yt-dlp
RUN mkdir -p /root/.config/yt-dlp && echo '--js-runtimes node' > /root/.config/yt-dlp/config

# Reported by /api/version. CI passes the release version (or sha-<short>),
# the commit and the branch; "unknown" for anyone who builds without them.
ARG APP_VERSION=unknown
ARG GIT_COMMIT=unknown
ARG GIT_BRANCH=unknown
ENV APP_VERSION=$APP_VERSION
ENV GIT_COMMIT=$GIT_COMMIT
ENV GIT_BRANCH=$GIT_BRANCH

COPY --from=builder /app /app

EXPOSE 8080
ENTRYPOINT ["/app"]
CMD ["serve"]
