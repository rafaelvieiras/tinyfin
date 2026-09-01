# WITH_WEB selects whether jellyfin-web (a separate, GPL-2.0-licensed
# project, github.com/jellyfin/jellyfin-web) gets built and bundled into
# the image, served as static files at /web/ (cmd/tinyfin/webui.go). Off
# by default: the default image stays MIT-only with zero extra runtime
# dependencies (AGENTS.md principle 2). This Go module never imports or
# links against jellyfin-web's code either way — see
# docs/00-decision-log.md's D9 for why that keeps this a clean MIT/GPL
# boundary rather than a license conflict.
ARG WITH_WEB=false

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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tinyfin /tinyfin
COPY --from=webassets /web /web
VOLUME ["/data"]
EXPOSE 8096
ENTRYPOINT ["/tinyfin"]
CMD ["-data", "/data/tinyfin.db", "-addr", ":8096"]
