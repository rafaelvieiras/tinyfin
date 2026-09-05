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
      unaddressed at this point: `Shows/NextUp` (needed real series/episode test data to implement
      meaningfully, none seeded yet — implemented in a later pass, see `internal/api/nextup.go` and
      its row in `docs/11`) and the dashboard-admin/SyncPlay/WebSocket 404s, which `docs/04` already
      scopes as lowest-priority and which every call site confirmed degrades gracefully.
- [x] **Goal:** a real Jellyfin client browses a real library and plays a Direct-Play-compatible
      file end to end. Met for a real web client (`jellyfin-web`, above) and, as of the two
      entries directly above, for the native Android client end to end on-device as well.

## Phase 2 — Transcoding pipeline

- [x] Transcode worker role, `jellyfin-ffmpeg` integration (`internal/transcode`): wraps
      `jellyfin-ffmpeg` as an external subprocess per docs/07's division of responsibility —
      builds the command line, runs it under a cancellable context, writes HLS output (playlist +
      MPEG-TS segments) into a per-job directory, and tears it down on `Stop` or context
      cancellation. Bundled into the Docker image via a dedicated build stage that downloads the
      official portable release tarball (`docs/00-decision-log.md`'s **D10**) — the final image's
      base had to move from `distroless/static-debian12` to `distroless/base-debian12` since the
      portable binaries are dynamically linked against glibc, confirmed by actually running
      `ffmpeg -version` inside the built image. `cmd/tinyfin` locates the bundled binary by default
      (`-ffmpeg-path`/`TINYFIN_FFMPEG_PATH`, falling back to `ffmpeg` on `$PATH` for non-Docker/dev
      runs).
- [x] Full Direct Play / Remux / Direct Stream / Transcode decision logic
      (`internal/playback.Decide`, replacing Phase 1's Direct-Play-only `DecideDirectPlay`):
      picks the cheapest mode a profile's `DirectPlayProfiles` satisfies — exact match is Direct
      Play, codecs-OK-but-wrong-container is Remux, video-OK-but-audio-unsupported is Direct
      Stream — and falls back to the profile's first `TranscodingProfiles` entry (Transcode) when
      none of those apply, or reports `NoCompatibleStream` if the profile offers no transcoding
      fallback either. Per-condition narrowing (`CodecProfile`/`ContainerProfile`'s
      `ProfileCondition` rules — bitrate/resolution/level limits) is modeled on the wire
      (`internal/deviceprofile`) but deliberately not evaluated yet — a documented scoping choice,
      not an oversight. All three built-in profiles (`webProfile`/`mobileProfile`/`defaultProfile`)
      now carry an h264/aac/hls/ts `TranscodingProfiles` fallback so every client reaches Transcode
      rather than a dead end.
- [x] HLS serving (`internal/api/hls.go`): `GET /Videos/{itemId}/master.m3u8` starts (or reuses) a
      transcode job and returns a single-rendition master playlist; `GET .../main.m3u8` serves the
      growing media playlist; segments are served through the existing `/Videos/{itemId}/{file}`
      route (`playback.go`), reusing Direct Play's route rather than inventing a new one. Jobs are
      keyed by item ID (one active transcode per item, not per session — a scoped MVP
      simplification) and torn down explicitly by `Sessions/Playing/Stopped`
      (`sessions_playback.go`), since a job's context is deliberately `context.Background()` and
      outlives any single HTTP request. Found and fixed one real bug via live testing against an
      actual movie file (not just a short synthetic clip, which hid it): `-hls_playlist_type vod`
      makes `jellyfin-ffmpeg` hold the whole playlist in memory and write `main.m3u8` to disk only
      once, at the very end of the entire encode — useless for a feature-length file when the API
      layer is polling for that file mid-encode. Switched to `-hls_playlist_type event`, which
      writes the playlist progressively after every segment while still keeping the full segment
      list (unlike the default "live" type's sliding window). Also found and fixed while wiring
      the two packages together (before any live test): `internal/transcode`'s first draft joined
      `-hls_segment_filename`/the playlist path against the job's absolute directory, which would
      have embedded local filesystem paths as segment URIs in the manifest — unusable by any real
      HTTP-based HLS client. Fixed by running `jellyfin-ffmpeg` with its working directory set to
      the job dir and plain relative output filenames instead.
      Also found and fixed via a real end-to-end browser test (jellyfin-web in an actual browser,
      not curl): `TranscodingUrl` carried no auth token, so a browser `<video>`/hls.js — which,
      unlike Direct Play's `ApiClient`-built URLs or the native player, resolves this URL
      server-side and can't attach a custom header — 401'd on the very first `master.m3u8` fetch.
      Fixed by embedding the token as an `api_key` query param in `hlsMasterURL`
      (`playback.go`), which `handleHLSMaster` already forwarded into its `main.m3u8` link; segment
      lines in `main.m3u8` still needed their own fix, since `jellyfin-ffmpeg` writes them as bare
      relative filenames with no query string at all — `handleHLSPlaylist` now rewrites each segment
      line with the request's own query before serving the playlist, so the token propagates all
      the way to every `.ts` fetch.
- [x] **Goal:** a client whose device profile forces a transcode gets a correct HLS stream. Verified
      twice: once via curl against the real staging deployment with a real ~2.5GB movie file
      (`PlaybackInfo` → `master.m3u8` → `main.m3u8` → segment, `Sessions/Playing/Stopped` tearing the
      job down), and again through an actual browser driving the bundled jellyfin-web end to end —
      logged in, pressed Play on a real library item, watched real transcoded frames render and
      advance over multiple HLS segment fetches, then confirmed `Sessions/Playing/Stopped` fired on
      leaving the player. The browser pass is what caught the `api_key` bug above; the curl pass
      alone would not have, since curl requests carried the token as a header.
      Verified end to end against the actual staging deployment (real bundled `jellyfin-ffmpeg`,
      real ~2.5GB movie file over the rclone-mounted library, real HTTP requests through the
      published port) — `PlaybackInfo` correctly resolved Transcode mode for a real mkv/hevc/aac
      file against the conservative default profile, `master.m3u8`/`main.m3u8`/a segment all
      served correctly (segment content-type `video/mp2t`, real transcoded video bytes), and
      `Sessions/Playing/Stopped` tore the job down. Also covered by an automated integration test
      (`internal/api/hls_test.go`) that exercises the same path against a real (synthetic) ffmpeg
      input, skipped gracefully when no ffmpeg binary is available.

## Phase 3 — Metadata provider interface

- [x] Provider interface implementation (`internal/metadata`): a fixed, small `Provider` interface
      (`Lookup(ctx, Query) (Result, error)`) matching D3/`08`'s "not a general-purpose plugin
      system" framing — deliberately shaped to survive a future move from an in-process Go call to
      a subprocess-isolated plugin (the `go-plugin` gRPC pattern) without a redesign, per **D11**
      (`docs/00-decision-log.md`): every method takes/returns only plain exported-field structs,
      `context.Context` carries only its deadline, errors are a sentinel or a structured
      `*ProviderError`, and the package never imports `internal/storage`/`internal/api`. Backed by
      `NoopProvider` (always `ErrNotFound`, the zero-config default) and `TMDBProvider` (the direct
      reference implementation).
- [x] Direct TMDB provider (`internal/metadata/tmdb.go`): queries TMDB's public v3 REST API
      (`/search/movie`, `/movie/{id}`, `/search/tv`, `/tv/{id}`, `/tv/{id}/season/{s}/episode/{e}`)
      directly over HTTP — ordinary third-party API integration, unrelated to the Jellyfin
      clean-room boundary. Configured via `TINYFIN_TMDB_API_KEY`/`-tmdb-api-key`; empty disables it
      (falls back to `NoopProvider`). Tested against a local `httptest.Server`, no real API key
      required for the test suite. TVDB is not implemented (TMDB alone satisfies D3's "a direct
      provider so Tinyfin is useful standalone" goal; add TVDB only if a real gap motivates it).
- [x] Library scanner (`internal/scan`, docs/08's "minimum needed to serve a browsable catalog" —
      previously only covered ad hoc by the throwaway, uncommitted `cmd/devseed`): walks a
      library's configured paths, identifies each item via the Kodi/Jellyfin filename convention
      (`nameparse.go` — "Title (Year)" folders, "Season NN" folders, "SxxEyy"/"NxNN" episode
      markers) *before* ever calling a Provider, extracts technical media info via `ffprobe`, and
      only then overlays a configured Provider's enrichment on top. This ordering is the direct
      answer to "items should already be enriched based on the Kodi/Jellyfin filename/metadata
      pattern" — every item is fully cataloged and browsable from filename-derived data alone, with
      or without a TMDB key configured (`TestScanLibraryNoProviderStillCatalogs`). `ffprobe`
      subprocess and Provider HTTP calls run through a bounded worker pool (default concurrency 4,
      docs/08's "Scan-time resource discipline"), not unbounded per-file spawning. Re-scanning is
      idempotent: item/image IDs are deterministic hashes of their file path, so a repeat scan
      upserts existing rows instead of duplicating them.
- [x] Library management API (`internal/api/library_admin.go`): `GET/POST/DELETE
      /Library/VirtualFolders` and `POST /Library/Refresh`, matching a real dashboard's "Add Media
      Library"/"Scan All Libraries" actions — route shapes and query params (`name`,
      `collectionType`, `paths`, `refreshLibrary`) confirmed against the published `@jellyfin/sdk`
      client's generated modules (docs/04's sanctioned secondary-verification source), after an
      initial, corrected misstep: a first lookup fetched `LibraryStructureController.cs` straight
      from the `jellyfin/jellyfin` reference-server repository, which is exactly what AGENTS.md's
      clean-room requirement prohibits — caught before any code was written from it, discarded, and
      redone against the sanctioned SDK client instead. Admin-only (new `requireAdmin` middleware,
      `auth.go` — the first route group in this project needing more than "any authenticated
      user"). A scan triggered by these endpoints runs in the background (`Server.runScan`), the
      same "outlives the triggering request" pattern Phase 2's transcode jobs already established.
- [x] Found and fixed a real concurrency bug in `internal/storage/sqlite`, exposed by
      `internal/scan` being this project's first caller to issue writes from more than one
      goroutine at once: two concurrent `UpsertItem` calls for a parent item and one of its
      children could each half-complete `recomputeAncestors`' DELETE+INSERT sequence for the same
      child row, tripping `item_ancestors`' UNIQUE constraint — and, separately, plain concurrent
      single-statement writes could return `SQLITE_BUSY "database is locked"` even within the
      configured 5s `busy_timeout`, meaning that pragma alone wasn't a reliable arbiter of
      cross-connection write contention under `modernc.org/sqlite`. Fixed by adding a `sync.Mutex`
      (`Store.writeMu`) that every top-level write (`UpsertItem`'s multi-statement transaction via
      `withTx`, `Store.Tx`, and every other single-statement write via a new `Store.exec` helper)
      serializes on — SQLite only ever supports one writer at a time regardless, so this removes
      the ambiguity structurally rather than tuning retry timeouts. Concurrent *reads* are
      untouched (WAL mode's actual concurrency benefit, why the connection pool itself stays
      uncapped). Regression-guarded by `TestConcurrentUpsertItem`
      (`internal/storage/sqlite/store_test.go`), which reliably reproduced the bug before the fix.
- [x] Dashboard library-management route mapping/diff and gap closure: a dedicated
      Playwright-driven pass over `/dashboard/libraries` and every modal it opens (`docs/11`'s
      "Dashboard — Bibliotecas" section), captured live against both the reference server and
      Tinyfin, found the highest-impact gap the whole `docs/11` route-coverage doc has surfaced so
      far — jellyfin-web's real "Adicionar Biblioteca de Mídia" submission sends folder paths in
      the `POST /Library/VirtualFolders` request body (`LibraryOptions.PathInfos`), not the `paths`
      query param `handleAddVirtualFolder` read, so a library added through the real admin
      dashboard silently ended up with zero folders (`204 No Content`, indistinguishable from
      success). Fixed, along with everything else that same pass found 404ing or silently
      no-op'ing: `POST /Library/VirtualFolders/LibraryOptions` (save an existing library's
      options — persisted as an opaque blob and echoed back, not modeled field-by-field, since
      Tinyfin doesn't act on most of them) and `POST /Library/VirtualFolders/Name` (rename);
      `POST /Items/{itemId}/Refresh` (the per-library "Rastrear biblioteca" scan action, distinct
      from the already-working whole-server `POST /Library/Refresh`); the `Environment/*` folder
      browser (`DefaultDirectoryBrowser`/`Drives`/`DirectoryContents`/`ParentPath`/`ValidatePath`);
      `Localization/cultures`/`countries` and `Libraries/AvailableOptions` (the Add/Manage modal's
      "Configurações da Biblioteca" section is client-side gated on all three succeeding —
      `AvailableOptions` honestly lists only TMDB as a fetcher, not the reference server's full
      plugin catalog Tinyfin doesn't have); and admin image management,
      `GET /Items/{itemId}/Images` (list) plus `POST`/`DELETE .../Images/{imageType}[/{imageIndex}]`
      (upload/delete). All of it confirmed live end to end against a rebuilt `tinyfin-staging`, not
      just unit-tested.
- [x] Dashboard overview (`/dashboard`, "Painel") route mapping and gap closure: the same
      Playwright-driven live-capture method applied to the admin Dashboard's own overview page
      (`docs/11`'s "Dashboard — Painel (overview)" section), against both the reference server and
      Tinyfin. Black-box observation was sufficient for every gap this pass found — no reason to
      read the reference server's C# source at all (AGENTS.md's clean-room requirement treats that
      as a last resort, not a default step). Six routes 404d unconditionally on every page load;
      implemented in parallel by five subagents, consolidated and live-verified by one coordinating
      pass: `GET /Items/Counts` (real Movie/Series/Episode counts via `QueryItems`, every other
      count field a structural zero — this project's scanner never produces those item types);
      `GET /System/Info/Storage` (real `statfs`-backed free/used space per library folder and per
      Tinyfin's own real directories — `Config.DataDir`/`WebDir`, new `Server`-construction fields
      threaded from `main.go`); `GET /Sessions` (a new small in-process session registry, `docs/06`'s
      default mode, populated from `Sessions/Capabilities/Full` and the existing playback-report
      routes — registered admin-only, not the usual authenticated-non-admin pattern, since it lists
      every user's session including IP address); `GET /System/ActivityLog/Entries` (a new, narrowly
      scoped durable activity log — `SessionStarted`/`AuthenticationFailed` only, new sqlite table
      and migration `0005_activity_log.sql`); `GET /ScheduledTasks` (reports exactly one real task,
      library scanning, backed by a new `Server.scanState` tracker — not fabricating any of the
      reference server's ~18 built-in tasks this project has no machinery for); and
      `GET /web/ConfigurationPages` (always `[]` — no plugin system — which also needed a fix in
      `cmd/tinyfin/main.go`'s static-web-asset wrapper, since it would otherwise shadow this one API
      route living under the `/web/` prefix). Found and fixed one real, previously-hidden bug along
      the way, unrelated to the six routes above: `internal/api/auth.go`'s `parseEmbyAuth` never
      percent-decoded the `X-Emby-Authorization` header's fields, so a real jellyfin-web build's
      percent-encoded `Client="Jellyfin%20Web"` came back verbatim instead of decoded — invisible
      until `GET /Sessions` became the first route to ever echo `Client` back on the wire. All of it
      confirmed live end to end against a rebuilt `tinyfin-staging`. WebSocket `/socket` remains this
      page's one still-404ing call — already tracked separately as `docs/06`'s realtime session
      channel, a larger feature this REST-focused pass didn't attempt.
- [ ] TVDB provider — not implemented (see above).
- [x] **Goal:** a freshly scanned library gets enriched metadata without Tinyfin implementing its
      own scraping. Verified via `internal/scan`'s test suite (movies and TV shows, with and
      without a configured provider, including re-scan idempotency) and against the real staging
      deployment: a `Series Infantis` library was added via `POST /Library/VirtualFolders`
      (`collectionType=tvshows`, `refreshLibrary=true`) pointing at a real read-only-mounted host
      folder, scanned, and its Series/Season/Episode hierarchy confirmed browsable through the API.

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
