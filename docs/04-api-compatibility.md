# 04 — API compatibility

## What "compatible" means here

Wire compatibility, not implementation compatibility. A client is satisfied if the routes it
calls, the headers it sends and expects, and the JSON shapes it parses behave the way a reference
Jellyfin server's would. Internal details a client cannot observe — database schema, admin-only
endpoints, plugin-loading APIs — are not part of the compatibility target and are free to differ
or not exist at all.

Compatibility is defined and verified against **observed client behavior and public API
documentation**, never against the `jellyfin/jellyfin` source tree — see the clean-room
requirement in `AGENTS.md` and decision **D1** in `00-decision-log.md`.

## Reference API surface

The reference server's REST API is generated from **60 controllers**. Grouped by function, the
categories Tinyfin's compatibility surface needs to cover, roughly in priority order for v1:

**Library and item navigation** (highest priority — nothing works without this)
Items, user library views, library structure, item lookup/refresh/update, user views, filters,
search, suggestions, instant mix.

**Specialized media types** (metadata shape per category, layered over the generic item model)
Movies, TV shows, artists, persons, genres, music genres, studios, years, collections, playlists,
trailers.

**Playback/streaming** (highest priority — this is the other half of "nothing works without this")
Videos, audio, universal audio, dynamic HLS, HLS segments, media info, media segments, playstate
(watched/resume/progress), video attachments, trickplay (seek thumbnails), SyncPlay.

**Images and branding**
Item images, remote images, branding.

**Subtitles and lyrics**
Subtitle delivery, lyrics.

**Users, sessions, devices**
Users, sessions, devices, QuickConnect, display preferences.

**System/admin** (lowest priority for v1 — needed for a usable server, not for client
compatibility per se)
System info, configuration, dashboard, activity log, scheduled tasks, plugins/packages
(surface only — see `01` on the plugin-loading non-goal), API keys, backup, startup wizard,
environment, client logs, time sync, localization.

**Explicitly not targeted** (see `01`): Live TV, Channels (DVR-related controllers).

## Authentication

Matches the reference format so that clients don't need any awareness they're talking to a
different server:

- Header `Authorization`, format:
  `MediaBrowser Client="...", Device="...", DeviceId="...", Version="...", Token="..."`
- Two token kinds:
  - **API key** — no user context, full administrative access, created by an admin, does not
    expire until revoked.
  - **User token** — obtained via `POST /Users/AuthenticateByName` (username/password), used on
    every subsequent request.
- Query parameter `ApiKey=<token>` for requests that cannot carry a header (media element
  sources, HLS segment URLs, the `GET /socket` upgrade).
- **Legacy channels are not honored** (decision D14/D14a): `X-Emby-Authorization`,
  `X-Emby-Token`, `X-MediaBrowser-Token`, the `api_key` query parameter and the `Emby` scheme
  are ignored, so the request is unauthenticated (401 on protected routes; login without a
  usable `Authorization` header is 400).
- QuickConnect (short-code pairing without typing a password) as a secondary flow for
  input-constrained clients (TVs).

## Playback negotiation contract

Covered in depth in `07`; the API-surface-relevant part is that the client sends a device profile
as part of a `PlaybackInfo`-equivalent request, and the server's response needs to match the
shape a real Jellyfin server would return closely enough that the client's own decision logic
(which of Direct Play/Remux/Direct Stream/Transcode it thinks is happening) stays in sync with
what the server actually does. A mismatch here is the single most likely source of a client
silently misbehaving (wrong seek behavior, wrong bitrate assumptions) — see `07` for how the
decision itself is made; this section is only about matching the shape of the negotiation.

## Verification approach

- Primary: real client traffic captured against a reference Jellyfin server, replayed against
  Tinyfin, diffed on response shape (not byte-for-byte — internal IDs and timestamps will differ
  by construction).
- Secondary: the reference server's published OpenAPI/Swagger schema, used to catch shape drift
  Tinyfin's own tests wouldn't otherwise notice.
- Out of scope for automated verification: administrative/plugin-loading endpoints no client
  depends on for playback — those get implemented to the extent `01`'s scope calls for, not
  verified against the reference server.

## Versioning against upstream drift

Open item **O1** in `00-decision-log.md` — the reference API surface is not frozen upstream, and
Tinyfin needs an explicit policy for which upstream version/commit is the compatibility baseline
at any given time, and how that baseline gets revisited. Not yet decided.
