# 08 — Library scanning and metadata

## The boundary with MyMediaDB

Tinyfin does not compete with MyMediaDB, and does not grow a competing cataloging system.
MyMediaDB's stated scope is organizing and enriching a personal media library — reading a local
library, extracting technical metadata, pulling external metadata on demand, and letting the
owner's manual edits always win over any external source. That scope is deliberately not widened
to include streaming or transcoding (decision **D3**, `00-decision-log.md`); Tinyfin's own scope
is deliberately not widened to include cataloging.

Concretely: Tinyfin's own scanner is the minimum needed to serve a browsable catalog over the API
surface in `04` — enough to know what files exist, extract technical media info (via `ffprobe`),
and hand off enrichment to whatever metadata provider is configured. It is not a second place to
manage titles, artwork, or overrides.

## Metadata provider interface

A fixed, small contract — not a general-purpose plugin-loading system (see the non-goal in `01`).
A provider is asked for metadata about an identified item and returns what it has; Tinyfin
composes the result into the shape the API surface needs.

- **MyMediaDB** is the first intended provider, consumed as an external metadata source over its
  own API — the same relationship it already has with Jellyfin/Kodi/Plex today, just with Tinyfin
  added to that list of consumers.
- **Direct provider fallback** (TMDB/TVDB queried directly) remains available for a deployment
  that doesn't run MyMediaDB, so Tinyfin is useful standalone — it should not hard-require a
  second project to be minimally functional.

## Scan-time resource discipline

The reference implementation has a documented failure mode here too: unbounded concurrent
`ffprobe` spawning during a library scan has caused out-of-memory conditions on real deployments.
Tinyfin's scanner bounds `ffprobe` (and any other scan-time subprocess) concurrency explicitly,
rather than spawning one per discovered file — a scan taking longer is an acceptable trade-off
against a scan that can exhaust the memory budget this whole project exists to protect (`09`).

## What the scanner does not do

- Does not maintain its own competing "source of truth" for titles, artwork, or user overrides —
  that authority lives wherever the configured metadata provider says it lives (MyMediaDB, if
  configured).
- Does not implement its own scraper integrations beyond the direct-provider fallback above —
  provider-specific scraping logic belongs in the provider, not duplicated in Tinyfin.
- Does not generate trickplay thumbnails as a separate subsystem from the rest of scan-time work
  — see `07` for where trickplay generation sits.
