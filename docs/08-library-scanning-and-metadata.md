# 08 — Library scanning and metadata

## The boundary between scanning and cataloging

Tinyfin does not grow a competing cataloging system. Organizing and enriching a media library —
resolving titles, curating artwork, letting manual edits always win over an external source — is
a separate concern from serving that library over an API, and is deliberately not widened into
Tinyfin's own scope (decision **D3**, `00-decision-log.md`).

Concretely: Tinyfin's own scanner is the minimum needed to serve a browsable catalog over the API
surface in `04` — enough to know what files exist, extract technical media info (via `ffprobe`),
and hand off enrichment to whatever metadata provider is configured. It is not a second place to
manage titles, artwork, or overrides.

## Metadata provider interface

A fixed, small contract — not a general-purpose plugin-loading system (see the non-goal in `01`).
A provider is asked for metadata about an identified item and returns what it has; Tinyfin
composes the result into the shape the API surface needs.

- **Direct provider** (TMDB/TVDB queried directly) is the reference implementation of this
  interface, so Tinyfin is useful standalone — it should not hard-require a second project to be
  minimally functional.
- The same interface allows any external cataloging tool to be plugged in as an alternative
  provider, as long as it can answer the interface's metadata queries.
- The interface (`internal/metadata`) is deliberately shaped to survive a later move from an
  in-process Go call to a subprocess-isolated plugin (the `go-plugin` gRPC pattern: any language,
  crash-isolated) without a redesign, ahead of opening the project to outside users — see **D11**
  in `00-decision-log.md` for the specific constraints this imposes and the estimated migration
  cost if they hold.

## Scan-time resource discipline

The reference implementation has a documented failure mode here too: unbounded concurrent
`ffprobe` spawning during a library scan has caused out-of-memory conditions on real deployments.
Tinyfin's scanner bounds `ffprobe` (and any other scan-time subprocess) concurrency explicitly,
rather than spawning one per discovered file — a scan taking longer is an acceptable trade-off
against a scan that can exhaust the memory budget this whole project exists to protect (`09`).

## What the scanner does not do

- Does not maintain its own competing "source of truth" for titles, artwork, or user overrides —
  that authority lives wherever the configured metadata provider says it lives.
- Does not implement its own scraper integrations beyond the direct-provider fallback above —
  provider-specific scraping logic belongs in the provider, not duplicated in Tinyfin.
- Does not generate trickplay thumbnails as a separate subsystem from the rest of scan-time work
  — see `07` for where trickplay generation sits.
