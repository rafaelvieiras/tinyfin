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
server. The maintainer asked for that check explicitly (an explicit request is its own sufficient reason to
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

## Item IDs are scoped per library

`internal/scan/id.go`'s `deterministicID` hashes a stable seed into a 32-hex item ID so that
re-scanning the same file upserts the same row instead of duplicating it. Every seed (movie, series,
season, episode — the four call sites in `scan.go`) now includes the scanning library's own ID, not
just the file's path.

This was found live on a test deployment, not derived on paper: a leftover manually-created test library
happened to point at the same host folder as a real movie library. Before this fix, the seed
was path-only, and `reconcileItemID`'s existing-row lookup (the mechanism that lets a changed ID
formula reconcile in place rather than duplicate, see its own doc comment) was scoped to *the
library currently being scanned* — so it could never see that the other library had already claimed
that path. Both libraries' scans minted the identical ID for the identical file, and `UpsertItem`'s
`ON CONFLICT(id)` let whichever scan wrote last silently reassign that row's `LibraryID`. Because
`Server.refreshAllLibraries` ("Scan All Libraries") launches one goroutine per library concurrently,
this became a race, nondeterministically splitting one library's real catalog across both — the
exact symptom observed (a partial movie library, with the missing titles showing up under
the leftover test library instead). Folding `LibraryID` into the hash means two libraries with
overlapping `Paths` now each mint their own distinct ID for the same physical file, so there is
nothing left to race over. Covered by `TestScanLibraryOverlappingPathsDoNotCollide`
(`internal/scan/scan_test.go`).

## `date_created` is set once and never touched again

Every scan pass walks *every* item found on disk, not just newly discovered files, and every one
of them goes through `UpsertItem`. Before this fix, `UpsertItem`'s `ON CONFLICT(id) DO UPDATE SET`
clause included `date_created=excluded.date_created` like every other column, and `scan.go`'s
`processMovie`/`walkSeries`/`walkSeasons`/`processEpisode` all unconditionally stamped
`DateCreated: time.Now().UTC()` on the `storage.Item` they built — including for a file that was
already cataloged. The result: every full/scheduled rescan silently reset every pre-existing item's
"date added" to that scan's own wall-clock time, in filesystem-traversal order. Since
`/Users/{userId}/Items/Latest` (`handleItemsLatest`, `internal/api/library.go`) sorts by
`date_created DESC`, "recently added" ended up reflecting scan/traversal order instead of true
historical add order — found by comparing Tinyfin's ordering against the reference server's.

Real Jellyfin never re-stamps an item's add date on a rescan (only genuine first discovery sets it);
Tinyfin's single-item `RefreshItem` path already matched this (it loads the existing row via
`GetItem` first and only mutates enrichment fields), but the full-library scan path never did.
Fixed centrally instead of duplicating a "look up the existing row first" step at every scan.go call
site: `date_created` is no longer part of `UpsertItem`'s `DO UPDATE SET` list at all
(`internal/storage/sqlite/item.go`), so a conflicting row's stored `date_created` always wins
regardless of what the caller passes — only a genuinely new row takes the value passed in (or
`time.Now()`, if the caller left it zero). Covered by `TestUpsertItemPreservesDateCreated`
(`internal/storage/sqlite/store_test.go`).

Note: this fix only stops *future* corruption — items already miscataloged by prior rescans
keep their now-wrong `date_created` until independently repaired (a rescan will not self-heal it,
since the row already exists and the new code path deliberately leaves an existing row's
`date_created` alone).

### A new item's `date_created` comes from the file, not the scan clock

Fixing the overwrite-on-rescan bug above exposed the next layer of the same symptom: even for a
*brand-new* row, every `scan.go` call site stamped `DateCreated: time.Now().UTC()` — the moment the
scan's goroutine happened to reach that file, not when the file was actually placed in the library.
The question of whether the reference server does the same arose while re-testing the previous fix by
deleting and rescanning a whole library from scratch: every item in that fresh scan ended up with
nearly identical `date_created` values (all within the same second), which is exactly the
"traversal order, not real add order" symptom this whole section is about — just moved one step
earlier, from "every rescan" to "every fresh scan of an existing collection."

Read (extraction only, no code carried over — this project's clean-room rule for
`jellyfin/jellyfin`) confirms the reference server does not do this: a newly resolved item's
`DateCreated` is seeded from the video file's own filesystem timestamp by default
(`UseFileCreationTimeForDateAdded`, on by default), falling back to the scan's wall-clock time only
when that timestamp is unavailable. **Matched, with one platform-forced substitution**:
`internal/scan/scan.go`'s new `fileDateAdded(path)` helper stats the file/folder and uses its
modification time, not creation time — Go's `os.Stat` cannot portably read a file's *creation* time
on Linux (common filesystems like ext4 don't expose "birth time" via the standard stat syscall; the
reference server has this same platform gap on Linux, per its own source). mtime is the closest
reliably-available proxy for "when this file was added" on every platform Tinyfin runs on. Used at
all four call sites that mint a new item (`processMovie`, `walkSeries`, `walkSeasons`,
`processEpisode`) — harmless for an *existing* row either way, since the `UpsertItem` fix above
already ignores whatever `DateCreated` a caller passes once a row exists. Covered by
`TestScanLibraryMovieDateCreatedFollowsFileModTime` (`internal/scan/scan_test.go`), which writes two
movies in the opposite order of their mtimes and asserts `date_created`-descending order follows the
mtimes, not the filenames/traversal order.

## Scan-time resource discipline

The reference implementation has a documented failure mode here too: unbounded concurrent
`ffprobe` spawning during a library scan has caused out-of-memory conditions on real deployments.
Tinyfin's scanner bounds `ffprobe` (and any other scan-time subprocess) concurrency explicitly,
rather than spawning one per discovered file — a scan taking longer is an acceptable trade-off
against a scan that can exhaust the memory budget this whole project exists to protect (`09`).

### Scan cost on slow (remote) filesystems

Operational behavior, not part of the API contract (nothing observable changes in a response):

- **Incremental probe.** After a successful `ffprobe`, the item's file size and mtime are stored
  (`scan_signatures`). A later scan whose file still has the same size and mtime reuses the item's
  stored `MediaStreams` and `RunTimeTicks` and does not run `ffprobe`. A changed file, an item with
  no stored streams/duration (e.g. a failed earlier probe) or a missing signature is probed again.
  `-scan-force-probe` / `TINYFIN_SCAN_FORCE_PROBE` disables the skip.
- **Cheaper probe.** `ffprobe` runs with `-probesize 5000000 -analyzeduration 5000000` (FFmpeg's own
  defaults, pinned) and a 3 minute timeout; the output fields used are unchanged.
- **Concurrency.** Scan workers per library default to 8 (`-scan-concurrency` /
  `TINYFIN_SCAN_CONCURRENCY`). Simultaneous `ffprobe` processes across all scans are bounded
  separately (`-scan-probe-concurrency` / `TINYFIN_SCAN_PROBE_CONCURRENCY`, default = scan
  concurrency). A refresh scans up to 3 libraries at once (`-scan-library-parallelism` /
  `TINYFIN_SCAN_LIBRARY_PARALLELISM`).
- **Sidecar images** are detected from one directory listing per folder per pass instead of one
  `stat` per candidate file name.
- Each library scan logs items, probed vs skipped counts and duration.

## Library kinds and file layouts the scanner reads

The observable behavior is specified in `docs/contract/specs/items.md` (rules `items.names`,
`items.runtime`, `items.episodes`, `items.homevideos`); this section only records where it lives.

- **`movies`**: a folder that holds one movie (a file, its stacked parts or versions) is that
  movie, named after the folder as written; other folders become `Folder` items and their files
  movies named after the file with the year and release tokens cut (`walkMovieDir`, `CleanTitle`).
  **`tvshows`**: series folder; a folder named like a season is that season, any other folder a season
  named after it, extras folders are skipped (`walkSeasons`, `SkippedFolder`). An episode file needs no
  `SxxExx`: numbers, date and name come from `ParseEpisodeFile`, the `.nfo` (several documents merge,
  `mergeEpisodeDocs`) and the folder it sits in; files in the series folder join the season of their
  number or the first unnumbered season or a season created for them (`seasonSet`).
- **`homevideos`**: every folder under the library path becomes a `Folder` item (a `PhotoAlbum` when it
  directly holds pictures) and every video file a `Video` item (`walkHomeVideos`), with no name
  parsing and no provider lookup; picture files are `Photo` items.
- Text from `.nfo` files is trimmed of surrounding white space when stored (`applyCommonNFOFields`);
  a series takes its runtime from `<runtime>` of `tvshow.nfo` only.

## What the scanner does not do

- Does not maintain its own competing "source of truth" for titles, artwork, or user overrides —
  that authority lives wherever the configured metadata provider says it lives.
- Does not implement its own scraper integrations beyond the direct-provider fallback above —
  provider-specific scraping logic belongs in the provider, not duplicated in Tinyfin.
- Trickplay thumbnails are a separate background job that follows a scan (`internal/trickplay`,
  see `07`), not part of the scan pass itself.
