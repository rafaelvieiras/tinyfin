# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A scan forgets items whose file or folder is gone (children, extras, collection
  and playlist membership with them); users' play state is kept for when the
  file returns. An inaccessible or empty library folder forgets nothing.
  Refreshing a series, season or folder item re-walks it.
- `.ignore` files (empty: whole folder; otherwise gitignore-like name patterns,
  nearest file wins), hidden folders and the fixed exclusion names in every
  library type; extras-named folders directly under a movie library root are
  ordinary folders.
- DVD and Blu-ray folders and `.iso`/`.img` images as movies (`VideoType`,
  `IsoType`, DVD media info from the main title set, `Data` streams).
- `.strm` files (address or path): item, media source, on-demand probe and
  proxied stream.
- Real-time library monitor (Linux, inotify) for libraries whose options carry
  `EnableRealtimeMonitor`.
- Library options applied by a scan: `EnablePhotos`, `SeasonZeroDisplayName`,
  `EnableEmbeddedTitles`.
- Contract: `specs/scan.md`, 23 scenarios; the runner can change the media tree
  (`fs` steps, `-media-local-root`) and wait for a background change (`retryMs`).

### Changed

- A media source is named after its file, like the reference.
- `ImageTags`, `BackdropImageTags` and `ImageBlurHashes` are always present on an
  item (empty when it has no picture).
- Library scans are much faster on slow filesystems: files whose size and
  mtime are unchanged since their last successful probe reuse the stored media
  info instead of running `ffprobe`; scan workers default to 8, `ffprobe`
  processes are bounded globally, up to 3 libraries scan in parallel, and
  sidecar images are found with one directory listing per folder. New options
  `-scan-concurrency`, `-scan-probe-concurrency`, `-scan-library-parallelism`,
  `-scan-force-probe` (env `TINYFIN_SCAN_*`). Each scan logs probed/skipped
  counts and duration. New migration `0018_scan_signatures`.

### Added

- Subtitles. External subtitle files beside a video (`.srt .ass .ssa .vtt .smi
  .sami .sup`, named `<video>.<lang>[.forced][.default][.sdh].<ext>` or with a
  free title, any case) are listed as `MediaStreams` ahead of the video's own
  streams, in the reference's file-name order, with `Language`, `Title`,
  `IsForced`/`IsDefault`/`IsHearingImpaired`, `IsExternal`, `Path`, `Score`
  and the `DisplayTitle` the reference builds (language names come from the
  culture list, now `internal/culture`). The folder listing is read once per
  directory by the scan, so adding, removing or renaming a file is seen on
  the next scan without re-probing the video. `HasSubtitles` covers them.
  `GET /Videos/{id}/{mediaSourceId}/Subtitles/{index}/Stream.{format}` (and
  `.../{startPositionTicks}/Stream.{format}`) serves them, and embedded text
  tracks (extracted with ffmpeg), as `srt`, `vtt`, `ass`, `ssa`, `json` and
  `ttml` with `startPositionTicks`, `endPositionTicks`, `copyTimestamps` and
  `addVttTimeMap`; `.../subtitles.m3u8` lists the WebVTT windows.
  PlaybackInfo sets `DefaultSubtitleStreamIndex`/`DefaultAudioStreamIndex`
  (from `SubtitleMode`, `SubtitleLanguagePreference`) and, when the request
  carries a device profile, `DeliveryMethod`/`DeliveryUrl` per subtitle
  stream. `POST /Videos/{id}/Subtitles` and `DELETE
  /Videos/{id}/Subtitles/{index}` manage external files; the provider search
  routes answer like a server with no subtitle provider, and
  `/FallbackFont/Fonts` lists nothing. The stream routes require a token
  (the reference serves them anonymously). New contract area `subtitles`
  (`docs/contract/specs/subtitles.md`, 23 scenarios), a text-body assertion
  (`expect.text`) in the conformance runner, and a lazy `SubFiles` fixture
  library. Not done: burning a subtitle into the transcoded video and
  subtitle groups in HLS manifests.
- Coverage tracker: `docs/COVERAGE.md` (overview, per controller and per
  subsystem) and `docs/coverage/` (every operation of the Jellyfin 12.2
  OpenAPI document with a status computed from the registered routes and the
  passing contract scenarios, plus hand-written subsystem features), generated
  by `go run ./cmd/conformance coverage` and validated in CI with `-check`.
  The README carries a generated summary and badges.
- Local artwork follows the reference: `png`, `jpg`, `jpeg`, `webp`, `tbn` and
  `gif` pictures in any case; `<video>`/`<video>-poster`/`poster`/`folder`/
  `cover`/`default`/`movie` for `Primary`; `Logo`, `Banner`, `Art`, `Thumb` and
  `Disc` names; every `fanart`/`background`/`art`/`backdrop<N>`/`extrafanart/`
  match as an indexed `Backdrop`; season pictures from the series folder
  (`seasonNN-poster`, `season-specials-poster`) and the season folder; episode
  `<episode>-thumb`/`<episode>` pictures; folder items and the library root's
  own pictures; `ImageBlurHashes`, tags that change with the file, `Art` in
  `ImageTags`, `Backdrop/{index}` answering 404/400 like the reference and
  immutable caching for tagged requests.
- Trailers, special features, theme songs and theme videos are cataloged as
  items owned by their movie, series or season and served by
  `/Items/{id}/LocalTrailers`, `/SpecialFeatures`, `/ThemeSongs`,
  `/ThemeVideos`, `/ThemeMedia` and the user-scoped legacy forms; items carry
  `ExtraType`, `LocalTrailerCount`, `SpecialFeatureCount` and (stacked movies)
  `PartCount`; `/Videos/{id}/AdditionalParts` lists the other files of a
  stacked movie. Extras never show up in listings, counts or searches. New
  migration `0020_extras`.
- Other versions of a movie and files of one episode with the same numbering
  and name are media sources of one item (selectable by `MediaSourceId`).
- Season folders that share a number are counted in the series' `ChildCount`;
  a folder named like an episode (`S26E01`) is a plain folder; `E5E6`,
  `E05E06E07` and `Show Title3` numbering; a movie folder that also holds a
  sub folder is a `Folder` item; a folder of a series with no video in it is a
  plain `Folder` (not a season); `ico`, `wbmp` and camera raw pictures are
  photos.
- Conformance: specs `images.md` and `extras.md`, 35 new scenarios, lazy
  fixture libraries `Art`, `ArtShows`, `Extras`, `ExtraShows`, `Resid` and
  `PhotoTypes`, and `{{library.<key>.id}}` templates.
- Collections (`BoxSet`): `POST /Collections` (name, ids), `POST` and
  `DELETE /Collections/{id}/Items`, and `DELETE /Items/{id}` for collections,
  gated on the `EnableCollectionManagement` policy (administrators always). A
  collection links its members without moving them, lists them in
  premiere-date order, derives its year, genres and lowest rating from them,
  reports played state derived from the leaves below (and writes a played mark
  to them), and is found by `IncludeItemTypes=BoxSet` under the collections
  library or a content library, in `CollapseBoxSetItems` and in
  `/Items/Counts`. The `Collections` library is created with the first
  collection and shown as a view only while a collection is visible.
- Library scans build collections from movie `<set>` names when the library
  option `AutomaticallyAddToCollection` is on and at least two movies share a
  name: new members join existing collections, nothing is removed, a refresh of
  one library leaves collections alone. `POST /Library/VirtualFolders` keeps
  the `LibraryOptions` it is sent. New migration `0020_collections`
  (`items.set_name`, unique collection links); contract: `specs/collections.md`.
- Library scans follow the reference's naming without metadata: a movie folder
  keeps its text as the name (`Foo (2015)`) and the year is read from it; loose
  movie files, folders of several movies, stacked parts and versions are
  cataloged, nested folders become `Folder` items; episode numbering covers
  `Show - 05`, bare and four digit numbers, `Season 2 Episode 13`, `[S02E15]`,
  dates, `S01E02E03` and `1x05-06` (with `IndexNumberEnd`), and episode names
  are cut at the year and release tokens.
- Folders of a series that are not `Season N` become seasons named after them,
  extras folders are skipped, folders inside a season are `Folder` items, an
  episode naming a season without a folder gets an empty season, and a season
  lists the episodes that carry its number.
- Home-videos libraries catalog pictures as `Photo` items and folders holding
  pictures as `PhotoAlbum`.
- An `.nfo` with several `<episodedetails>` is one multi-episode item (titles
  joined with ` / `).
- `RecursiveItemCount` on folders, seasons and series; `MediaType` is `Unknown`
  for everything that is not playable; listings carry `ChildCount` and
  `RecursiveItemCount` only when asked for in `Fields`.
- Network `EnableRemoteAccess=false` answers `503` to requests from outside the
  local network.
- Sort names drop the articles `the`, `a` and `an` anywhere as whole words.

- Home-videos libraries are scanned: nested folders become `Folder` items and
  video files `Video` items named after the file, with probed media, an
  optional `<name>.nfo` and the same playback path as movies; hidden entries,
  sidecar folders and trailer or sample files are left out.
- Episodes without a `SxxExx` pattern (specials, loose files in season folders
  and files directly in a series folder) are cataloged: named by the `.nfo`
  title or the file name, numbered only by what the name or the `.nfo` says,
  under their season folder or a season created for them (`Season N`,
  `Season Unknown`); season 0 is named `Specials`.
- A series takes `RunTimeTicks` from its `tvshow.nfo` `<runtime>` (a `0` is
  returned as `0`).
- User configuration validation per the users contract: `null` for a boolean,
  `SubtitleMode` or a list member and non-GUID list entries answer `400`,
  `SubtitleMode` is a lenient enumeration (numbers, any letter case, comma
  combinations), numbers and booleans are accepted for string members, and a
  `null` or non-object body answers `400`; also applied to the `Configuration`
  of `POST /Users`.
- `GET|POST /Auth/Keys` and `DELETE /Auth/Keys/{key}` specified and matched:
  listing members (`Id` `0`, `IsActive` `false`, no `DateRevoked`), verbatim
  application names and exact, case-sensitive token revocation.
- `GET /Items` query semantics per the items contract: library-views form, scope
  and parent rules, library access restriction (also for `Ids`), filters, search
  ranking, multi-key sorting, paging and `Fields` shaping; `GET /Items/{id}`
  returns a problem document for unknown or hidden items.
- Playstate area per the playstate contract: mark played/unplayed
  (`datePlayed`, play count and position rules, acting-user rules, folder marks
  that never write to the leaves), `POST|DELETE /UserFavoriteItems/{id}`,
  playback start/progress/stopped reporting with resume thresholds, live session
  state (`NowPlayingItem`, `PlayState`, queue) readable through
  `GET /Sessions?deviceId=`, `ExcludeActiveSessions` on `/UserItems/Resume`, and
  the `POST|DELETE /Users/{userId}/PlayedItems/{itemId}` aliases the official
  web client still calls.
- `GET|POST /System/Configuration/network` (administrator) with
  `LocalNetworkSubnets`: when set, only those subnets and loopback count as the
  local network for the remote-access login rule, the password-reset PIN file
  and `IsInNetwork`; unset keeps the private and link-local ranges.
- Conformance runner: server-file steps (`-server-data-dir`, `-read-file-cmd`),
  named configuration sections, the `origin:non-loopback` capability, risky
  scenarios ordered last behind a sole-administrator guard, and `-strict`.
- Executable behavior contract for Jellyfin 12+ wire compatibility under
  `docs/contract/` (users, items, playstate) with a conformance runner
  (`cmd/conformance`) that runs the same scenarios against a reference server
  and against Tinyfin.

### Changed

- Titles, sort titles, original titles and plots from `.nfo` files are stored
  without surrounding white space, and sort keys ignore it.
- Episodes are named after their file (without the extension) when no `.nfo`
  title exists, and an episode `.nfo` `<season>`/`<episode>` replaces the
  numbers of the file name.
- The `IsFolder` query parameter of `GET /Items` is ignored, as on the
  reference; `Filters=IsFolder` is unchanged.
- Playback info and stream requests for an item with no media file (a series,
  season or folder) answer `400`.
- Identifiers are written as 32 lowercase hex digits without dashes in every
  response (`wire.id.R1`); ids in dashed or upper-case form are still accepted
  in paths, query parameters and bodies (`wire.id.R2`). Existing databases are
  migrated in place (migration 0013) and a persisted dashed server id is
  folded on startup, so clients see a one-time `ServerId` spelling change.
- Item sort names follow the reference: a leading article is dropped, accents
  fold to ASCII, digit runs are zero-padded and episodes order by season and
  episode (`items.sort.R8`, `R9`); `SortName` is returned in listings only when
  requested through `Fields`. Existing items pick the new key up on their next
  library scan.
- `IsHd` and `Is4K` select resolution buckets and combine as a union
  (`items.filter.R17`).
- A progress or stopped report below the minimum resume position no longer
  sets `LastPlayedDate`; a negative progress position is stored as given.
- QuickConnect: authorizing an already authorized code answers 500 and
  authorizing moves `DateAdded` one minute forward, as on the reference.
- Compatibility scope is Jellyfin 12+ only: pre-12 legacy routes and legacy
  authentication channels are not part of the contract (decision D14).
