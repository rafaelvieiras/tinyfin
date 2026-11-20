# 11 — jellyfin-web route coverage

## What this is

A ground-truth inventory of every HTTP route jellyfin-web (the reference web client) actually
calls or can call, cross-referenced against what Tinyfin implements today (`internal/api/routing.go`,
`internal/api/server.go`). This is a planning artifact, not a spec — it exists so the project can
decide what to build next with real usage data instead of guessing from the OpenAPI surface alone.
Nothing here was implemented as part of producing this document; see `docs/04-api-compatibility.md`
for what "compatible" means and how it's normally verified.

## Method

Four sources, combined:

1. **Static analysis of the embedded `@jellyfin/sdk` bundle** (`node_modules.@jellyfin.sdk.bundle.js`,
   extracted from a Tinyfin container's own `/web`) — every URL-template string literal
   in the bundle, ~250 distinct path patterns. This is the full catalog of routes the modern SDK
   client code *can* call, tree-shaken to what jellyfin-web actually imports.
2. **Static analysis of the embedded legacy `jellyfin-apiclient` bundle** — this package builds URLs
   by string concatenation rather than template literals, so it doesn't show up in (1); grepped for
   path-suffix literals instead (`/InstantMix`, `/FavoriteItems/`, `/Refresh`, etc.) to find routes
   the SDK-bundle scan would otherwise miss entirely.
3. **Live network capture (Playwright)** clicking through home, movie library, movie detail and
   playback, TV series → season → episode, search, favorite/watched toggles — run against **both**
   a reference Jellyfin server (official image) and Tinyfin itself.
4. **Reading jellyfin-web's own source** (`github.com/jellyfin/jellyfin-web`, GPL-2.0 — a separate
   client project, not the reference *server*, so none of AGENTS.md's clean-room restriction on the
   `jellyfin/jellyfin` C# source applies; see AGENTS.md's "Reading jellyfin-web (the client)"
   section) for the component behind any interaction being catalogued: what precondition gates a
   button firing a request at all, and which response *field values* (not just shapes) a later
   lookup depends on. Sources (1)-(3) alone can miss both — a client-side guard that blocks a
   request produces no network traffic to capture, and a `200` response with the right shape but a
   wrong field value looks identical to a correct one under status-code/shape diffing. This is now
   routine for any row involving a click or a conditional render, not just a fallback for cases (1)-(3)
   couldn't explain — see the `POST /ScheduledTasks/Running/{taskId}` row below for the case that
   surfaced the gap: `GET /ScheduledTasks`'s `Key` field had the right shape and a `200` status, but
   the wrong *value*, which is exactly what (1)-(3) can't distinguish from correct.

Source (3) surfaced something source (1)/(2) alone couldn't: **the jellyfin-web build embedded in
Tinyfin's own image calls a materially different set of URL shapes than the one embedded in the
official reference image** for several common operations (list browsing, resume, favorites, watched
marking) — one build's legacy `jellyfin-apiclient` code path favors per-user URL prefixes
(`/Users/{userId}/Items/Resume`), the other favors query-param form (`/UserItems/Resume?userId=...`).
Where the two builds disagree, **this doc treats what Tinyfin's own embedded bundle actually sends as
authoritative** for status determination below — that's the literal client this server has to satisfy
on day one — and calls out the alternate legacy form in a note where it matters, since a different
jellyfin-web release (or a non-web client built against the older `jellyfin-apiclient` package) could
still send it. This split is itself worth a decision-log entry eventually (relates to open item **O1**,
`docs/00-decision-log.md`) — not resolved here, just surfaced.

Status legend:

- ✅ **Implemented** — registered in `server.go`, returns real data derived from the catalog.
- ⚠️ **Partial** — registered and returns `200`, but is a hardcoded honest-empty stub (no backing
  data model yet) or an under-permissioned admin route. Confirmed by reading the handler bodies in
  `internal/api/library.go`, not just guessed from behavior.
- ❌ **Not implemented** — no route registered; a real client request 404s (confirmed live for most
  entries below via the Playwright capture against Tinyfin; a smaller number are catalog-only,
  called out explicitly where live confirmation wasn't obtained).

## Summary

| | Count |
|---|---|
| Route entries catalogued below (excluding Live TV/Channels, out of scope per `docs/01` — `GET /LiveTv/Programs` is the one exception, counted here, see "Explicitly out of scope" below) — some entries group several closely-related paths on one row, so this counts table rows, not raw path-template strings | 109 |
| ✅ Implemented | 59 |
| ⚠️ Parcial (stub/honest-empty) | 5 |
| ❌ Not implemented | 45 |

64 of 109 (59%) return real or honestly-empty data today. The gaps cluster heavily in a few places
that matter for ordinary browsing, not just admin/edge-case features — see "Biggest real gaps" at the
bottom. The `/dashboard/libraries` admin flow added 9 rows and initially downgraded one
(`Library/VirtualFolders`, ✅→⚠️) in a dedicated mapping/diff pass; a follow-up implementation pass
then closed every gap that pass found — see "Dashboard — Libraries (admin library management)". A
later pass over `/dashboard` itself (the admin overview page) found and closed six more route gaps
plus one previously-hidden auth-header decoding bug — see "Dashboard — Overview". These
counts are recounted directly from the tables below (`grep`-counted status markers), not
hand-tracked incrementally across passes — the project's established practice after an earlier
count drifted from the table rows it was supposed to summarize.

---

## Library and item navigation

| Route | Status | Note |
|---|---|---|
| `GET /Users/{userId}/Views` | ✅ | The legacy form of the route below — the path the *published* SDK's `getUserViews` builds, even though the 12.2 bundle asks `/UserViews?userId=`. Served with the same body and the same order, `403` for another user's views and `404` for an identifier that names nobody (`items.views.R1`); before this it was a `404` to anyone. |
| `GET /UserViews` | ✅ | Home screen library list. Honors its `userId` parameter the way the reference does: the caller's own (or none) is fine, another user's is `403` unless the caller is an administrator, and an identifier that names nobody is `403` rather than `404` (`items.views.R2`). Ordered by the library's sort name (`admin.libraries.R4`), not by the bytes of the name. Re-audited byte-for-byte against the reference server : found and fixed two real divergences — `Type` was `"UserView"`, reference sends `"CollectionFolder"` for an ordinary library; `UserData` was omitted entirely, reference populates it same as any other item (`dto.go`'s `libraryBaseItemDto`). |
| `GET /UserViews/GroupingOptions`, `GET /Users/{userId}/GroupingOptions` | ✅ | The library folders a user may group into one view, which the preferences page renders as the checkboxes under "Library folders" (`items.grouping.R1`-`R4`). Both forms answer the same array of `{Name, Id}`: the groupable collection types only — `movies`, `tvshows` and libraries with no collection type, not `music`/`musicvideos`/`homevideos`/`books` — ordered by name rather than by sort name (`10 Coisas` before `9 Vidas`). Missing this route is what made the preferences page spin forever: the client loads it inside a `Promise.all` beside the user's views and hides its loading indicator only in that promise's success handler, so a `404` left the page blank and spinning. |
| `GET /Items` | ✅ | Generic listing/search, query-param filtered. Confirmed live (movie library grid, search). Also serves the Favorites tab's per-user form, `GET /Users/{userId}/Items?Filters=IsFavorite&...` — re-audited byte-for-byte against the reference server; found and fixed two real divergences: (1) `SortBy=SeriesSortName,SortName` (what the Favorites tab actually sends for Episode/Season rows) silently dropped the `SeriesSortName` key entirely, so favorited episodes from different series interleaved alphabetically by episode title instead of grouping by series — now a real sort key (`storage.SortBySeriesName`, a self-join to the owning series' `sort_name`). (2) `TotalRecordCount` ignored `EnableTotalRecordCount=false` (sent on every Favoritos-tab call) and always returned the real total instead of just how many items that response carries, confirmed against the reference server on both a first and second page. See the note below and "Users, sessions, devices" for the toggle routes it depends on. |
| `GET /Items/Latest` | ✅ | Home "recently added" rows. Confirmed live, exact query-param shape (`userId`+`parentId`) matches what Tinyfin's embedded bundle sends. A TV-shows library request now groups by series (one card per series — a `Season`, the `Series` itself, or a single `Episode`, per `docs/12`'s "Items/Latest — series grouping"), resolved via the clean-room two-phase process (spec extracted to `docs/12`, implemented from that spec plus live black-box comparison against the reference server, `internal/api/latest_grouping.go`) rather than left as the flat, ungrouped list of every recently-scanned `Episode`/`Season` row this used to return. The bundled client under `/web/` is served with `Cache-Control` (a versioned bundle is `immutable`, the entry point is revalidated) and gzipped when the client accepts it — measured before the change: no `Content-Encoding` on any of the 3.8 MB of JavaScript and no `Cache-Control` at all.

`groupItems=false` (confirmed live, any capitalization) returns the episodes themselves and never a `Season` or a `Series` row (`items.latest.R5`); the row a request without `parentId` gets — the one the home screen draws — groups the same way across libraries, ordered by each series' newest addition (`items.latest.R4`, measured, scenario `items.latest.grouping`); the movies-library case stays flat unconditionally, matching the reference server's own "no grouping" behavior there. A series every one of whose episodes is played (`PlayCount > 0`) for the requesting user is excluded from the grouped result entirely — confirmed live, doesn't consume a `limit` slot by being excluded — see `docs/12`'s "Fully watched series are excluded" section. `docs/12`'s "Implementation findings" section records one remaining reference-server behavior intentionally not reproduced (an unresolved card-selection quirk on month-plus-old bursts) — out of scope for this gap, logged for later. |
| `GET /Users/{userId}/Items/Latest` | ✅ | The per-user spelling the home screen also calls (measured on the reference: same body as `GET /Items/Latest`). Same authorization as `GET /Users/{userId}/Views`: own and administrator 200, another user's 403, an identifier that names nobody 404, a malformed one 400, anonymous 401. The account's `HidePlayedInLatest` and `LatestItemsExcludes` apply on both spellings (`items.latest.R1`-`R3`). |
| `GET /Items/{itemId}` | ✅ | Item detail. Confirmed live (`?userId=` query form). Re-audited against the reference server for the movie detail page specifically: `ImageTags`/`BackdropImageTags` used to only ever carry `Primary` even when the scanner had already indexed a servable `Backdrop`/`Logo`/`Banner` — now every single-instance kind the item actually has folds into `ImageTags` and every `Backdrop` into its own `BackdropImageTags` array (`dto.go`'s `imageTagsFor`), confirmed live: `ImageTags` now shows `{"Primary":..., "Logo":..., "Banner":...}` and `BackdropImageTags` a populated array for a real title. `People` (cast/crew) is also now populated when the item has a TMDB-enriched credit list (`storage.Item.People`, `dto.go`'s `peopleDtos`) — not live-validated against a real TMDB key (none configured), but covered by unit tests with a fake provider (`internal/metadata/tmdb_test.go`, `internal/scan/scan_test.go`, `internal/api/library_test.go`). Re-audited a second time (series/episode revalidation): `GenreItems` (a stable Id per `Genres` entry, `dto.go`'s `genreItemsFor`), `ExternalUrls` (TheMovieDb/IMDb links derived from `ProviderIDs`, `externalURLsFor`), and `ChildCount` (Series/Season/BoxSet only, direct non-recursive child count, `childCountFor`) were entirely absent from the DTO — now populated and confirmed live against a real title (`docker exec` + `curl`), matching the reference server's shape. `MediaSources` is now also embedded directly on this response (`dto.go`'s `mediaSourcesFor`, reusing `playback.go`'s `baseMediaSourceInfo` minus its device-profile negotiation), not just on `/PlaybackInfo` — confirmed live. `MediaStreams` entries now also carry `DisplayTitle` plus the additional passthrough fields (`Profile`, `ChannelLayout`, `TimeBase`, `AspectRatio`, `RefFrames`, `PixelFormat`, `Level`, `SampleRate`) DisplayTitle's algorithm needs — see docs/12's "Episode/movie detail page — MediaStream.DisplayTitle" for the full writeup and its "Implementation findings" for the live byte-for-byte comparison against the reference server. `Studios` (a stable Id per studio/network name, `dto.go`'s `studioItemsFor`, same `NameIdPair` shape as `GenreItems`) and `EndDate` are now also populated for a Series when TMDB enrichment supplies them (`storage.Item.Studios` from TMDB's `networks` field; `EndDate` from `last_air_date`, only once TMDB's `status` field is a terminal value — `Ended`/`Canceled`, not `Returning Series`/`In Production`/`Planned`/`Pilot`) — same "not live-validated, no TMDB key configured" caveat as `People` above, covered by unit tests (`internal/metadata/tmdb_test.go`, `internal/scan/scan_test.go`, `internal/api/library_test.go`). Not curled live against this endpoint specifically (no admin credentials available to authenticate against a test deployment); degradation was instead checked one level down — a test deployment rebuilt with this change and restarted cleanly, its `0003_studios` migration applied automatically (schema version 2 → 3), and its pre-existing Series/Season/Episode/Movie rows (338 items total) came through the restart with a correctly-defaulted empty `studios` column and no error/panic in the server logs. |
| `GET /Items/{itemId}/Ancestors` | ✅ | Breadcrumb trail. Confirmed live. |
| `GET /Users/{userId}/Items/{itemId}` | ✅ | Legacy per-item alias — still hit on *every* detail page load before the plain form, per `server.go`'s own comment (found via real client testing, not spec). Confirmed live. |
| `GET /UserItems/Resume` | ✅ | Continue-watching row. Confirmed live, exact match against Tinyfin's own bundle. Re-audit found and fixed a related bug one level down, in the mark-watched routes: a stale in-progress position could resurrect an already-unmarked item here — see the `PlayedItems`/`UserPlayedItems` notes below. |
| `GET /Shows/{seriesId}/Seasons` | ✅ | Confirmed live, real season data returned. A Season item is now also enriched (`internal/scan/scan.go`'s `walkSeasons`, `metadata.KindSeason` — TMDB's `GET /tv/{id}/season/{n}`), the same async enrich-then-reupsert pattern the series/movie/episode walks already use, giving a real localized name/overview/poster instead of the always-generic `"Season N"` filename fallback; not live-validated against real TMDB data (no key configured) but covered by `internal/metadata/tmdb_test.go` and `internal/scan/scan_test.go`. Degraded-path check was done at the deploy level, not by triggering a live rescan (no admin credentials available to call `POST /Library/Refresh` against a test deployment): the rebuilt image starts and serves its existing, already-scanned Season rows with no error/panic, and this endpoint's own route wasn't independently re-curled this pass. **Local season-poster gap closed**: a real jellyfin-web-vs-reference-server traffic comparison found a season card (Season 1 of a real series) with no `ImageTags.Primary` on Tinyfin (gray placeholder) while the reference server showed the real poster, even though both point at the same disk — the series root folder had a `season01-poster.jpg` (Kodi's per-season-poster convention, kodi.wiki's "Artwork" guide) that `walkSeasons` never looked for; TMDB-sourced season images were already wired correctly via `enrich`/`storeProviderImage`, this was purely a missing local-file lookup. Fixed with a new `seedSeasonImage` (`internal/scan/scan.go`), called before enrichment same as the movie/series local-artwork seeding it mirrors; covered by `TestScanLibrarySeasonPosterDetected` in `internal/scan/scan_test.go` and confirmed live against a test deployment for this exact series: `GET /Shows/{seriesId}/Seasons?userId=...` now returns `ImageTags: {"Primary": "1"}` for Season 1, and `GET /Items/{seasonId}/Images/Primary` serves bytes MD5-identical to the on-disk `season01-poster.jpg`. |
| `GET /Shows/{seriesId}/Episodes` | ✅ | Confirmed live, real episode data returned. Re-audit (series/episode revalidation) found a real bug: `?SeasonId=` (the casing jellyfin-web's season-detail-page call actually sends, confirmed via Playwright network capture) was read via a raw, case-sensitive `r.URL.Query().Get("seasonId")` in `handleShowEpisodes` (`library.go`), so it never matched and silently fell back to "every episode of the series" — the "More from Season X" carousel showed all ~103 episodes of a series instead of that season's ~37. Fixed by routing through `caseInsensitiveQuery` (the project's established pattern, already used elsewhere in this file); confirmed live against a test deployment for a real 3-season title (a series with three seasons): `?SeasonId=<season1>` (PascalCase) now returns 37 items matching `?seasonId=` (lowercase), not the full 103. |
| `GET /Items/Filters` | ✅ | Genres, tags, ratings and years of the items under `ParentId`; empty without one (browse.facets.R1). |
| `GET /Items/Filters2` | ⚠️ | Genres (with ids), tags and audio languages; subtitle languages are always empty (browse.facets.R2). |
| `GET /Search/Hints` | ✅ | Ranked hints over items and over genre, studio and person names (search.md). |
| `GET /Items/Suggestions` | ✅ | Random sample of the items the user can see (browse.suggestions). |
| `GET /Items/{itemId}/Similar` | ✅ | Items of the same type (movies, series) ranked by shared genres, studios, people and tags; empty for other types (browse.similar). |
| `GET /Shows/NextUp` | ✅ | Implemented (`internal/api/nextup.go`). Rule reverse-engineered by black-box testing against the reference server : per series, find the last episode (by season/episode order, not watch date) with `PlayCount > 0`; if it's not fully played, that same episode is the candidate (in-progress/"resume", included only when `enableResumable` isn't `false`); if fully played, the candidate is the next episode after it in order (crossing season boundaries correctly), or none if the series is finished (`enableRewatching` restart-at-episode-1 is a documented decision, not something the real server reproduced under test — see the handler's doc comment). Confirmed: a series with zero activity never appears (not "episode 1" as a naive reading of the endpoint name might suggest); marking an out-of-order episode played jumps straight to the one after it, not the first unwatched one; `nextUpDateCutoff` is a minimum-date bound; `EnableTotalRecordCount=false` reports `0`, diverging from `GET /Items`' own rule for that same param (`resolveTotalRecordCount`) — deliberately not reused unchanged. Also fixed a related pre-existing gap this depends on: `PlaystateStore.LastPlayedDate` was never actually written by any handler (`userdata.go`, `sessions_playback.go`) despite the storage field existing — now stamped/cleared to match confirmed reference-server behavior. Confirmed live end to end: `curl` against a test deployment matches the reference server for every case above, and a Playwright session shows the home screen's "Next Up" row rendering a real next episode after marking a prior one watched, with no console/network errors. Re-audit (series/episode revalidation) found a second real bug of the same shape as `Episodes?SeasonId=` above: `?SeriesId=` (PascalCase, the series-detail-page's own casing) was read via a raw, case-sensitive `r.URL.Query().Get("seriesId")`, so it never matched and silently fell back to "no series requested" — every series with progress leaked into a single series' "Next Up" widget (confirmed live: opening one series' detail page showed another series' next episode in the row). Fixed the same way, routed every param this handler reads through `caseInsensitiveQuery`, not just `seriesId`; confirmed live against a test deployment with three series each carrying progress: `?SeriesId=<id>` (PascalCase) now returns exactly that series' own candidate. |
| `GET /Items/{itemId}/Collections` | ✅ | Collections (BoxSet) holding the item; empty when none (browse.collections.R1). |
| `GET /Items/{itemId}/ThemeMedia` | ✅ | Implemented (`internal/api/theme_media.go`), per `docs/12`'s "Movie detail page — Theme Media" spec — resolved at request time (no scan-time indexing) by checking the item's own folder (derived from `storage.Item.Path`) for a standalone `theme.<ext>` file or any audio file inside `theme-music/` (→ `ThemeSongsResult`), and any video file inside `backdrops/` (→ `ThemeVideosResult`); `SoundtrackSongsResult` always empty (not a movie concept). Confirmed live end to end against the reference server's own empty-shape response (the title has no theme files) and, separately, by dropping a real `theme.mp3` into a movie folder and confirming it showed up in `ThemeSongsResult.Items` on the next request with no rescan needed — then removing it and confirming the response reverted to empty. `/ThemeSongs`/`/ThemeVideos` (separate legacy routes some older clients may call) are still unimplemented — out of scope for this gap, not exercised by jellyfin-web's own detail page. |
| `GET /Users/{userId}/Items/{itemId}/Intros` | ✅ | Empty list for a readable item (browse.intros.R1); also `GET /Items/{itemId}/Intros`. |
| `GET /Items/{itemId}/LocalTrailers` | ❌ | From SDK/apiclient catalog. Not exercised live (test library has no local trailer files) but a real registered client call. |
| `GET /Items/{itemId}/SpecialFeatures` | ❌ | Catalog only, same caveat as above. |
| `GET /Items/Root` | ❌ | Catalog only — media-folder root discovery. |
| `GET /Items/Counts` | ✅ | Implemented (`internal/api/items_counts.go`, `requireAuth`), found via the `/dashboard` overview pass — see "Dashboard — Overview" below. `MovieCount`/`SeriesCount`/`EpisodeCount` are real (`QueryItems` with `Limit: 1`, reading `TotalRecordCount`, per the no-full-catalog-scan rule every other count-shaped read in this project follows); every other field (`ArtistCount`, `ProgramCount`, `TrailerCount`, `SongCount`, `AlbumCount`, `MusicVideoCount`, `BoxSetCount`, `BookCount`, `ItemCount`) is a structural zero, not fabricated — `internal/scan` never produces any of those item types. Confirmed live against a test deployment. |
| `POST /Items/{itemId}/Refresh` | ✅ | Implemented (`internal/api/item_refresh.go`, admin-only). Originally catalogued as "single item refresh"; the `/dashboard/libraries` pass found this is actually the **primary per-library scan mechanism** too — jellyfin-web's "Scan library" (scan just one library) menu action calls exactly this route with the library's own `ItemId`, not a library-scoped variant: `POST /Items/{itemId}/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&ReplaceAllImages=false&RegenerateTrickplay=false&ReplaceAllMetadata=false` (query params accepted and ignored — Tinyfin has no refresh-mode distinctions to honor). When `itemId` matches a library's own `ItemId`, queues that library's scan (`s.runScan`), same mechanism `POST /Library/Refresh` uses for every library — confirmed live against a test deployment (204, scan ran). When `itemId` matches an ordinary catalog item instead, queues a real per-item re-fetch (`Server.runItemRefresh` -> `scan.Scanner.RefreshItem`) — Movie/Series/Season/Episode only, the same set `ScanLibrary` itself enriches; any other type is a silent no-op. The re-fetch re-reads **both** local sources (a sidecar `.nfo`, folder-convention artwork — `poster.jpg`, `seasonNN-poster.jpg`, ...) *and* the configured remote `metadata.Provider`, same order as a fresh scan (local artwork seeded, then the remote provider runs, then the local `.nfo` is applied last so it overrides the provider). The `ReplaceAllImages` query param is honored (validated against the reference server's own `ItemImageProvider`, `docs/08`'s "Local-vs-remote precedence" section): with it `false` (jellyfin-web's own default), a Provider-returned image never overwrites an image kind the item already has, local or remote; `true` lets it win. Series/Season store no filesystem `Path` of their own (only Movie/Episode do) — their folder is derived from one child episode's `Path` instead (`seriesFolder`/`seasonFolder`, `internal/scan/scan.go`). A Season's own `.nfo` handling is narrower than Series/Movie/Episode: it looks for `tvshow.nfo` in the season's own folder first, falling back to the series root, but only extracts a `<namedseason number="N">Custom Name</namedseason>` override for that season's display name (`applySeasonNFO`, `internal/scan/nfo.go`) — matching the reference server's own `SeriesNfoSeasonProvider`; found and implemented after a real series (a real series library) turned up with its `tvshow.nfo` misplaced inside `Season 01/` instead of the series root, which validation against the reference server's source confirmed produces no effect on either server for that specific file (no `<namedseason>` tag present) — a media-library organization issue, not a bug. **Deliberately does not surface as "running" anywhere**: an earlier version routed this through the same `scanState`/`GET /ScheduledTasks` mechanism a library scan uses, on the reasoning that *something* should show progress — but a per-item job showing as "scanning library" on the dashboard is wrong, and checking jellyfin-web's own source confirmed the reference server never conflates the two: a per-item refresh is tracked entirely separately (`BaseItemDto.RefreshProgress`/`RefreshStatus` on the item itself, plus a per-item `RefreshProgress` WebSocket message scoped by `ItemId` — `emby-itemrefreshindicator`/`RefreshIndicator.tsx` in jellyfin-web), never touching `GET /ScheduledTasks` or its `ScheduledTasksInfo` WebSocket push at all. Sharing `scanState` meant refreshing any single item made the Dashboard's "Scan All Libraries" button spin/disable itself with no library scan running — a variant of the exact bug this doc's own history above describes. Reverted; Tinyfin has no per-item progress indicator today (no `RefreshProgress`/`RefreshStatus` DTO fields, no per-item WebSocket push) — a real, documented gap, not silently faked via the wrong tracker. Regression-guarded by `TestItemRefreshDoesNotAffectScheduledTasks` (`internal/api/scheduled_tasks_test.go`). **Progress reporting, corrected against the reference:** the library branch reports real progress —
`GET /ScheduledTasks` carries a `CurrentProgressPercentage` that rises with the share of queued work the pass has finished
(`internal/scan.ScanLibraryWithProgress`), instead of the old "libraries finished in this run", which sat at 0% for the whole
of a single-library scan. Deliberate divergence, decided rather than accidental: the reference server keeps the `RefreshLibrary`
task `Idle` for a single-library refresh and reports the scan only on the library itself (`RefreshStatus`/`RefreshProgress`,
observed live), which leaves a dashboard watching only the task list with no sign of it at all; Tinyfin reports both
(docs/00-decision-log.md, D15). Progress over the websocket follows the same numbers, one push per whole percentage point
crossed.
 Used to be an honest no-op with no re-read of anything, local or remote: found live when refreshing a real series' metadata from the dashboard had no effect and nothing showed as running, then found live *again*, more specifically, that the fix must not stop at "call the remote provider" — the series' media folder already had a `tvshow.nfo` and a poster Tinyfin's own scanner already knows how to read, so an item can be "incomplete" and fixable with **zero** TMDB configuration at all; this distinction was caught directly (see `AGENTS.md`'s jellyfin-web-source-reading practice for how the surrounding `GET /ScheduledTasks`/`Key` bug in this same feature area was found, a separate but adjacent fix). Unknown `itemId` (neither a library nor an item): 404. |
| `GET /Playlists`, `GET /Playlists/{id}`, `GET/POST /Playlists/{id}/Items`, `.../Users` | ✅ | Playlists with entries, ownership, shares and the Playlists view (playlists.md). |
| `GET /Genres`, `GET /Genres/{genreName}` | ✅ | Distinct names carried by what the user can see, with counts (browse.genres). Genre images are not generated. |
| `GET /Years` | ❌ | From apiclient's concatenated-suffix scan (year filter). No route registered. |

## Specialized media types

| Route | Status | Note |
|---|---|---|
| `GET /Shows/{seriesId}/Seasons`, `/Episodes` | ✅ | Counted above under library nav — this *is* the TV-show-specific slice of that category and it's solid. |
| `GET /Studios` | ✅ | Studios carried by visible items, with counts; `GET /Studios/{name}` the single form (browse.studios). |
| `GET /LiveTv/Programs` | ⚠️ | Always returns an empty `QueryResult` (`library.go`'s `handleLiveTvPrograms`) — no LiveTV of any kind implemented (see "Explicitly out of scope" below), registered only because jellyfin-web's series detail page calls it unconditionally. Confirmed live: `curl` against a test deployment returns `200 {"Items":[],"TotalRecordCount":0,"StartIndex":0}`. |
| `GET /Artists`, `GET /Artists/AlbumArtists` | ❌ | Confirmed 404 live (search screen's artist-results query). Music library browsing by artist is entirely unimplemented. |
| `GET /Persons`, `GET /Persons/{name}` | ✅ | People credited on visible items (NFO `<actor>`, `<director>`, `<credits>`, `<writer>` and TMDB), personTypes filters, 404 for unknown names (browse.persons). |
| `GET /Genres/{name}/Images/{imageType}`, `GET /MusicGenres/{name}/Images/{imageType}` | ❌ | Catalog only. |
| `GET /Studios/{name}`, `GET /Studios/{name}/Images/{imageType}` | ⚠️ | `GET /Studios/{name}` is served (browse.studios.R2); the per-studio image route is not. |
| `GET /Persons/{name}/Images/{imageType}`, `GET /Artists/{name}/Images/{imageType}` | ❌ | Catalog only. |
| `GET /Movies/Recommendations` | ✅ | Categories from the movies the user played last (browse.recommendations). |
| `GET /Movies/{itemId}/Similar` | ✅ | Same as `GET /Items/{itemId}/Similar`. |
| `GET /Shows/Upcoming` | ❌ | Catalog only. |
| `GET /Trailers/{itemId}/Similar` | ✅ | Same as `GET /Items/{itemId}/Similar`. |

## Playback/streaming

| Route | Status | Note |
|---|---|---|
| `GET/POST /Items/{itemId}/PlaybackInfo` | ✅ | Confirmed live — the negotiation step that precedes both Direct Play and HLS playback. |
| `GET /Videos/{itemId}/master.m3u8` | ✅ | Confirmed live, full playback exercised end-to-end (Direct Play, `.ts` segments). |
| `GET /Videos/{itemId}/main.m3u8` | ✅ | Confirmed live. |
| `GET /Videos/{itemId}/{file}` | ✅ | Single wildcard covers `/stream`, `/stream.{container}`, and segment delivery (`seg_00000.ts`, ...). Confirmed live — segments served and played back successfully. Self-consistent: Tinyfin's own `master.m3u8`/`main.m3u8` only ever reference URLs shaped this way, so the real server's different segment-naming convention (`hls1/{mediaSourceId}/{n}.mp4`, seen against the reference server) isn't a gap — the client just follows whatever playlist URLs the server hands it. |
| `POST /Sessions/Playing` | ✅ | Confirmed live, fired on playback start. |
| `POST /Sessions/Playing/Progress` | ✅ | Confirmed live, fired periodically during playback. |
| `POST /Sessions/Playing/Stopped` | ✅ | Confirmed live, fired on navigating away from the player. |
| `GET /MediaSegments/{itemId}` | ✅ | Registered; not exercised live (no chapter/intro-skip segment data in the test library) but wired up. |
| `GET /Videos/{itemId}/Subtitles`, `/Subtitles/{index}`, `.../Stream.{format}`, `/{mediaSourceId}/Subtitles/{index}/{startTicks}/Stream.{format}` | ❌ | No subtitle-delivery route registered at all. Confirmed as a real, actively-called route family via the reference-server capture (embedded-subtitle stream fetched mid-playback); not exercised against Tinyfin directly since the test movie had no subtitle tracks selected, but the route is simply absent from `routing.go`. |
| `GET /Playback/BitrateTest` | ✅ | Implemented (`internal/api/playback.go`). Shape confirmed against the reference server via `curl`, not assumed: response is `Size` random bytes rounded UP to the next power of two (floor 16 — every `Size` from 1-16 comes back 16 bytes), `Content-Type: application/octet-stream`; `Size` omitted defaults to what a real server sends as its own default (100000, rounding to 131072); `Size` is validated `>= 1` and `<= 100000000` server-side (found by binary search against the real server's own 400 error, which spells the bounds out explicitly), mirrored here as the same bound. Confirmed live: `curl` against a test deployment matches on default size, an explicit size (with rounding), the 400 boundary at both ends, and random (non-reproducible) content. |
| Trickplay (seek thumbnails) | ❌ | No dedicated route found actively called by this particular jellyfin-web build (the SDK bundle only references `Trickplay` as a requestable `Fields` enum value on item queries, not as its own endpoint) — plausibly because the test library has no pre-generated trickplay data to trigger the image-tile fetch. Not implemented in Tinyfin regardless; low confidence on exact call shape without a trickplay-enabled library to test against. |
| `SyncPlay/*` (List, Join, Leave, New, Ready, Seek, Pause, Unpause, Queue, ...) | ❌ | `GET /SyncPlay/List` confirmed 404 live, called unconditionally on nearly every screen transition (noisy but harmless — client treats 404 as "SyncPlay unavailable"). The rest of the ~18-route family (from the SDK catalog) is equally unimplemented; explicitly in-scope per `docs/04` (unlike Live TV/Channels) but a multi-client feature, lower priority for a single-user/idle-memory-focused v1. |
| `POST /LiveStreams/Open`, `POST /LiveStreams/Close` | ❌ | Catalog only — external tuner live-stream lifecycle, effectively Live TV-adjacent. |

## Images and branding

| Route | Status | Note |
|---|---|---|
| Item images at a requested size — `GET /Items/{itemId}/Images/{imageType}?width=&height=&maxWidth=&maxHeight=&fillWidth=&fillHeight=&quality=` | ✅ | The size members the reference binds on this route, measured one at a time: caps in the order `maxHeight` then `maxWidth`, a fill box that covers without enlarging, never larger than the stored picture, the stored format kept (PNG stays PNG), JPEG default quality 100, a non-integer member answered 400. `images.api.R4`, `internal/picture` (shared with chapter pictures), `internal/api/item_image_resize.go`. Not implemented here: `format` and the `Accept: image/webp` negotiation (`DIV-29`). |
| Library card images — `GET /Items/{libraryItemId}/Images/Primary`, `GET /Items/{libraryItemId}/Images`, `POST`/`DELETE /Items/{libraryItemId}/Images/{imageType}` | ✅ | The same routes an item's own artwork uses, answered for a library id: `GET /Library/VirtualFolders` reports `PrimaryImageItemId` = the library's own `ItemId` whenever the library has a picture, and jellyfin-web's libraries page only draws a card image when that field is present (it builds the URL from `ItemId` and falls back to the collection-type icon otherwise — `libraries.js` chunk, the kind of client-side guard AGENTS.md's "Reading jellyfin-web" section describes). Confirmed live on the reference server: every one of its libraries reports `PrimaryImageItemId`, and `GET /Items/{libraryItemId}/Images/Primary` answers a 960x540 PNG stored under the server's metadata tree, stable across scans (`ImageTag` unchanged after full re-scans) and resizable through the usual `maxWidth`/`quality` query params. **Tinyfin generates its own** when the library has none: a mosaic of up to four of the library's own pictures (a backdrop when the item has one, 16:9 like the card, a poster otherwise) as slanted bands with the library name in white (`internal/libraryart`) — set in DejaVu Sans Bold at the size and anchor the reference's images measure ("Anime" 391x87 wide, ink centred at y=260 of 540; reproduced as 391x86 at y=259), so the name reads the same on both servers even though the picture under it does not, written into the scan image directory once the pass that cataloged those posters finishes (`Server.ensureLibraryImage`). This is a deliberate visual divergence — the mosaic is not the reference's single-still look — with the contract around it the same: 16:9, PNG, stable once generated, no image at all for a library with no artwork (docs/00-decision-log.md, D16). The library item publishes `ImageTags.Primary` with a tag that changes whenever the picture does, plus `ImageBlurHashes` and `PrimaryImageAspectRatio` (960/540), exactly as the reference's `CollectionFolder` does in `GET /UserViews` — that tag, not the serving route, is what makes jellyfin-web draw the card at all. Kept in its own `library_images` table rather than `image_refs` because a library is not a catalog item here. Deleting one is a 204 and the next scan composes a fresh mosaic unless an admin uploads first. The mosaic is up to four of the library's own pictures as slanted bands with the name in white over a uniform 38% dark overlay across the whole card (`internal/libraryart`). Upload replaces it in place (base64 body, same shape as an item upload).
| `GET /Items/{itemId}/Images/{imageType}`, `/{imageType}/{imageIndex}` | ✅ | Confirmed live implicitly — every list/grid/detail screen across the whole capture rendered real posters/backdrops/thumbnails. Deliberately unauthenticated (see `library.go`'s `handleItemImage` doc comment) — a real, tested requirement, not a spec guess. The same route answers a **cast credit's** photo for the id `BaseItemPerson.Id` carries (jellyfin-web builds `People[].PrimaryImageTag`'s URL from it): the picture is downloaded on the first request and cached in `person_images`, because a person is not a catalog item (`DIV-33`, [08](08-library-scanning-and-metadata.md) "Cast photos"). |
| `GET /Branding/Configuration` | ✅ | Confirmed live, fetched right after login. |
| `GET /Branding/Css`, `/Branding/Css.css` | ✅ | Implemented (`internal/api/config_sections.go`'s `handleBrandingCss`), anonymous like the reference. Answers the `CustomCss` the admin stored in the branding section (200 with an empty body when none is set — measured live). Where that CSS lands is the client's decision, not this route's: jellyfin-web injects it in its user-facing layouts only, never in the dashboard (`docs/14-client-branding-and-admin-pages.md`, section 2). |
| `GET`, `POST`, `DELETE /Branding/Splashscreen` | ❌ | Catalog only — measured 404 live on 10 Nov 2026. Not a no-op flag: `BrandingOptions.SplashscreenEnabled` **is** served here (`false` by default), so turning it on today asks jellyfin-web to draw a login backdrop from a route that does not exist. The reference's own OpenAPI declares the whole family (a `GET` with `tag`/`format`, an upload `POST` answering 204, a `DELETE`); the same picture is the login backdrop in web and in Android, since the Android client is a WebView over this same bundle. `docs/14`, section 2. |
| `GET /web/ConfigurationPage?name=` | ❌ | Catalog only — 404 measured live. This is the other half of the `/web/ConfigurationPages` row below: that route advertises a page, this one delivers it (a fragment carrying `data-controller="__plugin/<name>.js"`, plus the module itself). Declared by the reference's own OpenAPI, so it is a documented surface, not an undocumented internal — it is the only way to put a page of our own inside the dashboard without patching the client. `docs/14`, section 3. |
| `GET /Items/{itemId}/RemoteImages`, `/RemoteImages/Providers`, `/RemoteImages/Download` | ❌ | Confirmed 404 live for `/RemoteImages/Providers` specifically, via the `/dashboard/libraries` → "Editar imagens" flow (any library card's own image is edited through the exact same generic item-image admin UI a movie/series detail page would use — the library is just another `itemId`). |
| `GET /Items/{itemId}/Images` (no `imageType` — the "list every image with metadata" admin variant, distinct from the already-implemented `GET /Items/{itemId}/Images/{imageType}` byte-serving route), `POST /Items/{itemId}/Images/{imageType}[/{imageIndex}]` (upload), `DELETE /Items/{itemId}/Images/{imageType}[/{imageIndex}]` (delete) | ✅ | Implemented (`internal/api/item_images_admin.go`, admin-only). `GET` lists every `ImageRef` the item has across all known kinds (honest-empty list, not 404, for an item with none) — confirmed live against a test deployment for a real movie (Primary/Logo/Banner/Backdrop all listed with real dimensions/size). `POST`'s body is raw base64 image bytes (confirmed live against the reference server, not multipart), file extension follows `Content-Type`; for the one multi-instance kind (`Backdrop`), an `imageIndex` naming an existing image overwrites it in place, any other value (or none) appends as the next index — matches reference-server behavior confirmed live. `DELETE` is idempotent (204 whether or not the image/index existed), matching the reference server's own confirmed-live behavior, not a 404-on-miss guess. |

## Subtitles and lyrics

| Route | Status | Note |
|---|---|---|
| Subtitle delivery family (see Playback/streaming table above) | ❌ | Listed once above to avoid duplication; genuinely missing. |
| `GET /Items/{itemId}/RemoteSearch/Subtitles/{language}`, `/{subtitleId}` | ❌ | Catalog only — admin-triggered subtitle search/download. |
| `GET /Providers/Subtitles/Subtitles/{subtitleId}` | ❌ | Catalog only. |
| `GET /Audio/{itemId}/Lyrics`, `/RemoteSearch/Lyrics`, `/RemoteSearch/Lyrics/{lyricId}` | ❌ | Catalog only — no lyrics support at all (music playback isn't a focus of the test library used here either). |

## Users, sessions, devices

| Route | Status | Note |
|---|---|---|
| `POST /Users/AuthenticateByName` | ✅ | Confirmed live, the login flow itself. |
| `GET /Users/Public`, `GET /Users/Me`, `GET /Users`, `GET /Users/{userId}` | ✅ | Confirmed live where exercised (`Me`, `{userId}`); `Public`/list registered and match the reference shape. |
| `POST /Users/New`, `DELETE /Users/{userId}` | ✅ | Registered; admin-only in the reference server but only token-gated here (documented known simplification in `server.go`, not a silent gap). |
| `GET/POST /DisplayPreferences/{displayPreferencesId}` | ✅ | Confirmed live, fetched immediately post-login; left unhandled it blocks the post-login redirect entirely (per `server.go`'s own comment, found via real client testing). |
| `POST /Sessions/Capabilities/Full` | ✅ | Confirmed live, fired right after sign-in by every official client. |
| `GET /QuickConnect/Enabled` | ✅ | Confirmed live. Only this one QuickConnect route exists — see gap below. |
| `POST /Users/{userId}/FavoriteItems/{itemId}` (+ `DELETE`) | ✅ | Implemented (`internal/api/userdata.go`). Confirmed live and re-audited against the reference server byte-for-byte: response shape, `IsFavorite` persistence independent of `Played`/`PlayCount`, and the Favorites tab (`#/home?tab=1`) reflecting the toggle. |
| `POST /Users/{userId}/PlayedItems/{itemId}` (+ `DELETE`) | ✅ | Implemented (`internal/api/userdata.go`). Confirmed live and re-audited against the reference server: `PlayCount` only advances on the unplayed→played transition and resets to 0 on unmark (both matched already). Re-audit found one real divergence, now fixed: POST didn't reset `PlaybackPositionTicks` to 0 on marking played, unlike the reference server — a stale in-progress position survived a mark-watched call and resurfaced the item in the home screen's "Continuar assistindo" row the moment it was later unmarked watched. Same fix applied to `POST /UserPlayedItems/{itemId}` (`sessions_playback.go`'s `handleUserPlayedItemsMark`), which had the identical gap. |
| `POST /UserItems/{itemId}/Rating` | ❌ | Catalog only — star rating. |
| `POST/GET /UserItems/{itemId}/UserData` | ❌ | Catalog only — direct playstate/favorite edit, alternate to the two routes above. |
| WebSocket `/socket` | ✅ | Implemented (`internal/api/websocket.go`), per the clean-room spec in `docs/12-clean-room-behavior-specs.md`'s "Dashboard overview — the `GET /socket` realtime channel" section (behavior extraction plus a dedicated "known chronic issues, deliberately not reproduced" subsection covering real, multi-year `jellyfin/jellyfin` architecture bugs this implementation was designed around from the start). Auth/keep-alive handshake, and the three push-on-event channels (`Sessions`, `ScheduledTasksInfo`, `ActivityLogEntry`) all confirmed live against a test deployment with a real jellyfin-web build: `ForceKeepAlive`/`KeepAlive` handshake matches byte-for-byte, and triggering a real `POST /Library/Refresh` produced the expected `ScheduledTasksInfo` pushes (Running → Completed) with no server error. `RefreshProgress` (per-item scan progress) is a documented, deliberate gap — `internal/scan` has no per-item progress callback to hook yet. Remote-control command delivery over this channel (`GeneralCommand` et al) is a separate, still out-of-scope feature — see the row below. |
| `GET /Sessions` | ✅ | Implemented (`internal/api/sessions.go`, admin-only — see its row's own reasoning below), found via the `/dashboard` overview pass. A small in-process session registry (docs/06's default mode), populated from exactly two existing signals: `POST /Sessions/Capabilities/Full` (previously entirely discarded) and the three playback-report routes touching an already-registered device's `LastActivityDate`. Deliberately narrow — no now-playing tracking, no expiry beyond the `activeWithinSeconds` filter itself (see "Dashboard — Overview" below). Confirmed live against a test deployment, including a real jellyfin-web session showing up correctly in the admin Dashboard "Devices" widget. Note: the WebSocket `Sessions` push channel above uses a *different*, per-connection filtering rule than this REST route's admin-only gate — see `docs/12`'s WebSocket section for why that asymmetry is deliberate, not an inconsistency. |
| `POST /Sessions/{id}/Command`, `/Message`, `/Playing`, `/Viewing`, `POST /Sessions/Logout` | ❌ | Catalog only — remote session *control*. The transport it would ride on (`GET /socket`, above) now exists, but the command-delivery routes themselves are still entirely absent — a separate, larger feature. |
| `POST /QuickConnect/Initiate`, `/Connect`, `/Authorize` | ❌ | Catalog only — only the capability-check route (`Enabled`) exists; the actual pairing flow can't complete even though the client believes QuickConnect is available. |
| `GET /Devices`, `/Devices/Info`, `DELETE /Devices` | ✅ | Implemented (`internal/api/admin_pages.go`), admin-only; measured live (`/Devices`, `/Devices/Info`) including the live session this doc's own probe created showing up with its `AppName`/`LastUserName`. |
| `GET`/`POST /Devices/Options` | ✅ | Implemented, admin-only. Measured live: 404 for a device with **no saved options record** (that is the reference's answer too, not a missing route), 200 with `{Id,DeviceId,CustomName}` once one was posted. |
| `GET`/`POST /Auth/Keys`, `DELETE /Auth/Keys/{key}` | ✅ | Implemented (`internal/api/admin_pages.go`), admin-only; measured live (`{"Items":[],"TotalRecordCount":0,...}` on an instance with no keys). |
| `GET /Auth/Providers`, `GET /Auth/PasswordResetProviders` | ✅ | Both measured live (200): the built-in default provider of each kind, since Tinyfin has no provider registry. |

## System/admin

| Route | Status | Note |
|---|---|---|
| `GET /System/Info/Public`, `GET /System/Info` | ✅ | Confirmed live. |
| `GET/POST/DELETE /Library/VirtualFolders` | ✅ | Registered, admin-gated. Upgraded back from ⚠️: `POST`'s paths-in-body bug is fixed — `handleAddVirtualFolder` now reads `LibraryOptions.PathInfos[].Path` from the request body (what the real dashboard actually sends), falling back to the `paths` query param only when the body carries none, confirmed live end to end against a test deployment (a library added through the same body shape jellyfin-web sends now lands with its folder in `Locations`, not empty). `GET`'s response DTO (`virtualFolderInfo`) now models `PrimaryImageItemId`, `RefreshStatus` and `RefreshProgress` too (see the library-image row below for `PrimaryImageItemId`; `RefreshStatus`/`RefreshProgress` report a scan of that library that is in flight right now — `Active` + a percentage, or `Idle` with the field absent — which is the per-library report the reference server gives for `POST /Items/{libraryItemId}/Refresh`, observed live there as `Active` 0 → 18.64 → 75.41 on that one library while its `RefreshLibrary` task stayed `Idle`). It also echoes back whatever `LibraryOptions` blob was last saved via the new `.../LibraryOptions` route below (see there for the "opaque save-and-echo, not a modeled ~35-field object" scope decision) — closing most, not all, of the previously-flagged DTO gap. |
| `POST /Library/Refresh` | ✅ | Same registration status as above; this specific route (scan *every* library) is confirmed working, same as `POST /Items/{itemId}/Refresh` above (now also implemented, for the per-library case). |
| `GET /System/Endpoint` | ✅ | Implemented (`internal/api/system.go`). Shape and requires-auth confirmed against the reference server : `{"IsLocal":bool,"IsInNetwork":bool}`, 401 with no token. `IsLocal`/`IsInNetwork` derived from the request's actual TCP peer address (loopback / RFC 1918 private / link-local) — confirmed `X-Forwarded-For` is ignored entirely by the real server, so no reverse-proxy trust config was added to match that. Confirmed live: `curl` against a test deployment matches from outside the container (private Docker bridge address → `IsLocal:false, IsInNetwork:true`) and a Playwright session shows this called once at startup with no 404. |
| `GET /System/Info/Storage` | ✅ | Implemented (`internal/api/system_info_storage.go`, admin-only), found via the `/dashboard` overview pass — not previously catalogued under any name. Real `statfs(2)`-backed free/used space per library folder (one stat per `storage.Library.Paths` entry, since a library can span multiple mounts) and per Tinyfin's own real directories: `ProgramDataFolder`/`CacheFolder`/`LogFolder`/`InternalMetadataFolder`/`ImageCacheFolder` all report the same real directory (`Config.DataDir` — Tinyfin has no config/cache/log/metadata split, unlike the reference server, so this is the honest single answer, not five fabricated paths), `TranscodingTempFolder` is `os.TempDir()` (the real transcode working directory), `WebFolder` is present only when a web bundle is actually configured. `StorageType` is always `"Fixed"` — a documented simplification, no dependency-free way to detect network mounts. Confirmed live against a test deployment, including the Dashboard "Locations" widget rendering real GiB figures. See "Dashboard — Overview" below. |
| `GET /System/Configuration`, `POST /System/Configuration` | ✅ | Implemented (`internal/api/system.go`'s main server configuration), measured live. This is what every settings page of the dashboard loads and saves through, and what `#/dashboard/libraries/metadata` uses alone. |
| `GET`/`POST /System/Configuration/{network,encoding,branding}` | ✅ | Implemented (`internal/api/config_sections.go`), each stored as one JSON document and served back with the reference's defaults for members left out (`admin.config.R3`). Measured live on all three. The branding one is also what a client reads as `BrandingOptions`. |
| `GET`/`POST /System/Configuration/metadata`, `/System/Configuration/xbmcmetadata` | ✅ | The two named sections the library settings pages load: `#/dashboard/libraries/display` (`metadata`) and `#/dashboard/libraries/nfo` (`xbmcmetadata`). Both used to answer 404, which is why both pages stopped at their load-error alert; the contract now pins them (`admin.config.R9`/`R10`/`R11`, scenarios `admin.config.metadata`, `admin.config.xbmcmetadata`, `admin.config.section-invalid`). Every member is stored and served back with the reference's defaults and none is honored — no `.nfo` sidecar is written, and `DateAdded` always comes from the file's own timestamp, so `metadata` reports `true` (file time) and the scan-time option is stored without being honored (`DIV-30`, `DIV-31`). A member of the wrong type is `400` and leaves the section unchanged (the reference answers 500). |
| `GET`/`POST /System/Configuration/{key}` for any other key | ❌ | Deliberate: an unknown section answers 404 for an administrator (the reference answers 500; `admin.config.R6`), 403 for anybody else. |
| `GET /System/ActivityLog/Entries` | ✅ | Implemented (`internal/api/activity_log.go`, admin-only), found via the `/dashboard` overview pass. Deliberately narrow, real (not stubbed) activity log: only a few login/playback event types are generated — `SessionStarted` + `AuthenticationSucceeded` (successful login, two entries, `users.auth.R23`) and `AuthenticationFailed` (wrong password for an existing enabled account; unknown usernames and disabled accounts write nothing, `users.auth.R26`/`R27`) — durably persisted (`internal/storage/activity.go`, sqlite migration `0005_activity_log.sql`), not scan-progress/item-added/user-management events, which the reference server's own activity log also carries but this project has no hook points for yet. `hasUserId=true`/`false` genuinely filters on a nullable `user_id` column (a failed login has none), `minDate`/`startIndex`/`limit` all real. See "Dashboard — Overview" below for the full capture and scope writeup. |
| `GET /System/Logs`, `/System/Logs/Log` | ❌ | Catalog only. |
| `POST /System/Restart`, `/System/Shutdown` | ❌ | Catalog only. |
| `GET /System/Ping` | ✅ | Product name as a JSON string, GET and POST, no auth (misc.ping.R1). |
| `GET /ScheduledTasks` | ✅ | Implemented (`internal/api/scheduled_tasks.go` plus the registry and scheduler in `scheduled_tasks_engine.go`, admin-only). Reports the reference's twenty tasks with its own `Key`, `Id`, `Name`, `Category`, `Description` and `IsHidden` (`RefreshInternetChannels` and `RefreshGuide` are the hidden ones, so `isHidden=false` lists the other eighteen) — that is what makes the tasks page (`#/dashboard/tasks`) group them under `Application`, `Library` and `Maintenance` exactly like the reference instead of showing one lonely row. Only some do real work: `RefreshLibrary` (the media scan), `RefreshChapterImages` and `RefreshTrickplayImages` (their batches), `CleanActivityLog` (deletes activity entries older than 30 days), `CleanLogFiles` (deletes `log_*.log` files older than three days) and the cache/transcode/database sweeps (empty, nothing to reclaim yet). The rest — plugin updates, subtitle and lyric downloads, audio normalization, keyframe extraction, media segments, people and guide refreshes — belong to subsystems this project does not have and complete at once without touching anything (`admin.tasks.R10`), because a fabricated task that lied about doing work would be worse than an honest no-op. `State`, `CurrentProgressPercentage` and `LastExecutionResult` are live, and the percentage is real for the three batches that take time — the scan, the chapter batch and the trickplay batch (each pushes a `ScheduledTasksInfo` update per whole point). `isEnabled` and `isHidden` filter the list; the reference additionally treats the library tasks as disabled while no library exists. |
| `GET /ScheduledTasks/{taskId}` | ✅ | The task detail page (`#/dashboard/tasks/{id}`) loads one task here, and the play/stop and trigger actions address it by the same id. The id is the task's 32-hex `Id`; passing a `Key` is a 404, like any unknown id (`admin.tasks.R3`, `R5`). |
| `POST`/`DELETE /ScheduledTasks/Running/{taskId}` | ✅ | The list's play and stop buttons (`admin.tasks.R6`, `R7`, `R8`). Start queues the task and answers 204 without waiting; stop cancels a running media scan, chapter-image batch or trickplay batch, and the run then ends as `LastExecutionResult.Status` `Cancelled`. Stopping a task that is not running is a 204 no-op where the reference answers 500 — a divergence (`admin.tasks.stop-idle`), because a 500 for a harmless click is the reference's defect, not a contract worth reproducing. Both this and the trigger route below are served by one pattern, `POST /ScheduledTasks/{first}/{second}`: the two literal patterns would overlap on `/ScheduledTasks/Running/Triggers` and `net/http` panics on that ambiguity, so the handler dispatches on the position instead. |
| `POST /ScheduledTasks/{taskId}/Triggers` | ✅ | The detail page's trigger table, and the last real gap this page had: the body is the whole replacement list (adding and removing a trigger both send the resulting list), the four types the dialog offers — `DailyTrigger`, `WeeklyTrigger`, `IntervalTrigger`, `StartupTrigger` — plus `MaxRuntimeTicks` are accepted and persisted per task in the settings store, an unknown `Type` or a missing body is a 400 validation problem, and an empty list removes every trigger (`admin.tasks.R4`, `R5`). A scheduler started with the server checks every 30 s and fires `StartupTrigger` once at boot, `IntervalTrigger` when the interval has elapsed since the last fire, and `DailyTrigger`/`WeeklyTrigger` at the local time of day (and weekday). `MaxRuntimeTicks` is enforced: a run fired by that trigger is stopped when it passes the limit, exactly as a manual stop stops it, and reports `Cancelled` (`admin.tasks.R13`); a run started by hand carries no limit. |
| `GET/POST /Backup`, `/Backup/Create`, `/Backup/Restore`, `/Backup/Manifest` | ❌ | Catalog only. |
| `GET /Plugins`, `/Plugins/{id}`, `.../Configuration`, `.../Manifest`; `GET /Packages`, `/Packages/{name}` | ❌ | Catalog only — deliberately out of scope per `docs/01`'s plugin-loading non-goal; listed for completeness, not expected to ever move to ✅. |
| `GET /Localization/Countries`, `/Cultures`, `/Options`, `/ParentalRatings` | ✅ | All four served (misc.localization). |
| `GET /GetUtcTime` | ✅ | Reception and transmission instants (misc.time.R1). |
| `POST /ClientLog/Document` | ❌ | Catalog only — client crash/log upload endpoint. |

---

## Dashboard — Libraries (admin library management)

A dedicated pass over `/dashboard/libraries` and every modal it opens — page load, "Add
Media Library", the per-library "⋮" menu ("Edit images", "Manage library",
"Rename", "Scan library", "Remove") — captured live via Playwright against both the
a reference server and Tinyfin, same method as the rest of
this doc (source 3). Unlike the browsing-focused capture this doc started from, this pass logged
into the real admin dashboard and drove every modal open, not just the routes a non-admin session
would ever see.

**The headline finding — resolved: adding a library silently dropped every folder path.**
jellyfin-web's "Add Media Library" → OK submits
`POST /Library/VirtualFolders?collectionType=movies&refreshLibrary=true&name=<name>` with the
folder list carried in the **request body**, under `LibraryOptions.PathInfos` (confirmed live
against the reference server — full body captured:
`{"LibraryOptions":{...,"PathInfos":[{"Path":"/media/movies"}],...}}`, and the resulting
`GET /Library/VirtualFolders` response showed the new library with
`"Locations":["/media/movies"]`, matching). `library_admin.go`'s `handleAddVirtualFolder` used to
only ever read `paths` from the **query string** (`r.URL.Query()["paths"]`) — a shape the real
dashboard UI never actually sends (its query string carries only `collectionType`, `refreshLibrary`,
and `name`) — so a library added through the real admin dashboard was silently created with an
**empty `Paths` array** (`204 No Content`, indistinguishable from success). **Fixed**:
`handleAddVirtualFolder` now decodes the JSON body first and extracts
`LibraryOptions.PathInfos[].Path` when present, falling back to the `paths` query param only when
the body carries none (keeping the old curl-friendly shape working too). Confirmed live end to end
against a test deployment: a library added with the exact body shape jellyfin-web sends now shows
up with its real folder in `Locations`, and a subsequent scan walks it. This was the single most
impactful gap this pass found — every other issue below was a modal that didn't fully render or a
menu action that 404d, but this one silently produced a broken library that *looked* successful.

Page load (`GET /dashboard/libraries` itself) fires, beyond the already-catalogued `System/Info` and
`Library/VirtualFolders`:

| Route | Status | Note |
|---|---|---|
| `GET /web/ConfigurationPages?enableInMainMenu=true` | ✅ | Resolved by the later `/dashboard` overview pass — see "Dashboard — Overview" below and `internal/api/system.go`'s `handleConfigurationPages`. |
| `GET /ScheduledTasks?isHidden=false` | ✅ | Resolved by the later `/dashboard` overview pass — see its row in "System/admin" above and "Dashboard — Overview" below. |

Opening "Add Media Library" (and, identically, "Manage library" on an existing
library — same modal, pre-filled) fires:

| Route | Status | Note |
|---|---|---|
| `GET /Localization/cultures`, `GET /Localization/countries` | ✅ | Implemented (`internal/api/localization.go`, `requireAuth`). Populate the "Preferred download language" and "Country/Region" dropdowns. Serve the real ISO 639-1/639-2 (192 entries) and ISO 3166 (139 entries) datasets — standard reference data, not the reference server's own IP — with field names confirmed live against the reference server. Confirmed live against a test deployment that the "Library settings" section of the modal now renders past "Pastas" instead of being gated off. |
| `GET /Libraries/AvailableOptions?IsNewLibrary=false` (fired once with no `LibraryContentType`, again once the content-type dropdown is set, e.g. `&LibraryContentType=movies`) | ✅ | Implemented (`internal/api/localization.go`, `requireAuth`). Not previously catalogued under any name — a genuinely new route this pass found, not in the SDK/apiclient bundle's URL-template scan because it's built from separate path segments. Response shape (`MetadataSavers`/`MetadataReaders`/`SubtitleFetchers`/`LyricFetchers`/`MediaSegmentProviders` + per-kind `TypeOptions[]`) confirmed live against the reference server. Scope decision: since Tinyfin has no metadata-provider *registry* (TMDB is hardcoded, per `internal/metadata`), the fetcher lists are honest, not a copy of the reference server's full plugin catalog — `MetadataFetchers`/`ImageFetchers` list only `TheMovieDb`, only for the kinds `internal/metadata.TMDBProvider` actually covers (Movie/Series/Season/Episode; every other kind gets empty lists), `MetadataReaders` lists `Nfo` only where `internal/scan/nfo.go` actually reads one, `MetadataSavers` is always empty (nothing writes a sidecar file back). `SupportedImageTypes`/`DefaultImageOptions` are copied faithfully for every kind (wire facts about the item model, not a capability claim). Confirmed live against a test deployment. |

Clicking "Adicionar" under "Pastas" (the folder browser) fires:

| Route | Status | Note |
|---|---|---|
| `GET /Environment/DefaultDirectoryBrowser`, `GET /Environment/Drives`, `GET /Environment/DirectoryContents?includeDirectories=true&path=...`, `GET /Environment/ParentPath?path=...`, `POST /Environment/ValidatePath` | ✅ | Implemented (`internal/api/environment.go`, admin-only), operating on the real container filesystem. Response shapes confirmed live against the reference server: `DefaultDirectoryBrowser` → `{}` (no `Path` key, not a default `"/"`); `Drives` → one entry per root (`[{"Name":"/","Path":"/","Type":"Directory"}]` on Linux/container, unlike the reference server's Windows-drive-letter list — scope decision, single root only); `DirectoryContents` → `[{"Name","Path","Type"}]`, neither `includeDirectories` nor `includeFiles` defaults to "everything" (both unset returns `[]`, confirmed live); `ParentPath` → a bare JSON string, `null` at the filesystem root (confirmed live, not `"/"`); `ValidatePath` body `{"Path":...,"ValidateWriteable":...}` (note spelling, confirmed from the apiclient bundle) → 204 if the path exists (confirmed live to accept files, not just directories — the reference server's own certificate-path picker validates a file through this same route). Confirmed live end to end against a test deployment: `Drives` and `DirectoryContents?path=/media` both return the real mounted folders. |

Saving "Gerenciar biblioteca" (edit an existing library's options) fires a route not in the SDK/
apiclient catalog scan at all:

| Route | Status | Note |
|---|---|---|
| `POST /Library/VirtualFolders/LibraryOptions` | ✅ | Implemented (`internal/api/library_admin.go`'s `handleSaveLibraryOptions`, admin-only), body `{"Id":"<libraryItemId>","LibraryOptions":{...full options object...}}` — addressed by `Id`, unlike `DELETE /Library/VirtualFolders`'s by-`Name` addressing, matching the reference server. This is the *only* way to change an existing library's options at all — `POST /Library/VirtualFolders` (the add route) only ever creates. Scope decision: the ~35-field `LibraryOptions` object is persisted as an opaque raw-JSON blob (`storage.Library.LibraryOptions`, new sqlite column/migration `0004_library_options.sql`) and echoed back verbatim on the next `GET /Library/VirtualFolders` — a save-and-echo round trip, not an interpretation of any of those fields (Tinyfin doesn't act on real-time-monitoring/trickplay/etc. settings today). 404 if the library `Id` doesn't exist, 400 on malformed JSON, 204 on success. Confirmed live end to end against a test deployment: save then re-fetch returns the same `LibraryOptions` blob that was saved. |

The per-library "⋮" menu's other three actions:

| Route | Status | Note |
|---|---|---|
| "Scan library" (per-library scan) | ✅ | See `POST /Items/{itemId}/Refresh`'s updated row above — implemented, confirmed live against a test deployment (queues the one library's scan). |
| "Rename" | ✅ | Implemented (`internal/api/library_admin.go`'s `handleRenameVirtualFolder`, admin-only), `POST /Library/VirtualFolders/Name?name=<current>&newName=<new>&refreshLibrary=<bool>` — route shape confirmed by static analysis of Tinyfin's own embedded `@jellyfin/sdk` bundle (not live-tested against the reference server: its own confirmation dialog warns renaming discards all metadata, too destructive to trigger against the persistent fixture). Looks up the library by current `name`, renames it (ID/Paths/LibraryOptions preserved), queues a scan if `refreshLibrary=true`; 404 if `name` doesn't match a library, 400 if `newName` is empty. Confirmed live against a test deployment: rename-then-list shows the new name, folders intact. |
| "Remove" | ✅ | Uses the already-implemented `DELETE /Library/VirtualFolders?name=<name>`; not re-confirmed live in this pass (would delete a real library) but the route and its by-`Name` addressing were already confirmed in the page-load capture above. |

### The two library settings pages that loaded nothing (fixed 10 Nov 2026)

Both of these are reachable from the dashboard drawer, under Server, and both used to stop at a
load-error alert instead of their form: `#/dashboard/libraries/display` ("Exibir") and
`#/dashboard/libraries/nfo` ("NFO"). Root cause, measured live against the Sala deployment, not
inferred from the client's code:

| Page | What it loads | Then | Now |
|---|---|---|---|
| `#/dashboard/libraries/display` | `GET /System/Configuration` + `GET /System/Configuration/metadata` | 200 + **404** | 200 + 200 |
| `#/dashboard/libraries/nfo` | `GET /System/Configuration/xbmcmetadata` + `GET /Users` | **404** + 200 | 200 + 200 |

The two missing routes were named configuration sections (`GET`/`POST /System/Configuration/{key}`),
the same mechanism `encoding`, `network` and `branding` already used, so both pages now render and
save. What the reference serves, measured there the same day, which is what these sections serve
back:

- `metadata` → `{"UseFileCreationTimeForDateAdded":true}` (one member: which timestamp becomes
  `DateAdded`; `true` is the file's own, `false` the moment the library scanned it in). Tinyfin
  reports `true` as well and always dates by the file's timestamp, so the scan-time option is stored
  and not honored (`DIV-30`).
- `xbmcmetadata` → `{"ReleaseDateFormat":"yyyy-MM-dd","SaveImagePathsInNfo":true,"EnablePathSubstitution":true,"EnableExtraThumbsDuplication":false}`,
  plus `UserId` once a value was sent. A `POST` of either section answers 204 and the next `GET`
  returns what was sent. Every member is stored and served back and none is honored: this server
  writes no `.nfo` sidecar (`DIV-31`).

Both are save-and-echo, the same scope decision `LibraryOptions` already carries, and both agree
with the reference member by member except the `metadata` default, which is recorded rather than
copied because `false` is the truthful value for this server.

Also present in the bundle but not surfaced by any modal this pass exercised (no per-path
add/remove UI exists in this jellyfin-web build's library-card menu — only whole-library add/
rename/remove): `POST /Library/VirtualFolders/Paths` (add one path to an existing library) and
`POST /Library/VirtualFolders/Paths/Update` (replace one path with another). Neither is registered
in `server.go`; `library_admin.go`'s own doc comment already lists "per-path add/remove/update on
an existing library" as a deliberate, documented gap — this pass just confirms the exact route
shapes from the bundle rather than changing that call.

---

## Dashboard — Overview

A dedicated pass over `/dashboard` itself (the admin overview page, "Dashboard"), same method as "Dashboard — Bibliotecas" above: live Playwright network
capture on page load against both the reference server and Tinyfin.
Six routes 404d on Tinyfin that the page calls unconditionally on every load:

| Route | Status | Note |
|---|---|---|
| `GET /Items/Counts` | ✅ | See its row in "Library and item navigation" above. Drives the Movies/Series/Episodes counter widget. |
| `GET /System/Info/Storage` | ✅ | See its row in "System/admin" above. Drives the "Locations" storage-bars widget. |
| `GET /Sessions?activeWithinSeconds=960` | ✅ | See its row in "Users, sessions, devices" above. Drives the "Devices" widget. Registered admin-only, not `requireAuth`: it lists every tracked session across every user (IP address, device name, client included) and the admin Dashboard is the only confirmed caller — exposing that to any authenticated non-admin user would be a real information-disclosure regression, not a scope simplification, so this diverges from this project's usual "authenticated-but-not-admin" pattern for listing routes deliberately. |
| `GET /System/ActivityLog/Entries` (called twice: `hasUserId=true` for the "Activity" widget, `hasUserId=false` for the "Alerts" widget) | ✅ | See its row in "System/admin" above. |
| `GET /ScheduledTasks?isHidden=false` | ✅ | See its row in "System/admin" above. Already noted as a `/dashboard/libraries`-page gap too (that section's rows now point here). |
| `GET /web/ConfigurationPages?enableInMainMenu=true` | ✅ | Implemented (`internal/api/system.go`'s `handleConfigurationPages`, `requireAuth`) — always `[]`, matching the reference server's own response with no plugins installed (Tinyfin has no plugin system at all, per `docs/01`). Had to be special-cased in `cmd/tinyfin/webui.go`'s static-file-serving wrapper too: that wrapper treats every `/web/...` path as a jellyfin-web static asset by default, which would otherwise shadow this route entirely (a 404 from the file server, not from the API) whenever a web bundle is mounted. |

**A real, previously-hidden wire-compatibility bug found in the process, unrelated to any of the
six routes above**: a real jellyfin-web build sends this project's client-identification header
with its `Client` field percent-encoded — literally `Client="Jellyfin%20Web"` — and the reference
server decodes it before reporting it back anywhere. `internal/api/auth.go`'s `parseEmbyAuth` never
decoded any field at all; this went unnoticed until now because no earlier route ever echoed
`AuthContext.Client` back on the wire — `GET /Sessions` (above) is the first one that does, and it
surfaced literally as `"Client":"Jellyfin%20Web"` in the Dashboard's "Devices" widget in this
pass's own testing. Fixed by percent-decoding every field (`url.PathUnescape`, not `QueryUnescape` —
a literal `+` in a base64-shaped `DeviceId` must stay a `+`, not become a space); confirmed live
against a test deployment that the widget now reads "Jellyfin Web 12.0.0" like the reference server
does, and covered by a new unit test (`internal/api/auth_test.go`).

WebSocket `/socket` was the one confirmed-live 404 this page still produced on every load after
this pass — closed in a follow-up pass: a black-box capture plus a reference-source read (on the
maintainer's explicit request) extracted the full wire protocol and push semantics into
`docs/12-clean-room-behavior-specs.md`'s "Dashboard overview — the `GET /socket` realtime channel"
section, including a dedicated review of `jellyfin/jellyfin`'s own public issue tracker for real,
recurring architecture bugs in its WebSocket implementation (concurrent-write crashes, connections
that never get cleaned up, a zombie-session/SyncPlay bug) to design around rather than reproduce.
Implemented in `internal/api/websocket.go` and confirmed live against a test deployment with a real
jellyfin-web build — see this section's own `GET /socket` row above.

**A second, later bug on this same page**: the "Scan All Libraries"
button was reported to spin forever, click or no click. Root cause was in `GET
/ScheduledTasks`'s response, not the button's own code: this project's one synthetic task reported
`Id`/`Key` as an arbitrary internal string (`"library-scan"`), but jellyfin-web's Dashboard home
widget locates that task by filtering the task list on `Key === 'RefreshLibrary'` — a value hardcoded
client-side to match the reference server's own internal task key. With no match, the widget's
`librariesTask` was always `undefined`, and its `isScanning` check (`librariesTask?.State !==
'Idle'`) evaluated `true` unconditionally — the spinner had nothing to do with whether a scan was
actually running. Root-caused by reading jellyfin-web's own source (client-side JS, not the
reference server's C#, so outside AGENTS.md's clean-room requirement) after black-box network
capture alone couldn't explain a click that produced no request at all. Fixed by reporting
`"RefreshLibrary"` and implementing the route the button calls once it does find the task,
`POST /ScheduledTasks/Running/{taskId}` (previously catalog-only) — see this section's own
`GET /ScheduledTasks`/`POST /ScheduledTasks/Running/{taskId}` rows above.

---

## Explicitly out of scope (not counted above)

Per `docs/01-vision-and-scope.md`'s non-goal and `docs/04`'s own framing: Live TV and Channels
(DVR-related controllers) — `LiveTv/*` (~20 routes: Channels, Recordings, Timers, TunerHosts, ...)
and `Channels/*` (~5 routes). These showed up in the SDK catalog and, for `LiveTv/Programs*`, in live
capture (404, expected) — listed here only so the "known routes" count above isn't accused of
silently missing them; they were seen and deliberately excluded, not missed.

One exception, moved out of this out-of-scope list: `GET /LiveTv/Programs` specifically. jellyfin-web's
series detail page calls it unconditionally (found via the series/episode revalidation, not just
catalog inspection) even with no LiveTV tuner ever configured — confirmed against the reference
server that it comes back `200` with an honest-empty `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`
rather than 404. Registered as exactly that stub (`library.go`'s `handleLiveTvPrograms`), same
"honest empty over 404" precedent as `GET /Items/{itemId}/Similar`/`GET /Studios` — no LiveTV logic of
any kind added, just the one response shape a client that never expects LiveTV to exist here still
needs to not treat as broken. Every other `LiveTv/*`/`Channels/*` route stays out of scope and 404s.

---

## Biggest real gaps for ordinary browsing

Ranked by how much they'd actually bite a normal user clicking around, not by API-surface size:

0. ~~**Adding a library through the real admin dashboard silently creates one with zero
   folders.**~~ — resolved. `handleAddVirtualFolder` now reads `LibraryOptions.PathInfos` from the
   request body (what the real dashboard actually sends), falling back to the `paths` query param
   only when the body carries none; confirmed live end to end against a test deployment with the
   exact body shape jellyfin-web sends. See "Dashboard — Libraries (admin library management)"
   above for the full capture and fix.
1. ~~**Favorites and "mark watched" are silently broken**~~ — resolved. Both
   `POST /Users/{userId}/FavoriteItems/{itemId}` and `POST /Users/{userId}/PlayedItems/{itemId}` (+
   `DELETE`) are implemented and re-audited byte-for-byte against the reference server; see their rows
   above.
2. ~~**`/Shows/NextUp` (404)** breaks "what to watch next" on every TV series~~ — resolved.
   Implemented and confirmed live (home screen row and series detail page); see its row above.
3. **No subtitle-delivery routes at all.** Confirmed as a route family real clients actively use
   mid-playback (reference-server capture). Any title with subtitle tracks will fail to display them —
   a Direct Play/Remux correctness issue, not just a missing nicety.
4. **`/Artists` and `/Persons` 404** — search results for cast/crew and music artists come back empty
   with no indication why; same likely applies to any dedicated artist/person browsing screen, not just
   search.
5. ~~**`/Items/{itemId}/Collections` and `/Items/{itemId}/ThemeMedia` both 404 on every single item
   detail page.**~~ — `ThemeMedia` resolved (see its row above). `/Items/{itemId}/Collections` is
   still a 404, and neither of these actually breaks anything visibly (the "part of a collection"
   banner and background theme song just don't appear) — `/Collections` remains a cheap
   honest-empty-stub candidate, same pattern already used for `/Studios` and `/Items/Filters`, to
   remove the one remaining guaranteed failed request from every page view.
