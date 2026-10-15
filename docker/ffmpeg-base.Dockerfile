# Prebuilt ffmpeg base image. Published once to
# ghcr.io/raikada/nvr-ffmpeg:<ffmpeg-version> and consumed by
# consumer.Dockerfile via COPY --from. ffmpeg never changes between
# app releases, so pulling this cached layer drops the appliance build
# from ~9 min (recompiling ffmpeg every time) to ~2-3 min (Go + SPA
# only). Rebuild + re-push this image only when FFMPEG_VERSION changes.
#
#   docker buildx build --platform linux/amd64 \
#     -f docker/ffmpeg-base.Dockerfile \
#     -t ghcr.io/raikada/nvr-ffmpeg:8.0 --push .
#
# (CI: .github/workflows/ffmpeg-base.yml, manual dispatch.)
FROM debian:bookworm-slim

ARG FFMPEG_VERSION=8.0
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
