# 10 — Roadmap

Phased so that each phase produces something independently verifiable against the success
criteria in `01`, rather than a single large integration at the end.

## Phase 0 — Foundation documentation (current)

Decisions recorded in `00-decision-log.md` and promoted into `01` through `09`. No code. This
phase is done when the open items in the decision log that block starting implementation (O2, O3
at minimum) are resolved.

## Phase 1 — Compatibility core, single-node, embedded storage only

- Embedded SQLite storage driver, no scale mode yet.
- API/metadata role: library browsing, users, sessions, images, subtitles — the "library and item
  navigation" and "users, sessions, devices" groups from `04`.
- No transcoding yet — Direct Play only, against a small, hand-picked set of device profiles
  (open item O2).
- Goal: a real Jellyfin client (pick one — the reference web client is the natural first target)
  browses a real library and plays a Direct-Play-compatible file end to end.

## Phase 2 — Transcoding pipeline

- Transcode worker role, `jellyfin-ffmpeg` integration, the full Direct Play/Remux/Direct
  Stream/Transcode decision described in `07`.
- Still single-node; the worker role exists as a separate internal component but is not yet
  exercised across machines.
- Goal: a client whose device profile forces a transcode gets a correct HLS stream.

## Phase 3 — Metadata provider interface

- Provider interface from `08`, MyMediaDB as the first real provider, direct-provider fallback
  (TMDB/TVDB) as the no-second-project path.
- Goal: a freshly scanned library gets enriched metadata without Tinyfin implementing its own
  scraping.

## Phase 4 — Scale mode

- Scale-mode storage driver (write leader + streaming read replicas, `05`).
- Scale-mode session/realtime state (Redis/Valkey, `06`).
- Transcode worker role actually split onto a separate machine from the API/metadata role,
  exercising the separation that's existed architecturally since Phase 2.
- Goal: two or more API/metadata nodes serve reads correctly against one write leader, and a
  client's session survives being routed to a different node (or sticky routing is documented as
  the accepted alternative — see `06`).

## Deliberately not phased in yet

Live TV/DVR, a generic plugin-loading system, DLNA, offline mobile sync — see the non-goals in
`01`. Not scheduled because they're not part of the premise this project is testing; revisit only
if the target use case changes.
