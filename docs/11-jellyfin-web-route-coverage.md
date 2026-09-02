# 11 — jellyfin-web route coverage

## What this is

A ground-truth inventory of every HTTP route jellyfin-web (the reference web client) actually
calls or can call, cross-referenced against what Tinyfin implements today (`internal/api/routing.go`,
`internal/api/server.go`). This is a planning artifact, not a spec — it exists so the project can
decide what to build next with real usage data instead of guessing from the OpenAPI surface alone.
Nothing here was implemented as part of producing this document; see `docs/04-api-compatibility.md`
for what "compatible" means and how it's normally verified.

## Method

Three sources, combined:

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
| Route entries catalogued below (excluding Live TV/Channels, out of scope per `docs/01`) — some entries group several closely-related paths on one row, so this counts table rows, not raw path-template strings | 85 |
| ✅ Implemented | 33 |
| ⚠️ Parcial (stub/honest-empty) | 4 |
| ❌ Not implemented | 48 |

37 of 85 (44%) return real or honestly-empty data today. The gaps cluster heavily in a few places
that matter for ordinary browsing, not just admin/edge-case features — see "Biggest real gaps" at the
bottom.

---

## Library and item navigation

| Route | Status | Note |
|---|---|---|
| `GET /UserViews` | ✅ | Home screen library list. Re-audited byte-for-byte against the reference server (staging, :8097): found and fixed two real divergences — `Type` was `"UserView"`, reference sends `"CollectionFolder"` for an ordinary library; `UserData` was omitted entirely, reference populates it same as any other item (`dto.go`'s `libraryBaseItemDto`). |
| `GET /Items` | ✅ | Generic listing/search, query-param filtered. Confirmed live (movie library grid, search). Also serves the Favoritos tab's per-user form, `GET /Users/{userId}/Items?Filters=IsFavorite&...` — re-audited byte-for-byte against the reference server; found and fixed two real divergences: (1) `SortBy=SeriesSortName,SortName` (what the Favoritos tab actually sends for Episode/Season rows) silently dropped the `SeriesSortName` key entirely, so favorited episodes from different series interleaved alphabetically by episode title instead of grouping by series — now a real sort key (`storage.SortBySeriesName`, a self-join to the owning series' `sort_name`). (2) `TotalRecordCount` ignored `EnableTotalRecordCount=false` (sent on every Favoritos-tab call) and always returned the real total instead of just how many items that response carries, confirmed against the reference server on both a first and second page. See the note below and "Users, sessions, devices" for the toggle routes it depends on. |
| `GET /Items/Latest` | ✅ | Home "recently added" rows. Confirmed live, exact query-param shape (`userId`+`parentId`) matches what Tinyfin's embedded bundle sends. A TV-shows library request now groups by series (one card per series — a `Season`, the `Series` itself, or a single `Episode`, per `docs/12`'s "Items/Latest — series grouping"), resolved via the clean-room two-phase process (spec extracted to `docs/12`, implemented from that spec plus live black-box comparison against the reference server, `internal/api/latest_grouping.go`) rather than left as the flat, ungrouped list of every recently-scanned `Episode`/`Season` row this used to return. `groupItems=false` (confirmed live, any capitalization) still returns that original flat list; the movies-library case stays flat unconditionally, matching the reference server's own "no grouping" behavior there. `docs/12`'s "Implementation findings" section records two reference-server behaviors intentionally not reproduced (a played-item ranking exclusion, and an unresolved card-selection quirk on month-plus-old bursts) — out of scope for this gap, logged for later. |
| `GET /Items/{itemId}` | ✅ | Item detail. Confirmed live (`?userId=` query form). |
| `GET /Items/{itemId}/Ancestors` | ✅ | Breadcrumb trail. Confirmed live. |
| `GET /Users/{userId}/Items/{itemId}` | ✅ | Legacy per-item alias — still hit on *every* detail page load before the plain form, per `server.go`'s own comment (found via real client testing, not spec). Confirmed live. |
| `GET /UserItems/Resume` | ✅ | Continue-watching row. Confirmed live, exact match against Tinyfin's own bundle. Re-audit found and fixed a related bug one level down, in the mark-watched routes: a stale in-progress position could resurrect an already-unmarked item here — see the `PlayedItems`/`UserPlayedItems` notes below. |
| `GET /Shows/{seriesId}/Seasons` | ✅ | Confirmed live, real season data returned. |
| `GET /Shows/{seriesId}/Episodes` | ✅ | Confirmed live, real episode data returned. |
| `GET /Items/Filters` | ⚠️ | Registered, always returns empty facets (`library.go:502`) — "no such metadata yet, honest empty over 404" per its own doc comment. Confirmed live (200, empty). |
| `GET /Items/Filters2` | ⚠️ | Same stub as above (`library.go:506`). Confirmed live. |
| `GET /Items/{itemId}/Similar` | ⚠️ | Always returns empty list (`library.go:483`). Confirmed live. |
| `GET /Shows/NextUp` | ✅ | Implemented (`internal/api/nextup.go`). Rule reverse-engineered by black-box testing against the reference server (staging, :8097): per series, find the last episode (by season/episode order, not watch date) with `PlayCount > 0`; if it's not fully played, that same episode is the candidate (in-progress/"resume", included only when `enableResumable` isn't `false`); if fully played, the candidate is the next episode after it in order (crossing season boundaries correctly), or none if the series is finished (`enableRewatching` restart-at-episode-1 is a documented decision, not something the real server reproduced under test — see the handler's doc comment). Confirmed: a series with zero activity never appears (not "episode 1" as a naive reading of the endpoint name might suggest); marking an out-of-order episode played jumps straight to the one after it, not the first unwatched one; `nextUpDateCutoff` is a minimum-date bound; `EnableTotalRecordCount=false` reports `0`, diverging from `GET /Items`' own rule for that same param (`resolveTotalRecordCount`) — deliberately not reused unchanged. Also fixed a related pre-existing gap this depends on: `PlaystateStore.LastPlayedDate` was never actually written by any handler (`userdata.go`, `sessions_playback.go`) despite the storage field existing — now stamped/cleared to match confirmed reference-server behavior. Confirmed live end to end: `curl` against `tinyfin-staging` matches the reference server for every case above, and a Playwright session shows the home screen's "A seguir" row rendering a real next episode after marking a prior one watched, with no console/network errors. |
| `GET /Items/{itemId}/Collections` | ❌ | Confirmed 404 live, called unconditionally on **every** item detail page (movie, series, season) to check "part of a collection" banner. |
| `GET /Items/{itemId}/ThemeMedia` (and `/ThemeSongs`, `/ThemeVideos`) | ❌ | Confirmed 404 live, called on every single detail page load. Purely cosmetic (background theme music) but extremely high call frequency. |
| `GET /Users/{userId}/Items/{itemId}/Intros` | ❌ | Confirmed 404 live (legacy per-user form) — pre-roll trailers/intros before playback starts. Non-blocking (player just skips), but a real, frequently-hit gap. |
| `GET /Items/{itemId}/LocalTrailers` | ❌ | From SDK/apiclient catalog. Not exercised live (test library has no local trailer files) but a real registered client call. |
| `GET /Items/{itemId}/SpecialFeatures` | ❌ | Catalog only, same caveat as above. |
| `GET /Items/Root` | ❌ | Catalog only — media-folder root discovery. |
| `GET /Items/Counts` | ❌ | Catalog only — aggregate counts (e.g. dashboard widgets). |
| `POST /Items/{itemId}/Refresh` | ❌ | Catalog only — admin-triggered metadata refresh for a single item. |
| `GET /Playlists`, `GET /Playlists/{id}`, `GET/POST /Playlists/{id}/Items`, `.../Users` | ❌ | No playlist support at all — catalog only, not exercised live (no playlists in test library). |
| `GET /Genres`, `GET /Genres/{genreName}` | ❌ | Catalog only. See "Specialized media types" below — genre browsing has no route at all. |
| `GET /Years` | ❌ | From apiclient's concatenated-suffix scan (year filter). No route registered. |

## Specialized media types

| Route | Status | Note |
|---|---|---|
| `GET /Shows/{seriesId}/Seasons`, `/Episodes` | ✅ | Counted above under library nav — this *is* the TV-show-specific slice of that category and it's solid. |
| `GET /Studios` | ⚠️ | Always returns empty list (`library.go:514`) — "no Studios field on storage.Item at all today." Confirmed live (used by both the movie-library filter panel and search). |
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
| `GET /Items/{itemId}/RemoteImages`, `/RemoteImages/Providers`, `/RemoteImages/Download` | ❌ | Catalog only — admin metadata-editing "search for a better poster" UI. |

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
| WebSocket `/socket` | ❌ | Confirmed live — repeated WebSocket handshake failures (404) on every page. This is the realtime session channel `docs/06-session-and-realtime-state.md` scopes as its own interface; it isn't wired into the HTTP server at all yet. Consequence: no live "now playing on other devices," no remote control/cast-to-device, no server-push of any kind. |
| `GET /Sessions`, `POST /Sessions/{id}/Command`, `/Message`, `/Playing`, `/Viewing`, `POST /Sessions/Logout` | ❌ | Catalog only — the REST half of remote session control. Mostly moot without the WebSocket channel above, but entirely absent either way. |
| `POST /QuickConnect/Initiate`, `/Connect`, `/Authorize` | ❌ | Catalog only — only the capability-check route (`Enabled`) exists; the actual pairing flow can't complete even though the client believes QuickConnect is available. |
| `GET /Devices`, `/Devices/Info`, `/Devices/Options` | ❌ | Catalog only — device management (admin). |
| `GET/POST /Auth/Keys`, `/Auth/Keys/{key}` | ❌ | Catalog only — API key management (admin). |
| `GET /Auth/Providers`, `/Auth/PasswordResetProviders` | ❌ | Catalog only. |

## System/admin

| Route | Status | Note |
|---|---|---|
| `GET /System/Info/Public`, `GET /System/Info` | ✅ | Confirmed live. |
| `GET/POST/DELETE /Library/VirtualFolders` | ✅ | Registered, admin-gated. Not exercised live in this pass (would require the admin dashboard flow) but present in `server.go`. |
| `POST /Library/Refresh` | ✅ | Same as above. |
| `GET /System/Endpoint` | ✅ | Implemented (`internal/api/system.go`). Shape and requires-auth confirmed against the reference server (staging, :8097): `{"IsLocal":bool,"IsInNetwork":bool}`, 401 with no token. `IsLocal`/`IsInNetwork` derived from the request's actual TCP peer address (loopback / RFC 1918 private / link-local) — confirmed `X-Forwarded-For` is ignored entirely by the real server, so no reverse-proxy trust config was added to match that. Confirmed live: `curl` against `tinyfin-staging` matches from outside the container (private Docker bridge address → `IsLocal:false, IsInNetwork:true`) and a Playwright session shows this called once at startup with no 404. |
| `GET /System/Configuration`, `/System/Configuration/{key}`, `/System/Configuration/Branding` | ❌ | Catalog only — admin dashboard settings pages. |
| `GET /System/ActivityLog/Entries` | ❌ | Catalog only. |
| `GET /System/Logs`, `/System/Logs/Log` | ❌ | Catalog only. |
| `POST /System/Restart`, `/System/Shutdown` | ❌ | Catalog only. |
| `GET /System/Ping` | ❌ | Catalog only. |
| `GET/POST /ScheduledTasks`, `/ScheduledTasks/{taskId}`, `.../Triggers`, `.../Running/{taskId}` | ❌ | Catalog only — admin scheduled-task dashboard. |
| `GET/POST /Backup`, `/Backup/Create`, `/Backup/Restore`, `/Backup/Manifest` | ❌ | Catalog only. |
| `GET /Plugins`, `/Plugins/{id}`, `.../Configuration`, `.../Manifest`; `GET /Packages`, `/Packages/{name}` | ❌ | Catalog only — deliberately out of scope per `docs/01`'s plugin-loading non-goal; listed for completeness, not expected to ever move to ✅. |
| `GET /Localization/Countries`, `/Cultures`, `/Options`, `/ParentalRatings` | ❌ | Catalog only — admin setup wizard / user-creation dropdowns. |
| `GET /GetUtcTime` | ❌ | Catalog only — client time-sync check. |
| `POST /ClientLog/Document` | ❌ | Catalog only — client crash/log upload endpoint. |

---

## Explicitly out of scope (not counted above)

Per `docs/01-vision-and-scope.md`'s non-goal and `docs/04`'s own framing: Live TV and Channels
(DVR-related controllers) — `LiveTv/*` (~20 routes: Channels, Programs, Recordings, Timers,
TunerHosts, ...) and `Channels/*` (~5 routes). These showed up in the SDK catalog and, for
`LiveTv/Programs*`, in live capture (404, expected) — listed here only so the "known routes" count
above isn't accused of silently missing them; they were seen and deliberately excluded, not missed.

---

## Biggest real gaps for ordinary browsing

Ranked by how much they'd actually bite a normal user clicking around, not by API-surface size:

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
5. **`/Items/{itemId}/Collections` and `/Items/{itemId}/ThemeMedia` both 404 on every single item detail
   page.** Neither breaks anything visibly (collections banner and background theme song just don't
   appear), but they're the highest-frequency 404s in the whole capture — worth fixing cheaply (both are
   plausible honest-empty stubs, same pattern already used for `/Studios` and `/Items/Filters`) just to
   quiet the noise and remove two guaranteed failed requests from every page view.
