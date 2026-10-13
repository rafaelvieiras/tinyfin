# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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

- Identifiers are written as 32 lowercase hex digits without dashes in every
  response (`wire.id.R1`); ids in dashed or upper-case form are still accepted
  in paths, query parameters and bodies (`wire.id.R2`). Existing databases are
  migrated in place (migration 0013) and a persisted dashed server id is
  folded on startup, so clients see a one-time `ServerId` spelling change.
- Compatibility scope is Jellyfin 12+ only: pre-12 legacy routes and legacy
  authentication channels are not part of the contract (decision D14).
