# Consumer NVR appliance image (SP1–SP4 fork).
#
# Unlike the upstream mediamtx Dockerfiles (which wrap prebuilt static
# binaries), this fork links libav via cgo (clip remux + snapshot frame
# grab), so the image builds everything from source:
#
#   web   — the embedded SPA (node 20, mirrors scripts/web.mk)
#   ffmpeg— shared libs n8.0 exactly (go-astiav v0.40 requirement);
#           no distro ships it, so build a minimal shared-lib set
#   build — go 1.25 + cgo against the stage-built libav
#   final — debian slim + the same libav runtime libs + the binary
#
# Build (on the NAS or any Docker host; pick your platform):
#   docker build -f docker/consumer.Dockerfile -t raikada-nvr:latest \
#     --build-arg VERSION=v1.3.0 .
#
# Cross-build from another machine:
#   docker buildx build --platform linux/amd64 -f docker/consumer.Dockerfile \
#     -t raikada-nvr:latest --load .

ARG FFMPEG_VERSION=8.0

#################################################################
FROM node:20-alpine AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

#################################################################
FROM debian:bookworm-slim AS ffmpeg

ARG FFMPEG_VERSION
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential nasm pkg-config curl ca-certificates xz-utils \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" -o /tmp/ffmpeg.tar.xz \
    && mkdir -p /tmp/ffmpeg && tar -xJf /tmp/ffmpeg.tar.xz -C /tmp/ffmpeg --strip-components=1
# Shared libs only, no programs/docs; demux/remux + jpeg encode is all
# the recorder needs from libav (no x264 — we never encode video).
RUN cd /tmp/ffmpeg && ./configure \
      --prefix=/opt/ffmpeg \
      --enable-shared --disable-static \
      --disable-programs --disable-doc \
      --disable-debug \
    && make -j"$(nproc)" && make install

#################################################################
FROM golang:1.25-bookworm AS build

ARG VERSION=dev
RUN apt-get update && apt-get install -y --no-install-recommends pkg-config \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg /opt/ffmpeg /opt/ffmpeg
ENV PKG_CONFIG_PATH=/opt/ffmpeg/lib/pkgconfig \
    CGO_ENABLED=1

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# VERSION normally comes from git tags via go:generate; the build
# context has no .git, so stamp it from the build arg. hls.min.js is
# downloaded by its own generate step (network available at build time).
RUN printf '%s' "${VERSION}" > internal/core/VERSION \
    && go generate ./internal/servers/hls/...

# Embedded SPA from the web stage (mirrors scripts/web.mk).
COPY --from=web /src/web/dist/ internal/web/dist/

RUN go build -o /out/raikada .

#################################################################
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg /opt/ffmpeg/lib/ /opt/ffmpeg/lib/
ENV LD_LIBRARY_PATH=/opt/ffmpeg/lib

COPY --from=build /out/raikada /usr/local/bin/raikada
COPY docker/consumer-config.yml /etc/raikada/config.yml

# /data     — identity (keys, SQLite DB, TLS certs). Small, back it up.
# /recordings — video segments + snapshots + clip exports. Big volume.
VOLUME ["/data", "/recordings"]

# API/HLS/RTSP/WebRTC/RTMP/SRT. Use host networking on a NAS so
# WS-Discovery multicast + mDNS work; these EXPOSEs document the
# surface for bridge-mode users who forgo discovery.
EXPOSE 9997 9996 8554 8000/udp 8001/udp 1935 8888 8889 8189/udp 8890/udp

ENTRYPOINT ["/usr/local/bin/raikada"]
CMD ["/etc/raikada/config.yml"]
