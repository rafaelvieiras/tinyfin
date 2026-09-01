# Roadmap

> Checklist companion to [`docs/10-roadmap.md`](docs/10-roadmap.md), which explains *why* the
> phases are ordered this way and what each one's success criteria are. This file only tracks
> *what's done*. When a phase's goal is met, its box gets checked here — the reasoning stays in
> `docs/10-roadmap.md` and doesn't get duplicated.

Legend: `[x]` done · `[ ]` not started · `[~]` in progress

---

## Phase 0 — Foundation documentation

- [x] Vision and scope written (`docs/01`)
- [x] Glossary written (`docs/02`)
- [x] Architecture (roles, process layout) written (`docs/03`)
- [x] API compatibility surface and verification approach written (`docs/04`)
- [x] Storage driver design written (`docs/05`)
- [x] Session/realtime-state design written (`docs/06`)
- [x] Transcoding pipeline design written (`docs/07`)
- [x] Library scanning and metadata provider boundary written (`docs/08`)
- [x] Memory and performance goals written (`docs/09`)
- [x] Foundational decisions D1–D8 closed (`docs/00-decision-log.md`)
- [x] Repository created and pushed (private, `github.com/rafaelvieiras/tinyfin`)
- [x] **O2** — initial device profile list written down (`docs/07`)
- [x] **O3** — storage driver Go interface signature written down (`docs/05`)

Phase 0's documentation goals are met — O2 and O3 are both resolved. See `docs/10-roadmap.md` for
what starting Phase 1 actually requires next.

## Phase 1 — Compatibility core, single-node, embedded storage only

- [x] Embedded SQLite storage driver implementing the interface from O3 (`internal/storage/sqlite`)
- [~] API/metadata role: users, devices, library browsing, and images landed
      (`internal/api`); sessions (beyond auth) and subtitles are not built yet.
- [~] Direct Play negotiation against the device-profile set from O2 (`internal/deviceprofile`,
      `internal/playback`) — confirmed working against the `jellyfin-web` device profile via a
      real client (below). The `"Jellyfin Android"`/`"Jellyfin iOS"` `Client` strings are still
      placeholders pending confirmation against real native-client traffic; a staging deployment
      for that check is up (see below).
- [x] Real-client verification (web): a real, unmodified `jellyfin-web` build (clean-room —
      AGENTS.md sanctions this as external client-behavior observation, not reference-server
      source) driven against Tinyfin found and fixed 13 wire-compatibility bugs (CORS, semver
      format, case-insensitive routing, missing `DisplayPreferences`/legacy `GET /Users/{userId}`/
      `GET /Users/{userId}/Items/{itemId}` routes, nil `UserDto.Policy`/`Configuration` crashes,
      missing `ImageTags` and `MediaType` fields, image routes needing unauthenticated `<img>`
      access, missing `ApiKey` query-param auth for `<video src>`) — confirmed end to end through
      a real Direct Play (206 Partial Content stream). All of it is now regression-guarded by an
      automated Playwright suite (`e2e/`, synthetic committed fixtures) so this doesn't require
      another manual click-through session per change.
- [x] Real-client verification (native Android, connect/auth/browse): the official
      `jellyfin-android` app, driven against the same staging deployment, found and fixed two
      more wire-compatibility bugs invisible to `jellyfin-web`/curl testing — both root-caused by
      reading `jellyfin-android`'s own source (`JellyfinWebViewClient.kt`) after server-side logs
      kept showing fully successful logins while the app kept reopening its native
      "can't connect" screen: (1) `Config.ServerID`, left to mint a fresh random value every
      process restart, broke jellyfin-web's local server-list cache (keyed by `ServerId`) across
      the repeated rebuilds a staging session naturally does — now persisted to a sidecar file
      next to the SQLite DB so it survives restarts; (2) every ID this project mints
      (`internal/storage/sqlite`'s `newID`, `internal/api`'s `randomID`) was plain hex with no
      hyphens — the app's post-login WebView credential check parses `User.Id` as a UUID and
      silently treats a non-UUID value as "no user" even with a valid token present, so every ID
      is now formatted as a canonical UUID. Confirmed end to end: the app reaches the logged-in
      library view over both LAN and Netbird.
- [x] Real-client verification (native Android, playback flow coverage pass): after two more
      individual native-only playback crashes surfaced one at a time (`DisplayPreferencesDto`'s
      `ScrollDirection`/`SortOrder` `MissingFieldException`, then a second failure past that
      fix), switched from fixing one bug at a time to reading the whole native playback call
      path — `jellyfin-android`'s `PlayerViewModel`/`MediaSourceResolver` and the pinned
      `jellyfin-sdk-kotlin` v1.8.12 DTOs/serializers directly — for full coverage in one pass.
      Found and fixed: (1) `MediaSourceInfo` and `MediaStream` (nested in both `PlaybackInfo`
      and `GetItem` responses) were each missing several `kotlinx.serialization`-required
      fields, blocking Direct Play negotiation for every item unconditionally — the actual
      root cause class, worse than either individual crash found by testing alone; (2) the
      `ScrollDirection`/`SortOrder` fix was itself incomplete — an empty string isn't a valid
      enum member either, just a different flavor of the same `SerializationException`, now
      defaulted to `"Vertical"`/`"Ascending"`; (3) `UserPolicy` was still missing ~20 required
      fields, silently failing the native player's own `GET /Users/Me` call (caught internally,
      not crashing, but degrading silently); (4) `Sessions/Playing`, `.../Progress`,
      `.../Stopped`, `POST /UserPlayedItems/{itemId}`, and `GET /MediaSegments/{itemId}` were
      unregistered (404), breaking resume position and watched-state (also caught, not
      crashing) — now implemented against the existing `PlaystateStore`, not just stubbed.
      Separately, found while re-checking IDs end to end for this pass: `BaseItemDto.id` is
      typed as Kotlin `UUID` with a deserializer that throws a plain `IllegalArgumentException`
      on a non-UUID string — NOT a `SerializationException`, so it isn't even caught by the
      `ApiClientException` handling every native call site wraps itself in. The manual staging
      dev-seed helper (`cmd/devseed`, not part of the shipped project) was minting
      human-readable slug ids (`item-a-arte-da-guerra--2000`) instead of UUIDs; switched it to
      deterministic UUIDs derived from the same slug (keeps re-seeding idempotent) and migrated
      the existing staging DB's item/library ids by hand. All of the above is `go build`/`vet`/
      `test`- and curl-verified end to end (`PlaybackInfo` now returns a fully populated
      `MediaSources[0]` for a real Direct-Play-eligible file); on-device native playback
      confirmation is the next step.
- [x] Real-client verification (native Android, on-device Direct Play confirmed): the root
      cause of a "source error" playback failure turned out to be host infrastructure, not a
      Tinyfin bug — the rclone Google Drive FUSE mount backing the movies library had gone into
      a restart-crash loop (stale empty directories blocking the mountpoint), so the container's
      read-only bind mount was empty and every stream 404'd. Fixed by clearing the mountpoint and
      restarting the container; native Direct Play playback then worked end to end on-device,
      closing the Goal below. Found and fixed one real bug along the way, via the same
      server-side access logging (`server.go`'s `withAccessLog`) added to diagnose it:
      `Configuration.GroupedFolders` (and the three other slice fields beside it) was a Go nil
      slice marshaling to JSON `null` against a Kotlin non-nullable `List<String>` — same failure
      class as the `ScrollDirection`/`SortOrder` empty-string bug above, different shape (a null
      slice, not an empty enum string) — fixed by explicitly initializing all four to `[]string{}`.
- [x] Post-playback coverage pass (native WebView + web dashboard): after confirmed playback,
      found ~20 endpoints 404ing (via the same access-log tool) and several `BaseItemDto` fields
      silently missing — read jellyfin-web/jellyfin-android/jellyfin-sdk-kotlin source for full
      coverage rather than chasing reports one at a time (same approach as the playback-flow pass
      above). Root cause of the most user-visible gap: `Sessions/Playing/Progress` and
      `UserPlayedItems` (already implemented) were writing resume position/watched status
      correctly into `PlaystateStore`, but nothing ever read it back onto an item response —
      `BaseItemDto.UserData` didn't exist on the wire at all, so "Continue Watching" never
      appeared no matter how much of a file had been watched. Fixed, along with the home-screen
      widgets/navigation gaps sharing the same root storage support (`UserItems/Resume`,
      `Items/Latest`, `Items/{itemId}/Ancestors`, a `GetItem` fallback for library-root IDs) and
      registered honest-empty responses (rather than 404) for endpoints several client call sites
      render unconditionally before the request resolves (`Items/{itemId}/Similar`,
      `Items/Filters`/`Filters2`, `Studios`) — this project has no similarity/genre-scanning
      metadata yet (08), so empty is the accurate answer, not a stand-in for one. Left deliberately
      unaddressed: `Shows/NextUp` (needs real series/episode test data to implement meaningfully,
      none seeded yet) and the dashboard-admin/SyncPlay/WebSocket 404s, which `docs/04` already
      scopes as lowest-priority and which every call site confirmed degrades gracefully.
- [x] **Goal:** a real Jellyfin client browses a real library and plays a Direct-Play-compatible
      file end to end. Met for a real web client (`jellyfin-web`, above) and, as of the two
      entries directly above, for the native Android client end to end on-device as well.

## Phase 2 — Transcoding pipeline

- [ ] Transcode worker role, `jellyfin-ffmpeg` integration
- [ ] Full Direct Play / Remux / Direct Stream / Transcode decision logic
- [ ] **Goal:** a client whose device profile forces a transcode gets a correct HLS stream.

## Phase 3 — Metadata provider interface

- [ ] Provider interface implementation
- [ ] Direct TMDB/TVDB provider (reference implementation)
- [ ] **Goal:** a freshly scanned library gets enriched metadata without Tinyfin implementing its
      own scraping.

## Phase 4 — Scale mode

- [ ] Scale-mode storage driver (write leader + streaming read replicas)
- [ ] Scale-mode session/realtime-state driver (Redis/Valkey pub-sub)
- [ ] Transcode worker role actually split onto a separate machine from the API/metadata role
- [ ] **Goal:** two or more API/metadata nodes serve reads correctly against one write leader, and
      a client's session survives being routed to a different node (or sticky routing is
      documented as the accepted alternative).

## Deliberately not scheduled

Live TV/DVR, a generic plugin-loading system, DLNA, offline mobile sync — see decision **D8** in
`docs/00-decision-log.md`. Not phased in above; revisit only if the target use case changes.
