# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Library scans are much faster on slow filesystems: files whose size and
  mtime are unchanged since their last successful probe reuse the stored media
  info instead of running `ffprobe`; scan workers default to 8, `ffprobe`
  processes are bounded globally, up to 3 libraries scan in parallel, and
  sidecar images are found with one directory listing per folder. New options
  `-scan-concurrency`, `-scan-probe-concurrency`, `-scan-library-parallelism`,
  `-scan-force-probe` (env `TINYFIN_SCAN_*`). Each scan logs probed/skipped
  counts and duration. New migration `0018_scan_signatures`.

### Added

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
