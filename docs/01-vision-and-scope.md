# 01 — Vision and scope

## Target use case

A self-hosted media server for a home/small-scale deployment where:

- The library is video-first (movies, TV series), already organized and enriched by a separate
  cataloging tool (MyMediaDB or equivalent) rather than by Tinyfin itself.
- Access is primarily LAN plus a handful of remote/family users — not a public, unbounded-signup
  service.
- The operator cares about the server's own resource footprint independently of transcoding load:
  a server that spends 2GB of idle RAM holding a browsable list of a few thousand items in memory
  is doing unnecessary work, regardless of how few concurrent streams it serves.
- The operator wants the *option*, not the requirement, to run more than one node later — for
  read scaling, for redundancy, or because the deployment genuinely grew — without that option
  being foreclosed by decisions baked in at day one.
- Existing client apps (phones, TVs, Kodi boxes) should not need to be swapped or reconfigured
  beyond pointing them at a different server address.

## In scope for v1

- Full library browsing surface compatible with real Jellyfin clients: items, collections,
  playlists, favorites, search, images, subtitles.
- Playback negotiation and delivery: Direct Play, Remux, Direct Stream, full Transcode, HLS
  packaging, resume/watched state.
- Multi-user accounts with basic parental controls (PIN, rating-based restriction).
- Metadata via a pluggable provider interface, with MyMediaDB as the first real provider.
- Two storage modes (embedded SQLite, networked scale mode) and two session-state modes
  (in-process, Redis/Valkey) — see `05` and `06`.
- A transcode worker role that is architecturally separate from the API/metadata role from the
  first commit, regardless of whether a given deployment runs them on one machine or several.

## Explicitly out of scope for v1

Each of these is a deliberate deferral, not an oversight — see decision **D8** in
[`00-decision-log.md`](00-decision-log.md):

- **Live TV / DVR** (tuner integration, EPG, scheduled recording). Isolated feature, requires
  hardware the target deployment doesn't have, and doesn't interact with anything else in this
  document.
- **A generic third-party plugin-loading system.** Only pays for its own complexity if the goal
  is distributing Tinyfin to operators who need extensibility Tinyfin's maintainers didn't
  anticipate. The target use case doesn't need that yet. The metadata *provider* interface
  (`08`) is not this — it's a small, fixed contract for a known kind of extension, not a runtime
  plugin loader.
- **DLNA.**
- **Offline/mobile sync (downloading media into a client app for offline playback).**

None of these are ruled out forever — they're ruled out for the version that proves the core
premise (wire-compatible, low-idle-memory, scale-optional) works before spending effort on
breadth.

## Success criteria

A deployment is a success if:

1. An existing Jellyfin client, pointed at a Tinyfin server instead, browses the library and
   plays back media without modification or a compatibility shim on the client side.
2. Idle memory usage does not grow with library size the way the reference implementation's does
   (see `09` for the concrete target and the failure mode it responds to).
3. An operator can go from SQLite mode to scale mode by changing configuration, not by migrating
   data through a bespoke one-off process or rewriting client-facing behavior.
