# 00 — Decision log (living document)

> **What this is:** the backlog of foundational decisions that still need to be made or written
> up. This is not a spec — it's the work list. Each item, once decided, turns into text in a spec
> doc (`03` through `09`) and this entry then points at where it landed instead of restating it.
>
> **How this works:** one item at a time. For each item: state the question and the options, then
> record the decision and its destination once it's closed.
>
> **Status values:** `open` · `in discussion` · `decided`.

---

## Foundational decisions closed during initial scoping (pre-repository)

These were settled in the conversation that produced this repository, before any doc existed.
Recorded here for traceability, already promoted to their destination docs.

| ID | Question | Status | Decision | Destination |
|---|---|---|---|---|
| **D1** | Reimplement Jellyfin from scratch, or fork it? | **decided** | Clean-room reimplementation of the wire protocol only. No C# source copied. Rationale: the target (near-zero idle memory, pluggable scale) requires different foundational choices than the reference implementation's architecture allows to retrofit. | `README.md`, `04` |
| **D2** | Reuse or fork `jellyfin-ffmpeg`? | **decided** | Neither — use it unmodified as an external subprocess dependency, exactly as the reference server does. It is already an independently maintained fork of FFmpeg with the hardware-acceleration/HDR patches needed; forking it would duplicate maintenance for no benefit. Tinyfin's own code is the decision layer above it. | `07` |
| **D3** | Should Tinyfin build its own cataloging/metadata system, or consume an external one? | **decided** | Consume, don't build. Cataloging (scanning, metadata resolution, artwork curation) is a separate concern from serving. Tinyfin does not reimplement it — it consumes metadata through a small provider interface, with a direct TMDB/TVDB implementation as the reference provider so it stays useful standalone. | `08` |
| **D4** | Storage: single embedded engine, or pluggable? | **decided** | Pluggable, behind one interface, two implementations: embedded SQLite (default) and a networked scale mode. Rationale: the read-heavy shape of a media server's workload (browsing, images, playback info) fits a single-writer-plus-read-replicas model better than jumping straight to a full multi-writer database, while keeping the default path exactly as light as SQLite alone. | `05` |
| **D5** | Session/realtime state: same interface as storage, or separate? | **decided** | Separate interface. Swapping the database engine does not by itself make in-process session/realtime state (active sessions, SyncPlay groups, live progress) shareable across nodes — this is the specific gap PocketBase's maintainer cited when explaining why adding Postgres support wouldn't make PocketBase horizontally scalable. Default implementation is in-process; scale mode implementation is Redis/Valkey pub-sub. | `06` |
| **D6** | Transcode role: co-located with the API/metadata process, or separate from day one? | **decided** | Separate role from day one, even when both run on the same machine in a single-node deployment. Transcoding is stateless per-stream work (input file + output profile → segments) needing only shared access to source media — the same property every third-party Plex clustering shim (`kube-plex`, `ClusterPlex`, `Plex-Remote-Transcoder`, `UnicornTranscoder`) exploits by retrofitting this split years after the fact. Building it in from the start avoids that retrofit. | `03`, `07` |
| **D7** | License? | **decided** | MIT, matching the rest of the personal FOSS project set. Viable specifically because the wire protocol is implemented clean-room (D1) and `jellyfin-ffmpeg` is invoked as a subprocess, not linked (D2) — neither triggers the reference implementation's or FFmpeg's copyleft terms. | `LICENSE` |
| **D8** | Live TV/DVR, generic third-party plugin loading, DLNA, offline mobile sync — in scope for v1? | **decided** | Out of scope for v1. Each is either low-ROI for the target use case (no TV tuner hardware in the reference deployment) or high-cost relative to benefit (a generic runtime plugin-loading system only pays off if distributing to third-party operators, which is not the initial goal). Revisit if/when the target use case changes. | `01` |

---

## Open items

| ID | Question | Status | Notes |
|---|---|---|---|
| **O1** | Exact API version/commit of `jellyfin/jellyfin` to treat as the compatibility baseline, and how to track upstream drift over time. | open | Needs a concrete pinning strategy — see `04`. |
| **O2** | Device profile database format and initial coverage (which real clients get a profile at launch). | **resolved** | Format: compiled into the binary as a `map[string]DeviceProfile` keyed on the `Client` field parsed from `X-Emby-Authorization` — not a row in `05`'s storage interface, since a profile is static per-release configuration data, not user data, and only ever used as a fallback when a request doesn't carry an inline profile of its own. Unmatched `Client` falls through to one built-in conservative default (narrow Direct Play set, otherwise forced Transcode) instead of failing the request. Initial coverage: Jellyfin Web and the official Jellyfin Android/iOS apps — the clients actually in use against the reference deployment — growing on demand as new clients show up. See `07`. | `07` |
| **O3** | Exact schema for the storage interface (Go interface signature, not just the conceptual boundary already decided in D4). | **resolved** | One `Store` interface, composed of per-concern sub-interfaces (`ItemStore`, `UserStore`, `DeviceStore`, `PlaystateStore`, `ImageStore`, `SubtitleStore`, `LibraryStore`) plus `Tx`, `Healthy`, `Close`. `ItemFilter` requires a `Limit` (rejected if `<=0`) so no caller can query an unbounded catalog, and carries `UserID`/`Played`/`Favorite` for playstate-aware browsing. `ItemStore` gets a generic `ItemRelation` (kind + order) for N:M links — collections, playlists, credits — that a single `ParentID` can't express, matching the pattern `my-media-db` (the cataloging tool `08`'s provider interface was prototyped against) converged on independently. `ImageStore` addresses images as an indexed list per `(item, kind)`, not one-per-kind, matching the reference server's indexed Backdrop/Screenshot images. Revised after cross-checking the initial draft against `my-media-db` and the reference server's published OpenAPI schema (`https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json`): confirmed the playstate-filter and display-preferences designs; added `LyricStore`, `TrickplayStore`, and ID-based addressing on `ItemRelation`/`ImageRef` (a stable ID beats a positional/triple key — concurrent writers, duplicate playlist entries). Struct field shapes stay deferred to implementation time, against `04`'s wire shapes. See `05`. | `05` |
| **O4** | Read-replica replication mechanism for scale mode: build directly on Litestream's approach, or depend on it as a library/sidecar? | open | See `05` for the trade-off as currently understood; not yet a final call. |
| **O5** | Whether `rqlite` is ever brought in as a second scale-mode storage implementation (true multi-writer) or intentionally left out. | open | Deferred — not needed unless a concrete write-heavy scale scenario shows up. |
| **O6** | Go module path and repository hosting (GitHub org/user). | **resolved** | Repository created at `github.com/rafaelvieiras/tinyfin` (private). Module path confirmed as `github.com/rafaelvieiras/tinyfin` once code exists — see `AGENTS.md`. |
