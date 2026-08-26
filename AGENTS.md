# AGENTS.md — guidance for agents (human and LLM)

> Read this before writing code. This is the entry point: the principles that don't bend, the
> operational practices that avoid rework, and where to go next. This file **points to**
> authoritative sources — it does not duplicate them. When something here disagrees with a
> doc/decision-log entry, the specific source wins and this file should be corrected.

## What the project is, in one sentence

Tinyfin is a **wire-compatible reimplementation of the Jellyfin server API** in Go, built so that
existing Jellyfin clients keep working unmodified while idle memory usage stays near-zero and a
path to multi-node scale exists without being mandatory. Module:
`github.com/rafaelvieiras/tinyfin` (once code exists — currently docs-only).

## Principles that don't bend

All six are in the [README](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md):

1. **Idle memory is a budget, not an afterthought.** No full-catalog in-memory cache, ever. Every
   read is a query against a properly indexed embedded store (SQLite, FTS5 + generated columns).
   This directly targets a documented, root-caused failure
   mode in the reference implementation (`jellyfin/jellyfin` discussion #10444) — an
   aggressive non-critical in-memory cache is why a reference server grows from ~400MB to several
   GB over normal use. Do not reintroduce that pattern here for "performance."
2. **Single binary, zero required dependencies, by default.** `tinyfin` with an embedded SQLite
   file must be a complete, working deployment. Anything that requires a second process
   (Postgres, Redis/Valkey, a message broker) is opt-in scale-mode configuration, never a
   default requirement.
3. **Scale is a mode, not a rewrite.** Storage ([`docs/05`](docs/05-storage-drivers.md)) and
   session/realtime state ([`docs/06`](docs/06-session-and-realtime-state.md)) are each behind an
   interface with an embedded default and a networked implementation. These are **two separate
   interfaces** — do not conflate them. Swapping the database engine alone does not make the
   application horizontally scalable; PocketBase's own maintainer made this exact point when
   explaining why Postgres support wouldn't fix PocketBase's single-node ceiling (no
   synchronization mechanism for in-process event/session state). Tinyfin exists specifically to
   not repeat that gap.
4. **Wire compatibility is the product.** The only reason this project has value over writing a
   from-scratch media server is that unmodified Jellyfin clients (web, mobile, TV apps, the Kodi
   add-on, Findroid, Infuse) work against it. See [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md)
   for what "compatible" means precisely and how it's checked. A change that improves internal
   code cleanliness at the cost of diverging from observed client-facing behavior is a bug, full
   stop — file it as one.
5. **Transcoding is delegated, not reinvented.** [`jellyfin-ffmpeg`](https://github.com/jellyfin/jellyfin-ffmpeg)
   is an external, independently-maintained subprocess dependency — invoked, never forked, never
   vendored, never linked. Tinyfin owns exactly one thing here: the decision layer (Direct
   Play/Remux/Direct Stream/Transcode selection against a device profile) and the command/session
   lifecycle around it. See [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md).
6. **No cataloging logic here.** Scanning, metadata resolution, artwork curation belong to an
   external cataloging tool, consumed through a provider interface. Do not grow Tinyfin's own
   scanner into a competing cataloging system — see
   [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md) for the
   boundary.

## Clean-room requirement

Tinyfin implements the Jellyfin *wire protocol* (HTTP routes, headers, JSON shapes) as observed
from the outside — client behavior, public API documentation, and the OpenAPI surface. **Never**
copy code from the `jellyfin/jellyfin` C# source tree into this repository, not even as a
reference kept in a comment. Behavior gets derived from external, black-box observation (real
client traffic, published API docs) or reimplemented from first principles against a written
spec in `docs/`. This boundary is what keeps the MIT license clean; do not blur it for
convenience.

## Decisions-first workflow

- Open questions live in [`docs/00-decision-log.md`](docs/00-decision-log.md) until resolved.
- A resolved decision gets promoted into the relevant spec doc (`03` through `09`); the log entry
  then references where it landed instead of restating it.
- Don't make a structural call (storage interface shape, API route behavior, transcode pipeline
  contract) inside a PR without it first passing through the log — see
  [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Where to go next

| Question | Doc |
|---|---|
| What's actually in scope for v1, what's deliberately deferred? | [`docs/01-vision-and-scope.md`](docs/01-vision-and-scope.md) |
| What does a given term mean in this project specifically? | [`docs/02-glossary.md`](docs/02-glossary.md) |
| How do the processes/roles fit together? | [`docs/03-architecture.md`](docs/03-architecture.md) |
| What API surface, and how is compatibility verified? | [`docs/04-api-compatibility.md`](docs/04-api-compatibility.md) |
| SQLite mode vs. scale mode — the actual interface | [`docs/05-storage-drivers.md`](docs/05-storage-drivers.md) |
| Session/realtime state — why it's separate from storage | [`docs/06-session-and-realtime-state.md`](docs/06-session-and-realtime-state.md) |
| Direct Play/Remux/Direct Stream/Transcode, device profiles, worker separation | [`docs/07-transcoding-pipeline.md`](docs/07-transcoding-pipeline.md) |
| Scanner scope and the metadata provider boundary | [`docs/08-library-scanning-and-metadata.md`](docs/08-library-scanning-and-metadata.md) |
| Concrete memory/performance targets and why | [`docs/09-memory-and-performance-goals.md`](docs/09-memory-and-performance-goals.md) |
| Delivery order | [`docs/10-roadmap.md`](docs/10-roadmap.md) |
