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
   extracted from the `tinyfin-staging` container's own `/web`) — every URL-template string literal
   in the bundle, ~250 distinct path patterns. This is the full catalog of routes the modern SDK
   client code *can* call, tree-shaken to what jellyfin-web actually imports.
2. **Static analysis of the embedded legacy `jellyfin-apiclient` bundle** — this package builds URLs
   by string concatenation rather than template literals, so it doesn't show up in (1); grepped for
   path-suffix literals instead (`/InstantMix`, `/FavoriteItems/`, `/Refresh`, etc.) to find routes
   the SDK-bundle scan would otherwise miss entirely.
3. **Live network capture (Playwright)** clicking through home, movie library, movie detail and
   playback, TV series → season → episode, search, favorite/watched toggles — run against **both**
   the reference Jellyfin server (`localhost:8097`, official image) and Tinyfin itself
   (`localhost:8096`, `tinyfin-staging`).
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
- ⚠️ **Parcial** — registered and returns `200`, but is a hardcoded honest-empty stub (no backing
  data model yet) or an under-permissioned admin route. Confirmed by reading the handler bodies in
  `internal/api/library.go`, not just guessed from behavior.
- ❌ **Não implementado** — no route registered; a real client request 404s (confirmed live for most
  entries below via the Playwright capture against `localhost:8096`; a smaller number are catalog-only,
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
then closed every gap that pass found — see "Dashboard — Bibliotecas (admin library management)". A
later pass over `/dashboard` itself (the admin overview page) found and closed six more route gaps
plus one previously-hidden auth-header decoding bug — see "Dashboard — Painel (overview)". These
counts are recounted directly from the tables below (`grep`-counted status markers), not
hand-tracked incrementally across passes — the project's established practice after an earlier
count drifted from the table rows it was supposed to summarize.

---

## Library and item navigation

| Route | Status | Note |
|---|---|---|
| `GET /UserViews` | ✅ | Home screen library list. Re-audited byte-for-byte against the reference server (staging, :8097): found and fixed two real divergences — `Type` was `"UserView"`, reference sends `"CollectionFolder"` for an ordinary library; `UserData` was omitted entirely, reference populates it same as any other item (`dto.go`'s `libraryBaseItemDto`). |
| `GET /Items` | ✅ | Generic listing/search, query-param filtered. Confirmed live (movie library grid, search). Also serves the Favoritos tab's per-user form, `GET /Users/{userId}/Items?Filters=IsFavorite&...` — re-audited byte-for-byte against the reference server; found and fixed two real divergences: (1) `SortBy=SeriesSortName,SortName` (what the Favoritos tab actually sends for Episode/Season rows) silently dropped the `SeriesSortName` key entirely, so favorited episodes from different series interleaved alphabetically by episode title instead of grouping by series — now a real sort key (`storage.SortBySeriesName`, a self-join to the owning series' `sort_name`). (2) `TotalRecordCount` ignored `EnableTotalRecordCount=false` (sent on every Favoritos-tab call) and always returned the real total instead of just how many items that response carries, confirmed against the reference server on both a first and second page. See the note below and "Users, sessions, devices" for the toggle routes it depends on. |
| `GET /Items/Latest` | ✅ | Home "recently added" rows. Confirmed live, exact query-param shape (`userId`+`parentId`) matches what Tinyfin's embedded bundle sends. A TV-shows library request now groups by series (one card per series — a `Season`, the `Series` itself, or a single `Episode`, per `docs/12`'s "Items/Latest — series grouping"), resolved via the clean-room two-phase process (spec extracted to `docs/12`, implemented from that spec plus live black-box comparison against the reference server, `internal/api/latest_grouping.go`) rather than left as the flat, ungrouped list of every recently-scanned `Episode`/`Season` row this used to return. `groupItems=false` (confirmed live, any capitalization) still returns that original flat list; the movies-library case stays flat unconditionally, matching the reference server's own "no grouping" behavior there. A series every one of whose episodes is played (`PlayCount > 0`) for the requesting user is excluded from the grouped result entirely — confirmed live, doesn't consume a `limit` slot by being excluded — see `docs/12`'s "Fully watched series are excluded" section. `docs/12`'s "Implementation findings" section records one remaining reference-server behavior intentionally not reproduced (an unresolved card-selection quirk on month-plus-old bursts) — out of scope for this gap, logged for later. |
| `GET /Items/{itemId}` | ✅ | Item detail. Confirmed live (`?userId=` query form). Re-audited against the reference server for the movie detail page specifically: `ImageTags`/`BackdropImageTags` used to only ever carry `Primary` even when the scanner had already indexed a servable `Backdrop`/`Logo`/`Banner` — now every single-instance kind the item actually has folds into `ImageTags` and every `Backdrop` into its own `BackdropImageTags` array (`dto.go`'s `imageTagsFor`), confirmed live: `ImageTags` now shows `{"Primary":..., "Logo":..., "Banner":...}` and `BackdropImageTags` a populated array for a real staging title. `People` (cast/crew) is also now populated when the item has a TMDB-enriched credit list (`storage.Item.People`, `dto.go`'s `peopleDtos`) — not live-validated against a real TMDB key (none configured in staging), but covered by unit tests with a fake provider (`internal/metadata/tmdb_test.go`, `internal/scan/scan_test.go`, `internal/api/library_test.go`). Re-audited a second time (series/episode revalidation): `GenreItems` (a stable Id per `Genres` entry, `dto.go`'s `genreItemsFor`), `ExternalUrls` (TheMovieDb/IMDb links derived from `ProviderIDs`, `externalURLsFor`), and `ChildCount` (Series/Season/BoxSet only, direct non-recursive child count, `childCountFor`) were entirely absent from the DTO — now populated and confirmed live against a real staging title (`docker exec` + `curl`), matching the reference server's shape. `MediaSources` is now also embedded directly on this response (`dto.go`'s `mediaSourcesFor`, reusing `playback.go`'s `baseMediaSourceInfo` minus its device-profile negotiation), not just on `/PlaybackInfo` — confirmed live. `MediaStreams` entries now also carry `DisplayTitle` plus the additional passthrough fields (`Profile`, `ChannelLayout`, `TimeBase`, `AspectRatio`, `RefFrames`, `PixelFormat`, `Level`, `SampleRate`) DisplayTitle's algorithm needs — see docs/12's "Episode/movie detail page — MediaStream.DisplayTitle" for the full writeup and its "Implementation findings" for the live byte-for-byte comparison against the reference server. `Studios` (a stable Id per studio/network name, `dto.go`'s `studioItemsFor`, same `NameIdPair` shape as `GenreItems`) and `EndDate` are now also populated for a Series when TMDB enrichment supplies them (`storage.Item.Studios` from TMDB's `networks` field; `EndDate` from `last_air_date`, only once TMDB's `status` field is a terminal value — `Ended`/`Canceled`, not `Returning Series`/`In Production`/`Planned`/`Pilot`) — same "not live-validated, no TMDB key in staging" caveat as `People` above, covered by unit tests (`internal/metadata/tmdb_test.go`, `internal/scan/scan_test.go`, `internal/api/library_test.go`). Not curled live against this endpoint specifically (no admin credentials available in this session to authenticate against `tinyfin-staging`); degradation was instead checked one level down — `tinyfin-staging` rebuilt with this change and restarted cleanly, its `0003_studios` migration applied automatically (schema version 2 → 3), and its pre-existing Series/Season/Episode/Movie rows (338 items total) came through the restart with a correctly-defaulted empty `studios` column and no error/panic in the server logs. |
| `GET /Items/{itemId}/Ancestors` | ✅ | Breadcrumb trail. Confirmed live. |
| `GET /Users/{userId}/Items/{itemId}` | ✅ | Legacy per-item alias — still hit on *every* detail page load before the plain form, per `server.go`'s own comment (found via real client testing, not spec). Confirmed live. |
| `GET /UserItems/Resume` | ✅ | Continue-watching row. Confirmed live, exact match against Tinyfin's own bundle. Re-audit found and fixed a related bug one level down, in the mark-watched routes: a stale in-progress position could resurrect an already-unmarked item here — see the `PlayedItems`/`UserPlayedItems` notes below. |
| `GET /Shows/{seriesId}/Seasons` | ✅ | Confirmed live, real season data returned. A Season item is now also enriched (`internal/scan/scan.go`'s `walkSeasons`, `metadata.KindSeason` — TMDB's `GET /tv/{id}/season/{n}`), the same async enrich-then-reupsert pattern the series/movie/episode walks already use, giving a real localized name/overview/poster instead of the always-generic `"Season N"` filename fallback; not live-validated against real TMDB data (no key in staging) but covered by `internal/metadata/tmdb_test.go` and `internal/scan/scan_test.go`. Degraded-path check was done at the deploy level, not by triggering a live rescan (no admin credentials available in this session to call `POST /Library/Refresh` against `tinyfin-staging`): the rebuilt image starts and serves its existing, already-scanned Season rows with no error/panic, and this endpoint's own route wasn't independently re-curled this pass. **Local season-poster gap closed**: a real jellyfin-web-vs-reference-server traffic comparison found a season card (Dragon Ball Season 1) with no `ImageTags.Primary` on Tinyfin (gray placeholder) while the reference server showed the real poster, even though both point at the same disk — the series root folder had a `season01-poster.jpg` (Kodi's per-season-poster convention, kodi.wiki's "Artwork" guide) that `walkSeasons` never looked for; TMDB-sourced season images were already wired correctly via `enrich`/`storeProviderImage`, this was purely a missing local-file lookup. Fixed with a new `seedSeasonImage` (`internal/scan/scan.go`), called before enrichment same as the movie/series local-artwork seeding it mirrors; covered by `TestScanLibrarySeasonPosterDetected` in `internal/scan/scan_test.go` and confirmed live against `tinyfin-staging` for this exact series: `GET /Shows/{seriesId}/Seasons?userId=...` now returns `ImageTags: {"Primary": "1"}` for Season 1, and `GET /Items/{seasonId}/Images/Primary` serves bytes MD5-identical to the on-disk `season01-poster.jpg`. |
| `GET /Shows/{seriesId}/Episodes` | ✅ | Confirmed live, real episode data returned. Re-audit (series/episode revalidation) found a real bug: `?SeasonId=` (the casing jellyfin-web's season-detail-page call actually sends, confirmed via Playwright network capture) was read via a raw, case-sensitive `r.URL.Query().Get("seasonId")` in `handleShowEpisodes` (`library.go`), so it never matched and silently fell back to "every episode of the series" — the "More from Season X" carousel showed all ~103 episodes of a series instead of that season's ~37. Fixed by routing through `caseInsensitiveQuery` (the project's established pattern, already used elsewhere in this file); confirmed live against `tinyfin-staging` for a real 3-season title (Chapolin Colorado): `?SeasonId=<season1>` (PascalCase) now returns 37 items matching `?seasonId=` (lowercase), not the full 103. |
| `GET /Items/Filters` | ⚠️ | Registered, always returns empty facets (`library.go:502`) — "no such metadata yet, honest empty over 404" per its own doc comment. Confirmed live (200, empty). |
| `GET /Items/Filters2` | ⚠️ | Same stub as above (`library.go:506`). Confirmed live. |
| `GET /Items/{itemId}/Similar` | ⚠️ | Always returns empty list (`library.go:483`). Confirmed live. |
| `GET /Shows/NextUp` | ✅ | Implemented (`internal/api/nextup.go`). Rule reverse-engineered by black-box testing against the reference server (staging, :8097): per series, find the last episode (by season/episode order, not watch date) with `PlayCount > 0`; if it's not fully played, that same episode is the candidate (in-progress/"resume", included only when `enableResumable` isn't `false`); if fully played, the candidate is the next episode after it in order (crossing season boundaries correctly), or none if the series is finished (`enableRewatching` restart-at-episode-1 is a documented decision, not something the real server reproduced under test — see the handler's doc comment). Confirmed: a series with zero activity never appears (not "episode 1" as a naive reading of the endpoint name might suggest); marking an out-of-order episode played jumps straight to the one after it, not the first unwatched one; `nextUpDateCutoff` is a minimum-date bound; `EnableTotalRecordCount=false` reports `0`, diverging from `GET /Items`' own rule for that same param (`resolveTotalRecordCount`) — deliberately not reused unchanged. Also fixed a related pre-existing gap this depends on: `PlaystateStore.LastPlayedDate` was never actually written by any handler (`userdata.go`, `sessions_playback.go`) despite the storage field existing — now stamped/cleared to match confirmed reference-server behavior. Confirmed live end to end: `curl` against `tinyfin-staging` matches the reference server for every case above, and a Playwright session shows the home screen's "A seguir" row rendering a real next episode after marking a prior one watched, with no console/network errors. Re-audit (series/episode revalidation) found a second real bug of the same shape as `Episodes?SeasonId=` above: `?SeriesId=` (PascalCase, the series-detail-page's own casing) was read via a raw, case-sensitive `r.URL.Query().Get("seriesId")`, so it never matched and silently fell back to "no series requested" — every series with progress leaked into a single series' "Next Up" widget (confirmed live: opening one series' detail page showed another series' next episode in the row). Fixed the same way, routed every param this handler reads through `caseInsensitiveQuery`, not just `seriesId`; confirmed live against `tinyfin-staging` with three series each carrying progress: `?SeriesId=<id>` (PascalCase) now returns exactly that series' own candidate. |
| `GET /Items/{itemId}/Collections` | ❌ | Confirmed 404 live, called unconditionally on **every** item detail page (movie, series, season) to check "part of a collection" banner. |
| `GET /Items/{itemId}/ThemeMedia` | ✅ | Implemented (`internal/api/theme_media.go`), per `docs/12`'s "Movie detail page — Theme Media" spec — resolved at request time (no scan-time indexing) by checking the item's own folder (derived from `storage.Item.Path`) for a standalone `theme.<ext>` file or any audio file inside `theme-music/` (→ `ThemeSongsResult`), and any video file inside `backdrops/` (→ `ThemeVideosResult`); `SoundtrackSongsResult` always empty (not a movie concept). Confirmed live end to end against the reference server's own empty-shape response (staging title has no theme files) and, separately, by dropping a real `theme.mp3` into a staging movie folder and confirming it showed up in `ThemeSongsResult.Items` on the next request with no rescan needed — then removing it and confirming the response reverted to empty. `/ThemeSongs`/`/ThemeVideos` (separate legacy routes some older clients may call) are still unimplemented — out of scope for this gap, not exercised by jellyfin-web's own detail page. |
| `GET /Users/{userId}/Items/{itemId}/Intros` | ❌ | Confirmed 404 live (legacy per-user form) — pre-roll trailers/intros before playback starts. Non-blocking (player just skips), but a real, frequently-hit gap. |
| `GET /Items/{itemId}/LocalTrailers` | ❌ | From SDK/apiclient catalog. Not exercised live (test library has no local trailer files) but a real registered client call. |
| `GET /Items/{itemId}/SpecialFeatures` | ❌ | Catalog only, same caveat as above. |
| `GET /Items/Root` | ❌ | Catalog only — media-folder root discovery. |
| `GET /Items/Counts` | ✅ | Implemented (`internal/api/items_counts.go`, `requireAuth`), found via the `/dashboard` (Painel) overview pass — see "Dashboard — Painel (overview)" below. `MovieCount`/`SeriesCount`/`EpisodeCount` are real (`QueryItems` with `Limit: 1`, reading `TotalRecordCount`, per the no-full-catalog-scan rule every other count-shaped read in this project follows); every other field (`ArtistCount`, `ProgramCount`, `TrailerCount`, `SongCount`, `AlbumCount`, `MusicVideoCount`, `BoxSetCount`, `BookCount`, `ItemCount`) is a structural zero, not fabricated — `internal/scan` never produces any of those item types. Confirmed live against `tinyfin-staging`. |
| `POST /Items/{itemId}/Refresh` | ✅ | Implemented (`internal/api/item_refresh.go`, admin-only). Originally catalogued as "single item refresh"; the `/dashboard/libraries` pass found this is actually the **primary per-library scan mechanism** too — jellyfin-web's "Rastrear biblioteca" (scan just one library) menu action calls exactly this route with the library's own `ItemId`, not a library-scoped variant: `POST /Items/{itemId}/Refresh?Recursive=true&ImageRefreshMode=Default&MetadataRefreshMode=Default&ReplaceAllImages=false&RegenerateTrickplay=false&ReplaceAllMetadata=false` (query params accepted and ignored — Tinyfin has no refresh-mode distinctions to honor). When `itemId` matches a library's own `ItemId`, queues that library's scan (`s.runScan`), same mechanism `POST /Library/Refresh` uses for every library — confirmed live against `tinyfin-staging` (204, scan ran). When `itemId` matches an ordinary catalog item instead, queues a real per-item re-fetch (`Server.runItemRefresh` -> `scan.Scanner.RefreshItem`) — Movie/Series/Season/Episode only, the same set `ScanLibrary` itself enriches; any other type is a silent no-op. The re-fetch re-reads **both** local sources (a sidecar `.nfo`, folder-convention artwork — `poster.jpg`, `seasonNN-poster.jpg`, ...) *and* the configured remote `metadata.Provider`, same order as a fresh scan (local artwork seeded, then the remote provider runs, then the local `.nfo` is applied last so it overrides the provider). The `ReplaceAllImages` query param is honored (validated against the reference server's own `ItemImageProvider`, `docs/08`'s "Local-vs-remote precedence" section): with it `false` (jellyfin-web's own default), a Provider-returned image never overwrites an image kind the item already has, local or remote; `true` lets it win. Series/Season store no filesystem `Path` of their own (only Movie/Episode do) — their folder is derived from one child episode's `Path` instead (`seriesFolder`/`seasonFolder`, `internal/scan/scan.go`). A Season's own `.nfo` handling is narrower than Series/Movie/Episode: it looks for `tvshow.nfo` in the season's own folder first, falling back to the series root, but only extracts a `<namedseason number="N">Custom Name</namedseason>` override for that season's display name (`applySeasonNFO`, `internal/scan/nfo.go`) — matching the reference server's own `SeriesNfoSeasonProvider`; found and implemented after a real series (`MIB - Homens de Preto`) turned up with its `tvshow.nfo` misplaced inside `Season 01/` instead of the series root, which validation against the reference server's source confirmed produces no effect on either server for that specific file (no `<namedseason>` tag present) — a media-library organization issue, not a bug. **Deliberately does not surface as "running" anywhere**: an earlier version routed this through the same `scanState`/`GET /ScheduledTasks` mechanism a library scan uses, on the reasoning that *something* should show progress — Rafael asked whether a per-item job would show as "scanning library" on the dashboard, and checking jellyfin-web's own source confirmed the reference server never conflates the two: a per-item refresh is tracked entirely separately (`BaseItemDto.RefreshProgress`/`RefreshStatus` on the item itself, plus a per-item `RefreshProgress` WebSocket message scoped by `ItemId` — `emby-itemrefreshindicator`/`RefreshIndicator.tsx` in jellyfin-web), never touching `GET /ScheduledTasks` or its `ScheduledTasksInfo` WebSocket push at all. Sharing `scanState` meant refreshing any single item made the Dashboard's "Scan All Libraries" button spin/disable itself with no library scan running — a variant of the exact bug this doc's own history above describes. Reverted; Tinyfin has no per-item progress indicator today (no `RefreshProgress`/`RefreshStatus` DTO fields, no per-item WebSocket push) — a real, documented gap, not silently faked via the wrong tracker. Regression-guarded by `TestItemRefreshDoesNotAffectScheduledTasks` (`internal/api/scheduled_tasks_test.go`). Used to be an honest no-op with no re-read of anything, local or remote: found live when refreshing a real series' metadata from the dashboard had no effect and nothing showed as running, then found live *again*, more specifically, that the fix must not stop at "call the remote provider" — the series' media folder already had a `tvshow.nfo` and a poster Tinyfin's own scanner already knows how to read, so an item can be "incomplete" and fixable with **zero** TMDB configuration at all; Rafael caught this distinction directly (see `AGENTS.md`'s jellyfin-web-source-reading practice for how the surrounding `GET /ScheduledTasks`/`Key` bug in this same feature area was found, a separate but adjacent fix). Unknown `itemId` (neither a library nor an item): 404. |
| `GET /Playlists`, `GET /Playlists/{id}`, `GET/POST /Playlists/{id}/Items`, `.../Users` | ❌ | No playlist support at all — catalog only, not exercised live (no playlists in test library). |
| `GET /Genres`, `GET /Genres/{genreName}` | ❌ | Catalog only. See "Specialized media types" below — genre browsing has no route at all. |
| `GET /Years` | ❌ | From apiclient's concatenated-suffix scan (year filter). No route registered. |

## Specialized media types

| Route | Status | Note |
|---|---|---|
| `GET /Shows/{seriesId}/Seasons`, `/Episodes` | ✅ | Counted above under library nav — this *is* the TV-show-specific slice of that category and it's solid. |
| `GET /Studios` | ⚠️ | Always returns empty list (`library.go:514`) — "no Studios field on storage.Item at all today." Confirmed live (used by both the movie-library filter panel and search). |
| `GET /LiveTv/Programs` | ⚠️ | Always returns an empty `QueryResult` (`library.go`'s `handleLiveTvPrograms`) — no LiveTV of any kind implemented (see "Explicitly out of scope" below), registered only because jellyfin-web's series detail page calls it unconditionally. Confirmed live: `curl` against `tinyfin-staging` returns `200 {"Items":[],"TotalRecordCount":0,"StartIndex":0}`. |
| `GET /Artists`, `GET /Artists/AlbumArtists` | ❌ | Confirmed 404 live (search screen's artist-results query). Music library browsing by artist is entirely unimplemented. |
| `GET /Persons`, `GET /Persons/{name}` | ❌ | Confirmed 404 live (search screen's cast/crew-results query). Person/cast browsing entirely unimplemented. |
| `GET /Genres/{name}/Images/{imageType}`, `GET /MusicGenres/{name}/Images/{imageType}` | ❌ | Catalog only. |
| `GET /Studios/{name}`, `GET /Studios/{name}/Images/{imageType}` | ❌ | Catalog only — per-studio detail/image, distinct from the (stub) `/Studios` list route. |
| `GET /Persons/{name}/Images/{imageType}`, `GET /Artists/{name}/Images/{imageType}` | ❌ | Catalog only. |
| `GET /Movies/Recommendations` | ❌ | Catalog only — movie-specific recommendation carousel (distinct from generic `/Items/{itemId}/Similar`). |
| `GET /Movies/{itemId}/Similar` | ❌ | Catalog only — movie-specific similar-items variant. |
| `GET /Shows/Upcoming` | ❌ | Catalog only. |
| `GET /Trailers/{itemId}/Similar` | ❌ | Catalog only. |

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
| `GET /Playback/BitrateTest` | ✅ | Implemented (`internal/api/playback.go`). Shape confirmed against the reference server (staging, :8097) via `curl`, not assumed: response is `Size` random bytes rounded UP to the next power of two (floor 16 — every `Size` from 1-16 comes back 16 bytes), `Content-Type: application/octet-stream`; `Size` omitted defaults to what a real server sends as its own default (100000, rounding to 131072); `Size` is validated `>= 1` and `<= 100000000` server-side (found by binary search against the real server's own 400 error, which spells the bounds out explicitly), mirrored here as the same bound. Confirmed live: `curl` against `tinyfin-staging` matches on default size, an explicit size (with rounding), the 400 boundary at both ends, and random (non-reproducible) content. |
| Trickplay (seek thumbnails) | ❌ | No dedicated route found actively called by this particular jellyfin-web build (the SDK bundle only references `Trickplay` as a requestable `Fields` enum value on item queries, not as its own endpoint) — plausibly because the test library has no pre-generated trickplay data to trigger the image-tile fetch. Not implemented in Tinyfin regardless; low confidence on exact call shape without a trickplay-enabled library to test against. |
| `SyncPlay/*` (List, Join, Leave, New, Ready, Seek, Pause, Unpause, Queue, ...) | ❌ | `GET /SyncPlay/List` confirmed 404 live, called unconditionally on nearly every screen transition (noisy but harmless — client treats 404 as "SyncPlay unavailable"). The rest of the ~18-route family (from the SDK catalog) is equally unimplemented; explicitly in-scope per `docs/04` (unlike Live TV/Channels) but a multi-client feature, lower priority for a single-user/idle-memory-focused v1. |
| `POST /LiveStreams/Open`, `POST /LiveStreams/Close` | ❌ | Catalog only — external tuner live-stream lifecycle, effectively Live TV-adjacent. |

## Images and branding

| Route | Status | Note |
|---|---|---|
| `GET /Items/{itemId}/Images/{imageType}`, `/{imageType}/{imageIndex}` | ✅ | Confirmed live implicitly — every list/grid/detail screen across the whole capture rendered real posters/backdrops/thumbnails. Deliberately unauthenticated (see `library.go`'s `handleItemImage` doc comment) — a real, tested requirement, not a spec guess. |
| `GET /Branding/Configuration` | ✅ | Confirmed live, fetched right after login. |
| `GET /Branding/Css`, `/Branding/Css.css` | ❌ | Catalog only — custom admin-configured CSS injection. No-op unless an admin has set one; low priority. |
| `GET /Branding/Splashscreen` | ❌ | Catalog only. |
| `GET /Items/{itemId}/RemoteImages`, `/RemoteImages/Providers`, `/RemoteImages/Download` | ❌ | Confirmed 404 live for `/RemoteImages/Providers` specifically, via the `/dashboard/libraries` → "Editar imagens" flow (any library card's own image is edited through the exact same generic item-image admin UI a movie/series detail page would use — the library is just another `itemId`). |
| `GET /Items/{itemId}/Images` (no `imageType` — the "list every image with metadata" admin variant, distinct from the already-implemented `GET /Items/{itemId}/Images/{imageType}` byte-serving route), `POST /Items/{itemId}/Images/{imageType}[/{imageIndex}]` (upload), `DELETE /Items/{itemId}/Images/{imageType}[/{imageIndex}]` (delete) | ✅ | Implemented (`internal/api/item_images_admin.go`, admin-only). `GET` lists every `ImageRef` the item has across all known kinds (honest-empty list, not 404, for an item with none) — confirmed live against `tinyfin-staging` for a real movie (Primary/Logo/Banner/Backdrop all listed with real dimensions/size). `POST`'s body is raw base64 image bytes (confirmed live against the reference server, not multipart), file extension follows `Content-Type`; for the one multi-instance kind (`Backdrop`), an `imageIndex` naming an existing image overwrites it in place, any other value (or none) appends as the next index — matches reference-server behavior confirmed live. `DELETE` is idempotent (204 whether or not the image/index existed), matching the reference server's own confirmed-live behavior, not a 404-on-miss guess. |

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
| `POST /Users/{userId}/FavoriteItems/{itemId}` (+ `DELETE`) | ✅ | Implemented (`internal/api/userdata.go`). Confirmed live and re-audited against the reference server (staging, :8097) byte-for-byte: response shape, `IsFavorite` persistence independent of `Played`/`PlayCount`, and the Favoritos tab (`#/home?tab=1`) reflecting the toggle. |
| `POST /Users/{userId}/PlayedItems/{itemId}` (+ `DELETE`) | ✅ | Implemented (`internal/api/userdata.go`). Confirmed live and re-audited against the reference server: `PlayCount` only advances on the unplayed→played transition and resets to 0 on unmark (both matched already). Re-audit found one real divergence, now fixed: POST didn't reset `PlaybackPositionTicks` to 0 on marking played, unlike the reference server — a stale in-progress position survived a mark-watched call and resurfaced the item in the home screen's "Continuar assistindo" row the moment it was later unmarked watched. Same fix applied to `POST /UserPlayedItems/{itemId}` (`sessions_playback.go`'s `handleUserPlayedItemsMark`), which had the identical gap. |
| `POST /UserItems/{itemId}/Rating` | ❌ | Catalog only — star rating. |
| `POST/GET /UserItems/{itemId}/UserData` | ❌ | Catalog only — direct playstate/favorite edit, alternate to the two routes above. |
| WebSocket `/socket` | ✅ | Implemented (`internal/api/websocket.go`), per the clean-room spec in `docs/12-clean-room-behavior-specs.md`'s "Dashboard overview — the `GET /socket` realtime channel" section (behavior extraction plus a dedicated "known chronic issues, deliberately not reproduced" subsection covering real, multi-year `jellyfin/jellyfin` architecture bugs this implementation was designed around from the start). Auth/keep-alive handshake, and the three push-on-event channels (`Sessions`, `ScheduledTasksInfo`, `ActivityLogEntry`) all confirmed live against `tinyfin-staging` with a real jellyfin-web build: `ForceKeepAlive`/`KeepAlive` handshake matches byte-for-byte, and triggering a real `POST /Library/Refresh` produced the expected `ScheduledTasksInfo` pushes (Running → Completed) with no server error. `RefreshProgress` (per-item scan progress) is a documented, deliberate gap — `internal/scan` has no per-item progress callback to hook yet. Remote-control command delivery over this channel (`GeneralCommand` et al) is a separate, still out-of-scope feature — see the row below. |
| `GET /Sessions` | ✅ | Implemented (`internal/api/sessions.go`, admin-only — see its row's own reasoning below), found via the `/dashboard` (Painel) overview pass. A small in-process session registry (docs/06's default mode), populated from exactly two existing signals: `POST /Sessions/Capabilities/Full` (previously entirely discarded) and the three playback-report routes touching an already-registered device's `LastActivityDate`. Deliberately narrow — no now-playing tracking, no expiry beyond the `activeWithinSeconds` filter itself (see "Dashboard — Painel (overview)" below). Confirmed live against `tinyfin-staging`, including a real jellyfin-web session showing up correctly in the admin Dashboard "Dispositivos" widget. Note: the WebSocket `Sessions` push channel above uses a *different*, per-connection filtering rule than this REST route's admin-only gate — see `docs/12`'s WebSocket section for why that asymmetry is deliberate, not an inconsistency. |
| `POST /Sessions/{id}/Command`, `/Message`, `/Playing`, `/Viewing`, `POST /Sessions/Logout` | ❌ | Catalog only — remote session *control*. The transport it would ride on (`GET /socket`, above) now exists, but the command-delivery routes themselves are still entirely absent — a separate, larger feature. |
| `POST /QuickConnect/Initiate`, `/Connect`, `/Authorize` | ❌ | Catalog only — only the capability-check route (`Enabled`) exists; the actual pairing flow can't complete even though the client believes QuickConnect is available. |
| `GET /Devices`, `/Devices/Info`, `/Devices/Options` | ❌ | Catalog only — device management (admin). |
| `GET/POST /Auth/Keys`, `/Auth/Keys/{key}` | ❌ | Catalog only — API key management (admin). |
| `GET /Auth/Providers`, `/Auth/PasswordResetProviders` | ❌ | Catalog only. |

## System/admin

| Route | Status | Note |
|---|---|---|
| `GET /System/Info/Public`, `GET /System/Info` | ✅ | Confirmed live. |
| `GET/POST/DELETE /Library/VirtualFolders` | ✅ | Registered, admin-gated. Upgraded back from ⚠️: `POST`'s paths-in-body bug is fixed — `handleAddVirtualFolder` now reads `LibraryOptions.PathInfos[].Path` from the request body (what the real dashboard actually sends), falling back to the `paths` query param only when the body carries none, confirmed live end to end against `tinyfin-staging` (a library added through the same body shape jellyfin-web sends now lands with its folder in `Locations`, not empty). `GET`'s response DTO (`virtualFolderInfo`) still doesn't model `PrimaryImageItemId`/`RefreshStatus`/`RefreshProgress` (deliberate gap, unchanged), but now echoes back whatever `LibraryOptions` blob was last saved via the new `.../LibraryOptions` route below (see there for the "opaque save-and-echo, not a modeled ~35-field object" scope decision) — closing most, not all, of the previously-flagged DTO gap. |
| `POST /Library/Refresh` | ✅ | Same registration status as above; this specific route (scan *every* library) is confirmed working, same as `POST /Items/{itemId}/Refresh` above (now also implemented, for the per-library case). |
| `GET /System/Endpoint` | ✅ | Implemented (`internal/api/system.go`). Shape and requires-auth confirmed against the reference server (staging, :8097): `{"IsLocal":bool,"IsInNetwork":bool}`, 401 with no token. `IsLocal`/`IsInNetwork` derived from the request's actual TCP peer address (loopback / RFC 1918 private / link-local) — confirmed `X-Forwarded-For` is ignored entirely by the real server, so no reverse-proxy trust config was added to match that. Confirmed live: `curl` against `tinyfin-staging` matches from outside the container (private Docker bridge address → `IsLocal:false, IsInNetwork:true`) and a Playwright session shows this called once at startup with no 404. |
| `GET /System/Info/Storage` | ✅ | Implemented (`internal/api/system_info_storage.go`, admin-only), found via the `/dashboard` (Painel) overview pass — not previously catalogued under any name. Real `statfs(2)`-backed free/used space per library folder (one stat per `storage.Library.Paths` entry, since a library can span multiple mounts) and per Tinyfin's own real directories: `ProgramDataFolder`/`CacheFolder`/`LogFolder`/`InternalMetadataFolder`/`ImageCacheFolder` all report the same real directory (`Config.DataDir` — Tinyfin has no config/cache/log/metadata split, unlike the reference server, so this is the honest single answer, not five fabricated paths), `TranscodingTempFolder` is `os.TempDir()` (the real transcode working directory), `WebFolder` is present only when a web bundle is actually configured. `StorageType` is always `"Fixed"` — a documented simplification, no dependency-free way to detect network mounts. Confirmed live against `tinyfin-staging`, including the Dashboard "Locais" widget rendering real GiB figures. See "Dashboard — Painel (overview)" below. |
| `GET /System/Configuration`, `/System/Configuration/{key}`, `/System/Configuration/Branding` | ❌ | Catalog only — admin dashboard settings pages. |
| `GET /System/ActivityLog/Entries` | ✅ | Implemented (`internal/api/activity_log.go`, admin-only), found via the `/dashboard` (Painel) overview pass. Deliberately narrow, real (not stubbed) activity log: only two event types are ever generated — `SessionStarted` (successful login) and `AuthenticationFailed` (rejected login, unknown username or wrong password, indistinguishable on the wire the same way the reference server's own response never tells them apart) — durably persisted (`internal/storage/activity.go`, sqlite migration `0005_activity_log.sql`), not scan-progress/item-added/user-management events, which the reference server's own activity log also carries but this project has no hook points for yet. `hasUserId=true`/`false` genuinely filters on a nullable `user_id` column (a failed login has none), `minDate`/`startIndex`/`limit` all real. See "Dashboard — Painel (overview)" below for the full capture and scope writeup. |
| `GET /System/Logs`, `/System/Logs/Log` | ❌ | Catalog only. |
| `POST /System/Restart`, `/System/Shutdown` | ❌ | Catalog only. |
| `GET /System/Ping` | ❌ | Catalog only. |
| `GET /ScheduledTasks` | ✅ | Implemented (`internal/api/scheduled_tasks.go`, admin-only), found via the `/dashboard` (Painel) overview pass. Reports exactly one entry — a library scan, the one real background operation this project has — rather than fabricating any of the reference server's ~18 built-in tasks (plugin updates, trickplay, subtitle/lyric downloads, ...) this project has no machinery for. `State` ("Running"/"Idle") and `LastExecutionResult` are live, backed by `Server.scanState` (a small tracker wrapping every `runScan` call); `Triggers` is always `[]` — no scheduling engine exists at all, a real, documented gap. This entry's `Id`/`Key` must be the literal `"RefreshLibrary"` — jellyfin-web's Dashboard home widget and Libraries page both locate it by filtering on `Key === 'RefreshLibrary'` (client-side JS, not the reference server's C#); shipping any other value silently broke the "Scan All Libraries" button (`isScanning` derived from a `.find()` that never matched, so the spinner never stopped) — found by the user reporting the button spinning forever, root-caused by reading jellyfin-web's own source. See "Dashboard — Painel (overview)" below. |
| `POST /ScheduledTasks/Running/{taskId}` | ✅ | Implemented (`internal/api/scheduled_tasks.go`'s `handleScheduledTaskStart`), the route the Dashboard's "Scan All Libraries" button actually calls once it resolves the task above by Key. Validates `taskId` against the one task this project has, then runs the same `refreshAllLibraries` `POST /Library/Refresh` uses. |
| `POST /ScheduledTasks/{taskId}`, `.../Triggers`; `DELETE .../Running/{taskId}` | ❌ | Catalog only — per-task admin actions (get single task, edit triggers, cancel a running one) on the task listed above. No scan cancellation exists, and no scheduling engine exists to act on for the rest. |
| `GET/POST /Backup`, `/Backup/Create`, `/Backup/Restore`, `/Backup/Manifest` | ❌ | Catalog only. |
| `GET /Plugins`, `/Plugins/{id}`, `.../Configuration`, `.../Manifest`; `GET /Packages`, `/Packages/{name}` | ❌ | Catalog only — deliberately out of scope per `docs/01`'s plugin-loading non-goal; listed for completeness, not expected to ever move to ✅. |
| `GET /Localization/Countries`, `/Cultures`, `/Options`, `/ParentalRatings` | ❌ | Catalog only — admin setup wizard / user-creation dropdowns. |
| `GET /GetUtcTime` | ❌ | Catalog only — client time-sync check. |
| `POST /ClientLog/Document` | ❌ | Catalog only — client crash/log upload endpoint. |

---

## Dashboard — Bibliotecas (admin library management)

A dedicated pass over `/dashboard/libraries` and every modal it opens — page load, "Adicionar
Biblioteca de Mídia", the per-library "⋮" menu ("Editar imagens", "Gerenciar biblioteca",
"Renomear", "Rastrear biblioteca", "Remover") — captured live via Playwright against both the
reference server (`localhost:8097`) and Tinyfin (`localhost:8096`), same method as the rest of
this doc (source 3). Unlike the browsing-focused capture this doc started from, this pass logged
into the real admin dashboard and drove every modal open, not just the routes a non-admin session
would ever see.

**The headline finding — resolved: adding a library silently dropped every folder path.**
jellyfin-web's "Adicionar Biblioteca de Mídia" → OK submits
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
against `tinyfin-staging`: a library added with the exact body shape jellyfin-web sends now shows
up with its real folder in `Locations`, and a subsequent scan walks it. This was the single most
impactful gap this pass found — every other issue below was a modal that didn't fully render or a
menu action that 404d, but this one silently produced a broken library that *looked* successful.

Page load (`GET /dashboard/libraries` itself) fires, beyond the already-catalogued `System/Info` and
`Library/VirtualFolders`:

| Route | Status | Note |
|---|---|---|
| `GET /web/ConfigurationPages?enableInMainMenu=true` | ✅ | Resolved by the later `/dashboard` (Painel) overview pass — see "Dashboard — Painel (overview)" below and `internal/api/system.go`'s `handleConfigurationPages`. |
| `GET /ScheduledTasks?isHidden=false` | ✅ | Resolved by the later `/dashboard` (Painel) overview pass — see its row in "System/admin" above and "Dashboard — Painel (overview)" below. |

Opening "Adicionar Biblioteca de Mídia" (and, identically, "Gerenciar biblioteca" on an existing
library — same modal, pre-filled) fires:

| Route | Status | Note |
|---|---|---|
| `GET /Localization/cultures`, `GET /Localization/countries` | ✅ | Implemented (`internal/api/localization.go`, `requireAuth`). Populate the "Idioma preferido de download" and "País/Região" dropdowns. Serve the real ISO 639-1/639-2 (192 entries) and ISO 3166 (139 entries) datasets — standard reference data, not the reference server's own IP — with field names confirmed live against `jellyfin-real-staging`. Confirmed live against `tinyfin-staging` that the "Configurações da Biblioteca" section of the modal now renders past "Pastas" instead of being gated off. |
| `GET /Libraries/AvailableOptions?IsNewLibrary=false` (fired once with no `LibraryContentType`, again once the content-type dropdown is set, e.g. `&LibraryContentType=movies`) | ✅ | Implemented (`internal/api/localization.go`, `requireAuth`). Not previously catalogued under any name — a genuinely new route this pass found, not in the SDK/apiclient bundle's URL-template scan because it's built from separate path segments. Response shape (`MetadataSavers`/`MetadataReaders`/`SubtitleFetchers`/`LyricFetchers`/`MediaSegmentProviders` + per-kind `TypeOptions[]`) confirmed live against the reference server. Scope decision: since Tinyfin has no metadata-provider *registry* (TMDB is hardcoded, per `internal/metadata`), the fetcher lists are honest, not a copy of the reference server's full plugin catalog — `MetadataFetchers`/`ImageFetchers` list only `TheMovieDb`, only for the kinds `internal/metadata.TMDBProvider` actually covers (Movie/Series/Season/Episode; every other kind gets empty lists), `MetadataReaders` lists `Nfo` only where `internal/scan/nfo.go` actually reads one, `MetadataSavers` is always empty (nothing writes a sidecar file back). `SupportedImageTypes`/`DefaultImageOptions` are copied faithfully for every kind (wire facts about the item model, not a capability claim). Confirmed live against `tinyfin-staging`. |

Clicking "Adicionar" under "Pastas" (the folder browser) fires:

| Route | Status | Note |
|---|---|---|
| `GET /Environment/DefaultDirectoryBrowser`, `GET /Environment/Drives`, `GET /Environment/DirectoryContents?includeDirectories=true&path=...`, `GET /Environment/ParentPath?path=...`, `POST /Environment/ValidatePath` | ✅ | Implemented (`internal/api/environment.go`, admin-only), operating on the real container filesystem. Response shapes confirmed live against the reference server: `DefaultDirectoryBrowser` → `{}` (no `Path` key, not a default `"/"`); `Drives` → one entry per root (`[{"Name":"/","Path":"/","Type":"Directory"}]` on Linux/container, unlike the reference server's Windows-drive-letter list — scope decision, single root only); `DirectoryContents` → `[{"Name","Path","Type"}]`, neither `includeDirectories` nor `includeFiles` defaults to "everything" (both unset returns `[]`, confirmed live); `ParentPath` → a bare JSON string, `null` at the filesystem root (confirmed live, not `"/"`); `ValidatePath` body `{"Path":...,"ValidateWriteable":...}` (note spelling, confirmed from the apiclient bundle) → 204 if the path exists (confirmed live to accept files, not just directories — the reference server's own certificate-path picker validates a file through this same route). Confirmed live end to end against `tinyfin-staging`: `Drives` and `DirectoryContents?path=/media` both return the real mounted folders. |

Saving "Gerenciar biblioteca" (edit an existing library's options) fires a route not in the SDK/
apiclient catalog scan at all:

| Route | Status | Note |
|---|---|---|
| `POST /Library/VirtualFolders/LibraryOptions` | ✅ | Implemented (`internal/api/library_admin.go`'s `handleSaveLibraryOptions`, admin-only), body `{"Id":"<libraryItemId>","LibraryOptions":{...full options object...}}` — addressed by `Id`, unlike `DELETE /Library/VirtualFolders`'s by-`Name` addressing, matching the reference server. This is the *only* way to change an existing library's options at all — `POST /Library/VirtualFolders` (the add route) only ever creates. Scope decision: the ~35-field `LibraryOptions` object is persisted as an opaque raw-JSON blob (`storage.Library.LibraryOptions`, new sqlite column/migration `0004_library_options.sql`) and echoed back verbatim on the next `GET /Library/VirtualFolders` — a save-and-echo round trip, not an interpretation of any of those fields (Tinyfin doesn't act on real-time-monitoring/trickplay/etc. settings today). 404 if the library `Id` doesn't exist, 400 on malformed JSON, 204 on success. Confirmed live end to end against `tinyfin-staging`: save then re-fetch returns the same `LibraryOptions` blob that was saved. |

The per-library "⋮" menu's other three actions:

| Route | Status | Note |
|---|---|---|
| "Rastrear biblioteca" (per-library scan) | ✅ | See `POST /Items/{itemId}/Refresh`'s updated row above — implemented, confirmed live against `tinyfin-staging` (queues the one library's scan). |
| "Renomear" | ✅ | Implemented (`internal/api/library_admin.go`'s `handleRenameVirtualFolder`, admin-only), `POST /Library/VirtualFolders/Name?name=<current>&newName=<new>&refreshLibrary=<bool>` — route shape confirmed by static analysis of Tinyfin's own embedded `@jellyfin/sdk` bundle (not live-tested against the reference server: its own confirmation dialog warns renaming discards all metadata, too destructive to trigger against the persistent fixture). Looks up the library by current `name`, renames it (ID/Paths/LibraryOptions preserved), queues a scan if `refreshLibrary=true`; 404 if `name` doesn't match a library, 400 if `newName` is empty. Confirmed live against `tinyfin-staging`: rename-then-list shows the new name, folders intact. |
| "Remover" | ✅ | Uses the already-implemented `DELETE /Library/VirtualFolders?name=<name>`; not re-confirmed live in this pass (would delete a real staging library) but the route and its by-`Name` addressing were already confirmed in the page-load capture above. |

Also present in the bundle but not surfaced by any modal this pass exercised (no per-path
add/remove UI exists in this jellyfin-web build's library-card menu — only whole-library add/
rename/remove): `POST /Library/VirtualFolders/Paths` (add one path to an existing library) and
`POST /Library/VirtualFolders/Paths/Update` (replace one path with another). Neither is registered
in `server.go`; `library_admin.go`'s own doc comment already lists "per-path add/remove/update on
an existing library" as a deliberate, documented gap — this pass just confirms the exact route
shapes from the bundle rather than changing that call.

---

## Dashboard — Painel (overview)

A dedicated pass over `/dashboard` itself (the admin overview page — "Painel" in the pt-BR locale
this build renders), same method as "Dashboard — Bibliotecas" above: live Playwright network
capture on page load against both the reference server (`:8097`) and `tinyfin-staging` (`:8096`).
Six routes 404d on Tinyfin that the page calls unconditionally on every load:

| Route | Status | Note |
|---|---|---|
| `GET /Items/Counts` | ✅ | See its row in "Library and item navigation" above. Drives the "Filmes"/"Séries"/"Episódios" counter widget. |
| `GET /System/Info/Storage` | ✅ | See its row in "System/admin" above. Drives the "Locais" storage-bars widget. |
| `GET /Sessions?activeWithinSeconds=960` | ✅ | See its row in "Users, sessions, devices" above. Drives the "Dispositivos" widget. Registered admin-only, not `requireAuth`: it lists every tracked session across every user (IP address, device name, client included) and the admin Dashboard is the only confirmed caller — exposing that to any authenticated non-admin user would be a real information-disclosure regression, not a scope simplification, so this diverges from this project's usual "authenticated-but-not-admin" pattern for listing routes deliberately. |
| `GET /System/ActivityLog/Entries` (called twice: `hasUserId=true` for the "Atividade" widget, `hasUserId=false` for the "Alertas" widget) | ✅ | See its row in "System/admin" above. |
| `GET /ScheduledTasks?isHidden=false` | ✅ | See its row in "System/admin" above. Already noted as a `/dashboard/libraries`-page gap too (that section's rows now point here). |
| `GET /web/ConfigurationPages?enableInMainMenu=true` | ✅ | Implemented (`internal/api/system.go`'s `handleConfigurationPages`, `requireAuth`) — always `[]`, matching the reference server's own response with no plugins installed (Tinyfin has no plugin system at all, per `docs/01`). Had to be special-cased in `cmd/tinyfin/webui.go`'s static-file-serving wrapper too: that wrapper treats every `/web/...` path as a jellyfin-web static asset by default, which would otherwise shadow this route entirely (a 404 from the file server, not from the API) whenever a web bundle is mounted. |

**A real, previously-hidden wire-compatibility bug found in the process, unrelated to any of the
six routes above**: a real jellyfin-web build sends this project's client-identification header
with its `Client` field percent-encoded — literally `Client="Jellyfin%20Web"` — and the reference
server decodes it before reporting it back anywhere. `internal/api/auth.go`'s `parseEmbyAuth` never
decoded any field at all; this went unnoticed until now because no earlier route ever echoed
`AuthContext.Client` back on the wire — `GET /Sessions` (above) is the first one that does, and it
surfaced literally as `"Client":"Jellyfin%20Web"` in the Dashboard's "Dispositivos" widget in this
pass's own testing. Fixed by percent-decoding every field (`url.PathUnescape`, not `QueryUnescape` —
a literal `+` in a base64-shaped `DeviceId` must stay a `+`, not become a space); confirmed live
against `tinyfin-staging` that the widget now reads "Jellyfin Web 12.0.0" like the reference server
does, and covered by a new unit test (`internal/api/auth_test.go`).

WebSocket `/socket` was the one confirmed-live 404 this page still produced on every load after
this pass — closed in a follow-up pass: a black-box capture plus a reference-source read (per
Rafael's explicit instruction) extracted the full wire protocol and push semantics into
`docs/12-clean-room-behavior-specs.md`'s "Dashboard overview — the `GET /socket` realtime channel"
section, including a dedicated review of `jellyfin/jellyfin`'s own public issue tracker for real,
recurring architecture bugs in its WebSocket implementation (concurrent-write crashes, connections
that never get cleaned up, a zombie-session/SyncPlay bug) to design around rather than reproduce.
Implemented in `internal/api/websocket.go` and confirmed live against `tinyfin-staging` with a real
jellyfin-web build — see this section's own `GET /socket` row above.

**A second, later bug on this same page**: the "Rastrear Todas as Bibliotecas" (Scan All Libraries)
button reported by Rafael as spinning forever, click or no click. Root cause was in `GET
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
   only when the body carries none; confirmed live end to end against `tinyfin-staging` with the
   exact body shape jellyfin-web sends. See "Dashboard — Bibliotecas (admin library management)"
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
