<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/logo.svg">
    <img alt="Tinyfin" src="assets/logo.svg" width="320">
  </picture>
</p>

<p align="center">
  A Jellyfin-compatible media server in one Go binary.<br>
  Same API. Same clients. A fraction of the idle memory.
</p>

<p align="center">
  <a href="https://github.com/rafaelvieiras/tinyfin/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/rafaelvieiras/tinyfin/ci.yml?branch=main&label=CI"></a>
  <a href="https://github.com/rafaelvieiras/tinyfin/releases"><img alt="Release" src="https://img.shields.io/github/v/release/rafaelvieiras/tinyfin"></a>
  <a href="https://github.com/rafaelvieiras/tinyfin/pkgs/container/tinyfin"><img alt="Image" src="https://img.shields.io/badge/ghcr.io-tinyfin-blue?logo=docker&logoColor=white"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/rafaelvieiras/tinyfin"></a>
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/rafaelvieiras/tinyfin">
</p>

---

Tinyfin speaks the Jellyfin HTTP API. Point an unchanged Jellyfin client at it and the client
keeps working: the web UI, the mobile and TV apps, the Kodi add-on, Findroid, Infuse. The state
lives in one SQLite file. Reads are database queries, not an in-memory catalog, so idle memory
does not grow with library size. Multi-node scale is a mode you turn on later, not a rewrite.

This is a clean-room implementation. No Jellyfin source code is copied. Tinyfin implements the
publicly observable wire protocol, the same way Samba implements SMB.

## What you get

| | |
|---|---|
| **Wire compatible** | 203 of 364 Jellyfin 12.2 API operations are registered, 171 of them verified by an executable contract. See [the coverage panel](docs/COVERAGE.md). |
| **Low idle memory** | No full-catalog cache. Every read is an indexed SQLite query. |
| **One binary** | Embedded SQLite. No separate database, cache or message broker to run. |
| **Real transcoding** | Shells out to [`jellyfin-ffmpeg`](https://github.com/jellyfin/jellyfin-ffmpeg), the same fork the reference server uses. Bundled in the image. |
| **Loads Jellyfin data** | Reads Jellyfin naming, NFO files, artwork and `<set>` collections. Item IDs that clients already store keep working. |

## What it does not do

- **No copy of Jellyfin code.** Compatibility comes from observing the wire protocol, not from
  porting source. See [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md).
- **No transcoder of its own.** `jellyfin-ffmpeg` does the codec work. Tinyfin decides whether to
  remux, direct stream or transcode, and builds the command.
- **No media organizer.** Cataloging, artwork curation and multi-source metadata belong to an
  external tool used as an optional provider.
- **No Live TV, DVR, DLNA, plugin loader or offline sync.** Out of scope for now.

## Quick start

Two images are published from this repository. Pick the one you need.

| Image | Web UI | License of what you download |
|---|---|---|
| `ghcr.io/rafaelvieiras/tinyfin:latest` | no | MIT only |
| `ghcr.io/rafaelvieiras/tinyfin-web:latest` | yes, at `/web/` | MIT plus the GPL-2.0 `jellyfin-web` build |

Both are multi-arch (`linux/amd64`, `linux/arm64`) and both bundle `jellyfin-ffmpeg`.

```sh
docker run -d --name tinyfin -p 8096:8096 \
  -v ./data:/data \
  -v /path/to/media:/media:ro \
  ghcr.io/rafaelvieiras/tinyfin-web:latest
```

Open `http://<host>:8096/web/` and finish the first-run wizard. Create the admin user, then add
your libraries. A Jellyfin client that points at `http://<host>:8096` finds a normal server.

The container runs as uid `65532`. Give the data directory to that user first:

```sh
mkdir -p data && sudo chown 65532:65532 data
```

The same applies to the `./data` bind in `compose.yaml`. Without it the container stops at start
with `unable to open database file`.

Prefer Compose? Copy [`.env.example`](.env.example) to `.env`, edit it, then run
`docker compose up -d`.

### From source

```sh
go build -o tinyfin ./cmd/tinyfin
./tinyfin -data tinyfin.db -addr :8096
```

### Custom image

The two published images differ by one build argument:

```sh
docker build -t tinyfin --build-arg WITH_WEB=true .
```

`WITH_WEB=false` is the default. It keeps the image MIT-only and adds no runtime dependency. The
Go module never imports `jellyfin-web` either way. The boundary is recorded as decision **D9** in
[`docs/00-decision-log.md`](docs/00-decision-log.md).

## Configuration

Every flag has a default. `-h` prints the authoritative list.

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `-data` | — | `tinyfin.db` | SQLite database file |
| `-addr` | — | `:8096` | Listen address |
| `-web-dir` | `TINYFIN_WEB_DIR` | `/web` | Built `jellyfin-web` `dist/` to serve at `/web/`. Skipped when the directory is absent |
| `-ffmpeg-path` | `TINYFIN_FFMPEG_PATH` | `/usr/lib/jellyfin-ffmpeg/ffmpeg` | Binary used for remux, direct stream and transcode |
| `-ffprobe-path` | `TINYFIN_FFPROBE_PATH` | `/usr/lib/jellyfin-ffmpeg/ffprobe` | Binary used to read media info during a scan |
| `-scan-concurrency` | `TINYFIN_SCAN_CONCURRENCY` | `8` | Scan workers per library. Raise it for a remote filesystem |
| `-scan-probe-concurrency` | `TINYFIN_SCAN_PROBE_CONCURRENCY` | scan concurrency | Maximum `ffprobe` processes at once |
| `-scan-library-parallelism` | `TINYFIN_SCAN_LIBRARY_PARALLELISM` | `3` | Libraries scanned at once |
| `-scan-force-probe` | `TINYFIN_SCAN_FORCE_PROBE` | off | Probe every file again, even when size and mtime did not change |
| `-tmdb-api-key` | `TINYFIN_TMDB_API_KEY` | empty | Turns on TMDB enrichment. Without it the scan is catalog-only |

## How it works

1. **Scan.** The scanner walks each library, reads NFO and artwork, and asks `ffprobe` for media
   info. It skips a file when size and mtime match the last scan.
2. **Store.** Items, users, sessions and playback state go into one SQLite file. Storage sits
   behind an interface, so a networked driver can replace the embedded one.
3. **Serve.** The API answers the requests a Jellyfin client sends. Playback negotiation picks
   direct play, remux, direct stream or transcode, then hands the job to `jellyfin-ffmpeg`.

Design and rules of the road are in [`AGENTS.md`](AGENTS.md). The reasoning behind each decision
is in [`docs/`](docs/).

## Status

> [!NOTE]
> Release `0.x`. Phases 0 to 3 of the roadmap are done and were tested against real clients:
> `jellyfin-web` and the official Android app. Phase 4, the multi-node scale mode, has not
> started.

Coverage is honest, not complete. Check [`docs/COVERAGE.md`](docs/COVERAGE.md) before you rely on
a feature. Out of scope for 0.x, by decision **D8**: Live TV and DVR, DLNA, offline mobile sync
and a generic plugin loader. Multi-node scale mode is the unstarted phase 4. Metadata enrichment
needs a TMDB key; without one the scanner only catalogues files. Every deliberate difference from
the reference is listed in [`docs/13-divergences.md`](docs/13-divergences.md), and known issues are
in [`CHANGELOG.md`](CHANGELOG.md).

<!-- coverage:start -->
![API routes](docs/coverage/badge-routes.svg) ![contract-verified](docs/coverage/badge-verified.svg)

Measured against the Jellyfin 12.2.0 OpenAPI document (364 operations): **206 registered (57%)**, **174 with a passing contract scenario (48%)**.

| Route status | Count | Non-route features | Count |
|---|---|---|---|
| ✅ supported | 178 | ✅ supported | 64 |
| 🟡 partial | 12 | 🟡 partial | 19 |
| 🗓️ planned | 67 | 🗓️ planned | 13 |
| ⛔ not-planned | 91 | ⛔ not-planned | 16 |
| ↔️ divergent-by-design | 16 | ↔️ divergent-by-design | 0 |

Per controller and per subsystem, with what the reference does and what Tinyfin does: **[docs/COVERAGE.md](docs/COVERAGE.md)**.
<!-- coverage:end -->

## Documentation

| Document | Content |
|---|---|
| [`AGENTS.md`](AGENTS.md) | Principles, clean-room rule, where each thing lives |
| [`ROADMAP.md`](ROADMAP.md) | What is done, phase by phase |
| [`docs/00-decision-log.md`](docs/00-decision-log.md) | Open questions, tracked until resolved |
| [`docs/01-vision-and-scope.md`](docs/01-vision-and-scope.md) | Target use case and non-goals |
| [`docs/02-glossary.md`](docs/02-glossary.md) | Terms used across the other documents |
| [`docs/03-architecture.md`](docs/03-architecture.md) | Processes, roles and how the parts fit |
| [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md) | What compatibility means and how it is verified |
| [`docs/05-storage-drivers.md`](docs/05-storage-drivers.md) | SQLite mode and scale mode |
| [`docs/06-session-and-realtime-state.md`](docs/06-session-and-realtime-state.md) | Why storage and session state are separate |
| [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md) | Playback modes, device profiles, worker split |
| [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md) | Scanner scope and provider interface |
| [`docs/09-memory-and-performance-goals.md`](docs/09-memory-and-performance-goals.md) | Targets and the failure modes they answer |
| [`docs/12-clean-room-behavior-specs.md`](docs/12-clean-room-behavior-specs.md) | Behavioral rules the contract enforces |
| [`docs/13-divergences.md`](docs/13-divergences.md) | Every deliberate difference from the reference |
| [`docs/COVERAGE.md`](docs/COVERAGE.md) | Route by route, feature by feature |
| [`docs/contract/README.md`](docs/contract/README.md) | Executable contract: specs, scenarios and runner |

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) first. The clean-room rule is not negotiable: describe
behavior by what a client can observe, never by copying Jellyfin source.

Bug reports and feature requests go to [issues](https://github.com/rafaelvieiras/tinyfin/issues).
Security reports follow [`SECURITY.md`](SECURITY.md).

## Acknowledgements

The design leans on public findings from the Jellyfin project, the PocketBase scaling philosophy,
and the community projects that spread Plex transcoding over worker nodes: `kube-plex`,
`ClusterPlex`, `Plex-Remote-Transcoder` and `UnicornTranscoder`. All of them reached the same
conclusion: separate the stateless transcode step from the stateful server.

## License

[MIT](LICENSE). The `jellyfin-web` build that `tinyfin-web` bundles keeps its own GPL-2.0 license.
