# Tinyfin

> A drop-in, wire-compatible reimplementation of the Jellyfin server API, written in Go,
> designed for near-zero idle memory footprint — with an explicit, opt-in path to
> multi-node scale that the reference implementation does not offer.

**Status:** Phases 0–3 are implemented and verified end to end against real Jellyfin clients
(`jellyfin-web` and the official Android app): single-node storage, user/library/browsing API,
Direct Play/Remux/Direct Stream/Transcode via `jellyfin-ffmpeg`, and TMDB-backed metadata
enrichment with a working library scanner. Phase 4 (scale mode — multi-node storage and
session/realtime state) hasn't started. See [`ROADMAP.md`](ROADMAP.md) for the detailed,
phase-by-phase checklist and [`AGENTS.md`](AGENTS.md) for the principles and process governing
further work.
**License:** [MIT](LICENSE). Contributing: [`CONTRIBUTING.md`](CONTRIBUTING.md).

---

## What this is, in one sentence

A media server backend that speaks the same HTTP API as Jellyfin closely enough that existing
Jellyfin clients (official web/mobile/TV apps, the Kodi add-on, Findroid, Infuse, and others)
can point at a Tinyfin instance instead and keep working — while using a fraction of the idle
memory a reference Jellyfin/Emby installation needs, and without hard-coding itself into the
single-node ceiling that the reference implementation, and comparable single-binary/embedded-SQLite
projects such as PocketBase, deliberately accept.

## Running it

### Docker (recommended)

```sh
docker build -t tinyfin --build-arg WITH_WEB=true .
docker run -p 8096:8096 \
  -v ./data:/data \
  -v /path/to/your/media:/media:ro \
  tinyfin
```

(the image's default entrypoint already points `-data` at `/data/tinyfin.db`, matching the volume
above — no extra flags needed for a first run.)

`WITH_WEB=true` bundles a real, unmodified [`jellyfin-web`](https://github.com/jellyfin/jellyfin-web)
build into the image, served at `/web/` — off by default so the base image stays MIT-only with no
GPL code linked in (see [`docs/00-decision-log.md`](docs/00-decision-log.md)'s **D9**). `jellyfin-ffmpeg`
is always bundled regardless (Remux/Direct Stream/Transcode need it unconditionally). To compare
against a reference Jellyfin 12.x server side by side, see
[`scripts/conformance-ref-up.sh`](scripts/conformance-ref-up.sh) and
[`docs/contract/README.md`](docs/contract/README.md).

Once it's up, point any Jellyfin client (the bundled web UI at `/web/`, the official mobile/TV
apps, Kodi's add-on, Findroid, Infuse, ...) at `http://<host>:8096` like it's a normal Jellyfin
server — that's the whole premise.

### From source

```sh
go build -o tinyfin ./cmd/tinyfin
./tinyfin -data tinyfin.db -addr :8096
```

Flags (all optional, see `-h` for the authoritative list):

| Flag | Env var | Default | What it does |
|---|---|---|---|
| `-data` | — | `tinyfin.db` | Path to the SQLite database file |
| `-addr` | — | `:8096` | Address to listen on |
| `-web-dir` | `TINYFIN_WEB_DIR` | `/web` | A built `jellyfin-web` `dist/` to serve at `/web/`; skipped if the directory doesn't exist |
| `-ffmpeg-path` | `TINYFIN_FFMPEG_PATH` | `/usr/lib/jellyfin-ffmpeg/ffmpeg` | `jellyfin-ffmpeg` binary, for Remux/Direct Stream/Transcode (`docs/07`) |
| `-ffprobe-path` | `TINYFIN_FFPROBE_PATH` | `/usr/lib/jellyfin-ffmpeg/ffprobe` | `ffprobe` binary, for the library scanner's media-info extraction (`docs/08`) |
| `-scan-concurrency` | `TINYFIN_SCAN_CONCURRENCY` | `8` | Scan workers per library (I/O bound; raise for remote filesystems) |
| `-scan-probe-concurrency` | `TINYFIN_SCAN_PROBE_CONCURRENCY` | scan concurrency | Max simultaneous `ffprobe` processes across all scans |
| `-scan-library-parallelism` | `TINYFIN_SCAN_LIBRARY_PARALLELISM` | `3` | Libraries scanned at once during a refresh |
| `-scan-force-probe` | `TINYFIN_SCAN_FORCE_PROBE` | off | Re-probe every file even when size and mtime are unchanged |
| `-tmdb-api-key` | `TINYFIN_TMDB_API_KEY` | *(empty)* | Enables TMDB metadata enrichment when set; catalog-only scanning otherwise (`internal/metadata`) |

A library still has to be added and scanned before there's anything to browse — either through a
real client's admin dashboard (`POST /Library/VirtualFolders`, `internal/api/library_admin.go`) or
directly against the API.

## Why this exists

Two independent, well-documented problems with the reference implementation motivate this
project — neither is a rumor, both are acknowledged by the upstream projects themselves:

1. **Idle memory grows with library size, and it's a design choice, not a leak.** Jellyfin's own
   maintainers describe a server going from ~400MB at startup to several GB over normal browsing,
   because the library manager caches aggressively in memory instead of querying the database on
   each request (see `jellyfin/jellyfin` discussion #10444). A design that queries a well-indexed
   store per request instead avoids this class of growth entirely.
2. **A single SQLite file is the whole state store, and SQLite is single-writer.** This is the
   reason Jellyfin cannot run two server processes against the same library and state today, and
   why the same limitation is a deliberate, stated design choice in PocketBase — not a bug to be
   fixed, but a boundary the maintainers chose not to cross. Tinyfin treats this as a decision to
   make explicit and pluggable rather than implicit and permanent.

Neither problem is solved by "just use a bigger machine" once the goal includes leaving a path
open to running more than one node. Tinyfin's premise is that both problems are addressable by
decisions made at the very start of the project — retrofitting them later, after client code and
operators depend on a particular deployment shape, is exactly the position the reference
implementations are in today.

## What this is not

- **Not a fork of Jellyfin.** No Jellyfin source is copied. The server is a clean-room
  implementation of the *publicly observable wire protocol* (HTTP endpoints, headers, JSON
  shapes) — the same relationship Samba has to SMB, or Wine has to the Win32 API. See
  [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md) for how compatibility is defined
  and verified.
- **Not a transcoder.** Tinyfin shells out to [`jellyfin-ffmpeg`](https://github.com/jellyfin/jellyfin-ffmpeg),
  an existing, independently maintained FFmpeg fork with hardware-acceleration and HDR
  tone-mapping patches, as an external subprocess dependency — the same way the reference server
  does. Tinyfin's own code is the decision layer above it (direct play vs. remux vs. transcode,
  command construction, session lifecycle), not a codec implementation. See
  [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md).
- **Not a media organizer.** Cataloging, metadata editing, artwork curation, and multi-source
  metadata resolution are a separate concern, deliberately left to an external cataloging tool
  consumed as an optional metadata provider. Tinyfin's own library scanner stays minimal: enough
  to serve a browsable catalog and hand off enrichment to a provider plugin, never a competing
  cataloging system. See [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md).
- **Not aiming for 100% feature parity.** Live TV/DVR, a generic third-party plugin-loading
  system, DLNA, and offline mobile sync are explicitly out of scope for the initial target — see
  [`docs/01-vision-and-scope.md`](docs/01-vision-and-scope.md) for the reasoning.

## What Tinyfin does and doesn't do

<!-- coverage:start -->
![API routes](docs/coverage/badge-routes.svg) ![contract-verified](docs/coverage/badge-verified.svg)

Measured against the Jellyfin 12.2.0 OpenAPI document (364 operations): **184 registered (51%)**, **142 with a passing contract scenario (39%)**.

| Route status | Count | Non-route features | Count |
|---|---|---|---|
| ✅ supported | 156 | ✅ supported | 61 |
| 🟡 partial | 12 | 🟡 partial | 19 |
| 🗓️ planned | 85 | 🗓️ planned | 13 |
| ⛔ not-planned | 95 | ⛔ not-planned | 17 |
| ↔️ divergent-by-design | 16 | ↔️ divergent-by-design | 0 |

Per controller and per subsystem, with what the reference does and what Tinyfin does: **[docs/COVERAGE.md](docs/COVERAGE.md)**.
<!-- coverage:end -->

## Principles

1. **Idle memory is a budget, not an afterthought.** No full-catalog in-memory cache, ever. Every
   read is a query against a properly indexed store. See
   [`docs/09-memory-and-performance-goals.md`](docs/09-memory-and-performance-goals.md).
2. **Single binary, zero required dependencies, by default.** The default deployment is one Go
   binary with an embedded SQLite database — no separate database process, no cache process, no
   message broker. Anything beyond that is opt-in.
3. **Scale is a mode the operator turns on, not a rewrite.** The storage layer and the session/
   realtime-state layer are each defined behind an interface with two implementations: an
   embedded default and a networked one. Turning on scale mode is a configuration decision, not
   an architecture change. See [`docs/05-storage-drivers.md`](docs/05-storage-drivers.md) and
   [`docs/06-session-and-realtime-state.md`](docs/06-session-and-realtime-state.md).
4. **Wire compatibility is the product.** The value of this project is that unmodified existing
   clients work against it. Any change that breaks that compatibility for convenience is treated
   as a defect, not a trade-off.
5. **Transcoding is delegated, not reinvented.** FFmpeg (via `jellyfin-ffmpeg`) does the codec
   work. Tinyfin owns the decision of *whether* to invoke it and *how*, never the decoding/
   encoding itself.
6. **Decisions are recorded before code is written.** This repository follows a docs-first
   process: open questions live in [`docs/00-decision-log.md`](docs/00-decision-log.md) until
   resolved, then get promoted into the relevant spec document.

## Documentation map

| Doc | Covers |
|---|---|
| [`AGENTS.md`](AGENTS.md) | Entry point for contributors (human or LLM): the principles that don't bend, the clean-room requirement, where everything else lives |
| [`docs/00-decision-log.md`](docs/00-decision-log.md) | Open questions, tracked until resolved |
| [`docs/01-vision-and-scope.md`](docs/01-vision-and-scope.md) | Target use case, explicit non-goals |
| [`docs/02-glossary.md`](docs/02-glossary.md) | Terms used consistently across the other docs |
| [`docs/03-architecture.md`](docs/03-architecture.md) | Process/role layout, how the pieces fit together |
| [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md) | What "compatible with Jellyfin clients" means and how it's verified |
| [`docs/05-storage-drivers.md`](docs/05-storage-drivers.md) | SQLite mode vs. scale mode, and why |
| [`docs/06-session-and-realtime-state.md`](docs/06-session-and-realtime-state.md) | Why storage and session state are two separate pluggable concerns |
| [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md) | Direct Play/Remux/Direct Stream/Transcode, device profiles, worker separation |
| [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md) | Scanner scope, metadata provider interface |
| [`docs/09-memory-and-performance-goals.md`](docs/09-memory-and-performance-goals.md) | Concrete targets and the reference-implementation failure modes they respond to |
| [`docs/10-roadmap.md`](docs/10-roadmap.md) | Phased delivery plan |
| [`ROADMAP.md`](ROADMAP.md) | Checklist of what's actually done, phase by phase |

## Acknowledgements

This project's design is informed by public findings from the Jellyfin project (GitHub
discussions and issues cited throughout `docs/`), the PocketBase project's own stated scaling
philosophy, and the various community projects that distribute Plex transcoding across worker
nodes (`kube-plex`, `ClusterPlex`, `Plex-Remote-Transcoder`, `UnicornTranscoder`) — all of which
independently arrived at "separate the stateless transcode step from the stateful server" as the
only part of this problem that's actually easy.
