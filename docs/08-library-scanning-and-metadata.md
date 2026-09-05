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

## Local-vs-remote precedence (validated against the reference server)

When both a local source (a Kodi-style sidecar `.nfo`, folder-convention artwork) and the configured
remote Provider have data for the same item, which one wins was originally a convention this project
adopted from Kodi's own documented `.nfo` semantics, not something checked against the reference
server. Rafael asked for that check explicitly (an explicit request is its own sufficient reason to
read the reference server's C# source, AGENTS.md's clean-room requirement) once a real per-item
refresh feature made the question concrete. Findings, from `jellyfin/jellyfin`'s metadata-refresh
pipeline (extraction only — this describes observed behavior, no code/names/structure carried over):

- **Field-level metadata** (Overview, Genres, ProductionYear, ProviderIDs, ...): the reference server
  gathers local-provider data first, then lets remote providers fill in *only whatever field the local
  pass left empty* — a local value present for a field is never overwritten by a remote one. Whatever
  neither source provides keeps the item's prior stored value (a refresh is never destructive by
  omission). This is exactly for a `MetadataRefreshMode=Default, ReplaceAllMetadata=false` request —
  precisely the query jellyfin-web's dashboard "Refresh metadata" action sends (this doc's `POST
  /Items/{itemId}/Refresh` catalog entry, `docs/11`). **Tinyfin's existing convention (`enrich` runs
  before the local `.nfo` overlay, so the `.nfo`'s own non-empty fields always win, and whatever
  neither source sets is left untouched) matches this exactly** — confirmed, not a coincidence worth
  re-deriving, no code change needed here.
- **Images**: the reference server only lets a remote/provider image *replace* one the item already
  has when the request's `ReplaceAllImages` (or a per-type equivalent) is true; with `ReplaceAllImages
  =false` — again, what jellyfin-web's dashboard action actually sends — an existing image of a given
  type is left alone and the remote provider is never even asked for that type. **Fixed to match**:
  `internal/scan/scan.go`'s `storeProviderImage` now checks whether the item already has an `ImageRef`
  of the Provider result's `Kind` and skips the download/overwrite unless a `replaceImages` flag is
  set, threaded from `POST /Items/{itemId}/Refresh`'s own `ReplaceAllImages` query param
  (`api/item_refresh.go` -> `Server.runItemRefresh` -> `scan.Scanner.RefreshItem`); every `ScanLibrary`
  call site passes `false` (a routine/re-scan never replaces an existing image either, matching the
  reference server's own default). Before this fix, Tinyfin always let a Provider-returned image
  overwrite a local one (same deterministic `ImageRef` ID scheme, `seedSeasonImage`'s doc comment)
  regardless of the flag, because the query param was never read at all — a real divergence, found by
  this same validation pass, not a silent guess that happened to be wrong. Covered by
  `TestRefreshItemImagesRespectReplaceAllImages` (`internal/scan/scan_test.go`).
- **Season-level `.nfo`**: a real user library surfaced a `tvshow.nfo` living inside a `Season NN/`
  folder instead of the series root — a distinct, narrower Kodi convention this project had never
  read at all before. The reference server has a dedicated pair for this
  (`SeriesNfoSeasonProvider`/`SeriesNfoSeasonParser`, extraction only): it looks for `tvshow.nfo` in
  the *season's own folder* first, falling back to the series root if absent there, but extracts
  only a `<namedseason number="N">Custom Name</namedseason>` element matching the season's own
  index — every other field in that file (title, plot, cast, provider IDs, ...) is series-level and
  is silently skipped for a Season entity. **Implemented to match**: `internal/scan/nfo.go`'s new
  `applySeasonNFO`, wired into both a fresh scan (`walkSeasons`) and per-item refresh
  (`RefreshItem`'s `refreshLocalNFO`, Season case) — same lookup order, same narrow `<namedseason>`-
  only extraction (only `item.Name` is overlaid). Covered by `TestScanLibraryAppliesNamedSeason` and
  `TestRefreshItemSeasonAppliesNamedSeason` (`internal/scan/scan_test.go`), the latter also proving
  the season-folder-before-series-root fallback order. Before this, Tinyfin had zero season-level
  `.nfo` support of any kind.

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
