# WITH_WEB selects whether jellyfin-web (a separate, GPL-2.0-licensed
# project, github.com/jellyfin/jellyfin-web) gets built and bundled into
# the image, served as static files at /web/ (cmd/tinyfin/webui.go). Off
# by default: the default image stays MIT-only with zero extra runtime
# dependencies (AGENTS.md principle 2). This Go module never imports or
# links against jellyfin-web's code either way — see
# docs/00-decision-log.md's D9 for why that keeps this a clean MIT/GPL
# boundary rather than a license conflict.
ARG WITH_WEB=false

# JELLYFIN_FFMPEG_VERSION pins the jellyfin-ffmpeg release (github.com/jellyfin/jellyfin-ffmpeg,
# a separate FOSS project from the Jellyfin server — see D2/D7 in docs/00-decision-log.md for why
# depending on it as an unmodified external subprocess is the whole point, not a shortcut) bundled
# into the image so Phase 2 transcoding (docs/07-transcoding-pipeline.md) works out of the box.
# Unlike WITH_WEB, this is NOT optional: Tinyfin invokes jellyfin-ffmpeg as a required subprocess
# for Remux/Direct Stream/Transcode, so shipping without it would leave those modes silently
# broken. The `ffmpeg` build stage below downloads the portable, statically-codec-linked tarball
# GitHub release asset for the target architecture (no apt package involved — see D10,
# docs/00-decision-log.md, for why: Debian/Ubuntu's `jellyfin-ffmpeg7` .deb package expects a full
# apt-managed userland the distroless-family final image below intentionally doesn't have).
ARG JELLYFIN_FFMPEG_VERSION=7.1.4-3

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/tinyfin ./cmd/tinyfin

FROM alpine:3.20 AS web-false
RUN mkdir -p /web

FROM node:24-bookworm AS web-true
RUN apt-get update && apt-get install -y --no-install-recommends git \
	&& rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/jellyfin/jellyfin-web.git /src-web
WORKDIR /src-web
RUN npm ci
RUN npm run build:production
RUN mv dist /web

FROM web-${WITH_WEB} AS webassets

# Downloads the jellyfin-ffmpeg portable Linux tarball (ffmpeg + ffprobe, every codec library
# statically linked in) for whichever architecture buildkit is targeting. TARGETARCH is a
# buildkit-provided build arg (amd64/arm64) — no manual --platform plumbing needed.
FROM debian:bookworm-slim AS ffmpeg
ARG TARGETARCH
ARG JELLYFIN_FFMPEG_VERSION
RUN apt-get update && apt-get install -y --no-install-recommends curl xz-utils ca-certificates fonts-dejavu-core fonts-wqy-zenhei \
	&& rm -rf /var/lib/apt/lists/* \
	&& arch="$( [ "$TARGETARCH" = "arm64" ] && echo linuxarm64-gpl || echo linux64-gpl )" \
	&& curl -fsSL -o /tmp/jellyfin-ffmpeg.tar.xz \
		"https://github.com/jellyfin/jellyfin-ffmpeg/releases/download/v${JELLYFIN_FFMPEG_VERSION}/jellyfin-ffmpeg_${JELLYFIN_FFMPEG_VERSION}_portable_${arch}.tar.xz" \
	&& mkdir -p /jellyfin-ffmpeg \
	&& tar -xJf /tmp/jellyfin-ffmpeg.tar.xz -C /jellyfin-ffmpeg \
	&& rm /tmp/jellyfin-ffmpeg.tar.xz

# base-debian12 (not static-debian12): the jellyfin-ffmpeg portable binaries below are dynamically
# linked against glibc (confirmed via ldd — libc/libm/libpthread/libdl + the ELF interpreter), so
# the fully-static distroless variant that the Go binary alone was happy with can't run them.
# base-debian12 adds glibc + libssl but still ships no shell and no package manager, keeping the
# same minimal-attack-surface property static-debian12 had. See D10, docs/00-decision-log.md.
FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/tinyfin /tinyfin
COPY --from=webassets /web /web
# Final ffmpeg/ffprobe path other Tinyfin code (internal/transcode) should target:
#   /usr/lib/jellyfin-ffmpeg/ffmpeg and /usr/lib/jellyfin-ffmpeg/ffprobe
COPY --from=ffmpeg /jellyfin-ffmpeg /usr/lib/jellyfin-ffmpeg
# Fonts for burned-in text subtitles, the same fallback as the jellyfin image: DejaVu Sans for a
# font that is not installed, WenQuanYi Zen Hei for CJK. The image has no fontconfig data of its
# own and libass draws no text without a configuration, so one is shipped (deploy/fonts.conf).
COPY --from=ffmpeg /usr/share/fonts/truetype/dejavu /usr/share/fonts/truetype/dejavu
COPY --from=ffmpeg /usr/share/fonts/truetype/wqy/wqy-zenhei.ttc /usr/share/fonts/truetype/wqy/wqy-zenhei.ttc
COPY deploy/fonts.conf /etc/fonts/fonts.conf
ENV FONTCONFIG_FILE=/etc/fonts/fonts.conf
ENV TINYFIN_FONTS_DIR=/usr/share/fonts/truetype/dejavu
VOLUME ["/data"]
EXPOSE 8096
ENTRYPOINT ["/tinyfin"]
CMD ["-data", "/data/tinyfin.db", "-addr", ":8096"]
