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
| ✅ Implemented | 30 |
| ⚠️ Parcial (stub/honest-empty) | 4 |
| ❌ Not implemented | 51 |

34 of 85 (40%) return real or honestly-empty data today. The gaps cluster heavily in a few places
that matter for ordinary browsing, not just admin/edge-case features — see "Biggest real gaps" at the
bottom.

---

## Library and item navigation

| Route | Status | Note |
|---|---|---|
| `GET /UserViews` | ✅ | Home screen library list. Confirmed live. |
| `GET /Items` | ✅ | Generic listing/search, query-param filtered. Confirmed live (movie library grid, search). |
| `GET /Items/Latest` | ✅ | Home "recently added" rows. Confirmed live, exact query-param shape (`userId`+`parentId`) matches what Tinyfin's embedded bundle sends. |
| `GET /Items/{itemId}` | ✅ | Item detail. Confirmed live (`?userId=` query form). |
| `GET /Items/{itemId}/Ancestors` | ✅ | Breadcrumb trail. Confirmed live. |
| `GET /Users/{userId}/Items/{itemId}` | ✅ | Legacy per-item alias — still hit on *every* detail page load before the plain form, per `server.go`'s own comment (found via real client testing, not spec). Confirmed live. |
| `GET /UserItems/Resume` | ✅ | Continue-watching row. Confirmed live, exact match against Tinyfin's own bundle. |
| `GET /Shows/{seriesId}/Seasons` | ✅ | Confirmed live, real season data returned. |
| `GET /Shows/{seriesId}/Episodes` | ✅ | Confirmed live, real episode data returned. |
| `GET /Items/Filters` | ⚠️ | Registered, always returns empty facets (`library.go:502`) — "no such metadata yet, honest empty over 404" per its own doc comment. Confirmed live (200, empty). |
| `GET /Items/Filters2` | ⚠️ | Same stub as above (`library.go:506`). Confirmed live. |
| `GET /Items/{itemId}/Similar` | ⚠️ | Always returns empty list (`library.go:483`). Confirmed live. |
| `GET /Shows/NextUp` | ❌ | Confirmed 404 live — called on the home screen's continue-watching-shows row *and* on every series detail page. High-traffic route; its absence silently drops "next episode to watch" everywhere it would appear. |
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
| `GET /Playback/BitrateTest` | ❌ | Confirmed 404 live — bandwidth probe fired before essentially every playback session, used to pick an initial bitrate/quality guess. Non-fatal (client falls back) but called constantly. |
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
| `POST /Users/{userId}/FavoriteItems/{itemId}` (+ `DELETE`) | ❌ | **Confirmed 404 live** — clicking "Adicionar aos favoritos" on any item detail page fails silently; the button UI updates optimistically but the server never persists it. |
| `POST /Users/{userId}/PlayedItems/{itemId}` (+ `DELETE`) | ❌ | **Confirmed 404 live** — same story for "Marcar como reproduzido" (mark watched). Tinyfin *does* register `POST /UserPlayedItems/{itemId}` (the newer/SDK-shaped path), but the client build actually embedded in Tinyfin's own image never calls that path for this button — it calls the legacy `/Users/{userId}/PlayedItems/{itemId}` form instead, which isn't registered. Net effect: this Tinyfin-registered route is presently unreachable by its own bundled web client. |
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
| `GET /System/Endpoint` | ❌ | Confirmed 404 live — called at startup on every session; used by the client to detect LAN vs. remote address and affects initial speed/quality assumptions. |
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

1. **Favorites and "mark watched" are silently broken** (`POST /Users/{userId}/FavoriteItems/{itemId}`,
   `POST /Users/{userId}/PlayedItems/{itemId}`, both 404 confirmed live). The buttons exist, respond to
   clicks, and the UI even updates optimistically — there is no visible error. This is the single worst
   gap found: it looks like it works and doesn't.
2. **`/Shows/NextUp` (404)** breaks "what to watch next" on every TV series — both the home screen's
   row and the series detail page call it. For a project whose primary media type in the test library
   is TV shows, this is a core navigation feature, not an edge case.
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
