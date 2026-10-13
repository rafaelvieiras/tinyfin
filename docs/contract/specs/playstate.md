# Playstate: watched marking and playback reporting

Scope: marking items watched/unwatched and reporting playback (start, progress, ping, stopped), plus the
resume position and play count that result. Behavior only; every rule below is exercised by
[`scenarios/playstate.json`](../scenarios/playstate.json) unless tagged `[untestable]`.

## Conventions

- Tick = 100 ns; 10,000,000 ticks = 1 s. Fixture movies run about 6 s (about 60,000,000 ticks), fixture
  episodes about 4 s.
- "User data" means the per-user state of an item as returned in `UserData` by `GET /Items/{id}?userId=`:
  `Played`, `PlayCount`, `PlaybackPositionTicks`, `LastPlayedDate`, `IsFavorite`, `Key`, plus
  `UnplayedItemCount` for folders. `LastPlayedDate` is omitted (not `null`) when the item was never played.
- Playback thresholds (server configuration, defaults shown; the contract asserts only outcomes that are
  clearly below/above them):
  - minimum resume percentage: **5 %** of runtime;
  - maximum resume percentage: **90 %** of runtime;
  - minimum resume duration: **300 s** of runtime;
  - "finished" guard: a position within the last **1 s** of the runtime counts as finished.
- Fixture position values used by scenarios: `100000` ticks (0.01 s, about 0.2 %, below the minimum),
  `25000000` (2.5 s, between the thresholds), `59500000` (above the maximum or inside the last second),
  `6000000000` (600 s, beyond the end of every fixture item).
- Because every fixture movie is far shorter than the 300 s minimum resume duration, a **resume position can
  never be saved for a fixture item** with the default configuration: a stop or progress between the
  thresholds marks the item played instead. The branch "between the thresholds and runtime of at least 300 s
  saves a resume position" is specified (rules `progress.R5`, `stopped.R4`) and its scenarios are `pending`
  until a fixture movie of at least 300 s (`item:long`) exists.
- Seeding a resume position for the "mark played/unplayed resets position" scenarios uses
  `POST /UserItems/{itemId}/UserData` with `PlaybackPositionTicks` (owned by another area, tagged
  `depends:userdata-update`).

## Mark played

`POST /UserPlayedItems/{itemId}`

Purpose: mark an item as watched for a user. For a folder (Series, Season) only the folder's own state
changes, see [Folders](#folders).
Auth: any authenticated user or API key; permission to download is irrelevant.

Request: path `itemId`; query `userId` (optional GUID), `datePlayed` (optional ISO 8601 date-time).

Responses:

| Status | Condition |
|---|---|
| 200 | JSON user-data object (below) for the **target user** |
| 400 | `datePlayed` not parseable; `itemId` malformed or all-zero; `userId` malformed; API key without `userId` |
| 401 | anonymous |
| 403 | non-admin with `userId` different from own id |
| 404 | `userId` names no user; item does not exist or is not visible to the user (problem-details body) |

200 body fields: `Played`, `PlayCount`, `PlaybackPositionTicks`, `LastPlayedDate`, `IsFavorite`, `Key`,
`ItemId` (item id), and for folders `UnplayedItemCount` and `PlayedPercentage`.

- **playstate.mark-played.R1** A successful mark returns 200 with `Content-Type: application/json` and the
  user-data object of the item after the change; the same values are then returned in `UserData` by
  `GET /Items/{id}?userId=`.
- **playstate.mark-played.R2** The item ends with `Played=true` and `PlaybackPositionTicks=0`, always (a saved
  resume position is discarded).
- **playstate.mark-played.R3** Without `datePlayed`: `PlayCount` becomes `max(PlayCount, 1)` (it is not
  incremented); `LastPlayedDate` keeps its previous value, or is set to the current time when the item had
  none.
- **playstate.mark-played.R4** With `datePlayed`: `PlayCount` is incremented by 1 (so 0 becomes 1, 1 becomes 2)
  and `LastPlayedDate` equals `datePlayed` expressed in UTC. ISO 8601 with `Z` and with a numeric offset
  (`+02:00`) are accepted; the offset is normalized to UTC. The query name is matched case-insensitively
  (the official web client sends `DatePlayed`). The compact `yyyyMMddHHmmss` form is out of contract.
- **playstate.mark-played.R5** A `datePlayed` that is not a date-time yields 400 and changes nothing.
- **playstate.mark-played.R6** Idempotency: repeating the call without `datePlayed` leaves `PlayCount` and
  `LastPlayedDate` exactly as after the first call.

## Mark unplayed

`DELETE /UserPlayedItems/{itemId}`

Purpose: mark an item as unwatched (for a folder only its own state, see [Folders](#folders)). Auth, `userId` and item resolution as for
mark played; same 400/401/403/404 conditions except those about `datePlayed`. No `datePlayed` parameter.
200 returns the same user-data object.

- **playstate.mark-unplayed.R1** The item ends with `Played=false`, `PlayCount=0`, `PlaybackPositionTicks=0`
  and no `LastPlayedDate` (history is lost).
- **playstate.mark-unplayed.R2** Other user data is untouched: `IsFavorite` (and rating/likes) is preserved.
- **playstate.mark-unplayed.R3** Works and returns 200 on an item that was never played; repeating the call is
  harmless and returns the same state.

## Folders

Applies to `POST`/`DELETE /UserPlayedItems/{itemId}` when `itemId` is a Series or Season.

A folder mark is a state of the folder itself: it does **not** write to the leaf items (episodes) below it.
The folder reports a derived view in which leaves that were never individually touched are counted as played
(after a mark played) or as unplayed (after a mark unplayed).

- **playstate.folder.R1** Marking a folder played or unplayed leaves every descendant leaf's own user data
  unchanged (`Played`, `PlayCount`, `PlaybackPositionTicks`, `LastPlayedDate` as read from the leaf).
- **playstate.folder.R2** After a mark played the folder reports `Played=true`, `UnplayedItemCount=0`,
  `PlayedPercentage=100`, `PlayCount=0` and no `LastPlayedDate`, both in the 200 response and in later
  `GET /Items/{id}`. A Season below a marked Series reports the same; marking only one Season leaves the
  Series with `Played=false` and `UnplayedItemCount` equal to the leaves of the other seasons.
- **playstate.folder.R3** After a mark unplayed the folder reports `Played=false`, `PlayedPercentage=0`,
  `PlayCount=0`, no `LastPlayedDate` and `UnplayedItemCount` equal to the number of all leaves under it,
  regardless of the played state of the leaves themselves (a leaf that is individually played is still
  reported as played when read, but still counted).
- **playstate.folder.R4** `datePlayed` has no observable effect on a folder mark: neither the folder nor any
  leaf gets a `PlayCount` increment or a `LastPlayedDate`.
- **playstate.folder.R5** Changing a single leaf changes the derived state of its ancestors: from a clean
  state, after marking one of three episodes played the series has `Played=false` and `UnplayedItemCount=2`;
  after all three it has `Played=true` and `UnplayedItemCount=0`.
- **playstate.folder.R6** A saved resume position of a leaf survives a folder mark played (the leaf keeps
  `PlaybackPositionTicks` and `Played=false`).
- **playstate.folder.R7** After a folder mark played, unmarking one leaf makes its ancestors `Played=false`
  with `UnplayedItemCount=1` (only that leaf; leaves not individually touched stay counted as played).

## Acting user

Applies to mark played and mark unplayed (and the legacy aliases, where the id comes from the path).

- **playstate.user.R1** With no `userId` the operation targets the authenticated user.
- **playstate.user.R2** `userId` equal to the caller's own id behaves exactly as omitted.
- **playstate.user.R3** An administrator may pass another user's `userId`; that user's data changes and the
  caller's own data does not; the response is the target user's data.
- **playstate.user.R4** A non-administrator passing a `userId` different from their own receives 403, nothing
  changes, and this is decided before the user is looked up (an unknown id also yields 403 for a
  non-administrator).
- **playstate.user.R5** An administrator passing a `userId` that names no user receives 404.
- **playstate.user.R6** A `userId` that is not a GUID receives 400.
- **playstate.user.R7** An API key without `userId` receives 400 (a key has no user of its own).
- **playstate.user.R8** An API key with a valid `userId` acts on that user and returns 200.
- **playstate.user.R9** State is strictly per user: marking or unmarking for one user never changes another
  user's `Played`, `PlayCount`, position or date.

## Target item

- **playstate.item.R1** An `itemId` that is a valid GUID but names no item yields 404 with a JSON
  problem-details document (`status` 404; the text is not part of the contract), for mark played and mark
  unplayed; no data is created.
- **playstate.item.R2** An `itemId` that is not a GUID yields 400.
- **playstate.item.R3** The all-zero GUID yields 400 (not 404).
- **playstate.item.R4** An item outside the user's permitted libraries (`EnableAllFolders=false`, no enabled
  folders) is treated as nonexistent: 404, problem-details document as in `item.R1`.
- **playstate.item.R5** The download permission (`EnableContentDownloading=false`) does not affect marking:
  such a user marks and unmarks normally.

## Authentication

- **playstate.auth.R1** Every playstate route (`POST|DELETE /UserPlayedItems/{itemId}`, `POST
  /Sessions/Playing`, `/Progress`, `/Ping`, `/Stopped`, and the legacy aliases) returns 401 to an anonymous
  caller.
- **playstate.auth.R2** A header carrying only `Token` (no client/device fields) is accepted for a valid user
  token: the omitted client fields are taken from the device registered for the token. (Verified against the
  reference server.)
- **playstate.auth.R3** Legacy credentials are not honored on these routes: `X-Emby-Token` and the `api_key`
  query parameter yield 401 even with a valid token (scope-negative).

## Playback start

`POST /Sessions/Playing`

Purpose: the client announces that playback of an item began. Registers now-playing state on the caller's
session and counts a play.
Auth: any authenticated caller; the session is identified by the client/device/device-id/version of the
authorization header (one session per client+device+user).

Request body (JSON): `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, `IsPaused`, `IsMuted`,
`VolumeLevel`, `PlayMethod` (`Transcode|DirectStream|DirectPlay`), `RepeatMode`
(`RepeatNone|RepeatAll|RepeatOne`), `PlaybackOrder` (`Default|Shuffle`), `CanSeek`, `AudioStreamIndex`,
`SubtitleStreamIndex`, `LiveStreamId`, `PlaylistItemId`, `NowPlayingQueue` (`[{Id, PlaylistItemId}]`),
`Item`, `SessionId`, `PlaybackStartTimeTicks`, `Brightness`, `AspectRatio`.

Responses: 204 empty on success; 400 malformed body, malformed `ItemId`, invalid enum value, wrong field
type; 401 anonymous.

- **playstate.start.R1** A valid report returns 204 with an empty body.
- **playstate.start.R2** Each start increments the item's `PlayCount` by 1 for the calling user, even when the
  same play session starts again without stopping (0 becomes 1, then 2).
- **playstate.start.R3** A start does not change `Played` or `PlaybackPositionTicks` of a movie. It sets
  `LastPlayedDate` to now when the item was not played; when the item was already played (re-watch) the
  `LastPlayedDate` is left unchanged.
- **playstate.start.R4** `PositionTicks` in a start report is not persisted to user data.
- **playstate.start.R5** An `ItemId` that is missing, all-zero or unknown is not an error: 204, and no user
  data is touched (only the session state is updated).
- **playstate.start.R6** An item that exists but is not visible to the caller (restricted user) is also
  accepted with 204; the report is not rejected. (Audit-derived: reports are resolved without the user's
  library filter. Flagged as a divergence candidate, see Open questions.)
- **playstate.start.R7** Invalid input yields 400 and changes nothing: `ItemId` not a GUID, malformed JSON,
  enum fields with unknown names, numeric fields with non-numeric values.
- **playstate.start.R8** `Brightness`, `AspectRatio`, `PlaybackStartTimeTicks`, `NextMediaType` and unknown
  extra properties are accepted and have no effect.
- **playstate.start.R9** `SessionId` in the body is ignored; the server always uses the caller's own session.
- **playstate.start.R10** A start of a video item creates an activity-log entry of type `VideoPlayback` (audio:
  `AudioPlayback`, other: `Playback`); a stop creates `VideoPlaybackStopped` / `AudioPlaybackStopped` and none
  for other media types. No entry when the item is unknown or is theme media. (Pending runtime verification.)
- **playstate.start.R11** [untestable: WebSocket delivery] Start, progress and stop push session and
  user-data-changed messages to subscribed WebSocket clients; progress-only saves do not push user-data-changed.

## Playback progress

`POST /Sessions/Playing/Progress`

Purpose: periodic report of position and player state; also the point at which the resume position (or the
finished state) is saved. Same body as start; same auth; 204 empty on success; 400/401 as for start.

- **playstate.progress.R1** A valid report returns 204 with an empty body.
- **playstate.progress.R2** `PositionTicks` below the minimum resume percentage (5 % of runtime): the stored
  position is 0 and `Played` is unchanged.
- **playstate.progress.R3** `PositionTicks` above the maximum resume percentage (90 %) or within the last
  second of the runtime: `Played=true`, position 0, `LastPlayedDate` = now.
- **playstate.progress.R4** `PositionTicks` between the thresholds and runtime below the minimum resume
  duration (300 s, so every fixture item): `Played=true`, position 0.
- **playstate.progress.R5** `PositionTicks` between the thresholds and runtime of at least 300 s:
  `PlaybackPositionTicks = PositionTicks`, `Played` unchanged, `LastPlayedDate` = now (item enters the resume
  list).
- **playstate.progress.R6** `PositionTicks` omitted: user-data position and `Played` are not touched.
- **playstate.progress.R7** Progress never changes `PlayCount` (only start, a stop with no position, and
  mark-played with a date do).
- **playstate.progress.R8** Progress is accepted without a prior start (the session is created or reused on
  the fly) and updates user data the same way.
- **playstate.progress.R9** A position below the minimum overwrites an existing saved resume position with 0
  (the item leaves the resume list).
- **playstate.progress.R10** Missing, all-zero or unknown `ItemId`: 204, no user data touched.
- **playstate.progress.R11** Malformed `ItemId`, wrong field types or malformed JSON: 400.

## Playback stopped

`POST /Sessions/Playing/Stopped`

Purpose: the client ends playback; finalizes played state and resume position, clears now-playing state.

Request body: `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, `Failed`, `LiveStreamId`,
`PlaylistItemId`, `NowPlayingQueue`, `NextMediaType`, `Item`, `SessionId`.
Responses: 204 empty; 400 (negative `PositionTicks`, malformed body); 401 anonymous.

- **playstate.stopped.R1** A valid report returns 204 with an empty body.
- **playstate.stopped.R2** `PositionTicks` omitted (or null) means "watched to the end": `PlayCount` +1,
  `Played=true`, position 0, `LastPlayedDate` = now. After a start this makes `PlayCount` 2.
- **playstate.stopped.R3** With `PositionTicks`, the same thresholds as progress apply: below 5 % leaves
  `Played` false and position 0; above 90 %, within the last second, or beyond the end: `Played=true`,
  position 0; between and runtime below 300 s: `Played=true`, position 0. `PlayCount` is not changed by the
  stop itself (a start then a stop with a position gives `PlayCount` 1).
- **playstate.stopped.R4** Between the thresholds with runtime of at least 300 s: position saved and `Played`
  false; above 90 % on such an item: `Played=true`, position 0. (Pending, needs `item:long`.)
- **playstate.stopped.R5** `Failed=true` writes nothing: `PlayCount`, `Played` and position keep the values
  they had before the stop (the count from an earlier start stays).
- **playstate.stopped.R6** Negative `PositionTicks` yields 400 and changes no user data.
- **playstate.stopped.R7** Missing, all-zero or unknown `ItemId`: 204, no user data touched.
- **playstate.stopped.R8** An item not visible to the caller is accepted with 204 like start (see
  `start.R6`).
- **playstate.stopped.R9** Start followed by a stop with a position increments `PlayCount` exactly once (by the
  start).
- **playstate.stopped.R10** A stop without a preceding start is valid and updates user data as above (no
  `PlayCount` from the missing start).
- **playstate.stopped.R11** Malformed `ItemId`, wrong field types or malformed JSON: 400.

## Resume

Observed through `GET /UserItems/Resume?userId=`.

- **playstate.resume.R1** An unplayed item with a saved position greater than 0 is listed as resumable.
- **playstate.resume.R2** An item that is marked played, or finished by a stop/progress, is not listed.
- **playstate.resume.R3** Progress below the minimum percentage resets the saved position, which removes the
  item from the list.

## Ping

`POST /Sessions/Playing/Ping?playSessionId=`

Purpose: keep a transcode alive without sending progress.

- **playstate.ping.R1** With a non-empty `playSessionId` the response is 204 with an empty body, whether or
  not any playback or transcode with that id exists.
- **playstate.ping.R2** `playSessionId` missing or empty yields 400.
- **playstate.ping.R3** Ping touches neither user data nor now-playing state.

## Session state

Observed through `GET /Sessions?deviceId=<device id>` (administrator). `PlayState.PositionTicks` drifts
forward 1 s per second while a started item is not paused (automatic progress), so scenarios that assert it
set `IsPaused=true`.

- **playstate.session.R1** After start, the caller's session lists `NowPlayingItem.Id` = `ItemId`.
- **playstate.session.R2** `PlayState` mirrors the report: `IsPaused`, `IsMuted`, `VolumeLevel`, `RepeatMode`,
  `PlaybackOrder`, `CanSeek`, `PositionTicks`, `PlayMethod`. Omitted booleans are false, `RepeatMode` defaults to
  `RepeatNone`, `PlaybackOrder` to `Default`.
- **playstate.session.R3** `PlayMethod` `Transcode` (also the default when omitted) with no active transcode
  for the `PlaySessionId` is recorded as `DirectPlay`.
- **playstate.session.R4** `PlayMethod` `DirectStream` and `DirectPlay` are recorded as sent.
- **playstate.session.R5** Each progress report replaces the previous `PlayState` values.
- **playstate.session.R6** After stopped, the session has no `NowPlayingItem` and its play state is reset.
- **playstate.session.R7** `NowPlayingQueue` is stored on the session only by the stopped report; start and
  progress accept but do not store it. A stopped report with a non-null queue replaces the stored queue.
- **playstate.session.R8** An omitted `MediaSourceId` is reported as the `ItemId` (32 hex digits).
- **playstate.session.R9** [untestable: needs waiting several seconds] While playing and not paused, the
  server advances the session position by 1 s per second (capped at runtime) without writing user data.

## Legacy web routes

Kept only because the official web client calls them (`scope: legacy-web-required`). Thin aliases of the
modern routes; the difference is that the user id is a path segment and is required.

- `POST /Users/{userId}/PlayedItems/{itemId}`: same as `POST /UserPlayedItems/{itemId}?userId={userId}`
  (query `datePlayed`, sent by the web client as `DatePlayed`).
- `DELETE /Users/{userId}/PlayedItems/{itemId}`: same as `DELETE /UserPlayedItems/{itemId}?userId={userId}`.

- **playstate.legacy.R1** The alias returns the same responses and effects as mark played.
- **playstate.legacy.R2** The alias returns the same responses and effects as mark unplayed.
- **playstate.legacy.R3** The path user id follows the acting-user rules: own id or administrator, else 403;
  unknown user 404; anonymous 401.
- **playstate.legacy.R4** `DatePlayed` (any case) with ISO 8601 UTC is honored as in `mark-played.R4`.

## Out of contract

The deprecated `/PlayingItems/...` and `/Users/{userId}/PlayingItems/...` routes (see
[`scope-excluded.md`](../scope-excluded.md)) and the compact `yyyyMMddHHmmss` `datePlayed` format.

## Open questions

1. Reports for items the user cannot see (start/progress/stopped) are accepted and write user data for the
   hidden item (rules `start.R6`, `stopped.R8`). Static audit only; not in the validated security findings.
   Decide whether Tinyfin should reject with 404 and record a `divergence`.
2. `PlayedPercentage` for leaf items and its exact value for folders is not asserted (audit unresolved).
3. Resolved (reference probe): `LastPlayedDate` is omitted from the JSON (not `null`) when the item was never
   played, in both the mark response and `UserData`.
4. Resolved (reference probe): a stop with a position, with or without a preceding start, never changes
   `PlayCount` (start then stop with a position leaves 1; stop with a position alone leaves 0).
5. Position `PositionTicks` negative on progress is unspecified.
6. Multi-user sessions (additional users) and live-TV stream handling are not observable black-box and are not
   specified.
7. API-key scenarios (`user.R7`, `user.R8`) need an API-key identity in the runner; they are pending.
8. A user whose session was removed mid-request, and the 404 "session not found" of start/stopped, cannot be
   triggered black-box.
9. Reports from a restricted user leave a hidden user-data row that scenarios cannot clean up.
10. Fixture `item:long` (movie of at least 300 s) is needed to unpend the resume-position scenarios.

## Rule to scenarios

| Rule | Scenarios (ids without the `playstate.` prefix) |
|---|---|
| `playstate.mark-played.R1` | `mark-played.basic`, `mark-played.response-fields` |
| `playstate.mark-played.R2` | `mark-played.basic`, `mark-played.resets-position` |
| `playstate.mark-played.R3` | `mark-played.basic`, `mark-played.no-date-keeps-count` |
| `playstate.mark-played.R4` | `mark-played.date-utc`, `mark-played.date-offset-normalized`, `mark-played.date-increments-count` |
| `playstate.mark-played.R5` | `mark-played.invalid-date` |
| `playstate.mark-played.R6` | `mark-played.idempotent` |
| `playstate.mark-unplayed.R1` | `mark-unplayed.resets-everything`, `mark-unplayed.resets-position`, `folder.series-mark-unplayed` |
| `playstate.mark-unplayed.R2` | `mark-unplayed.keeps-favorite` |
| `playstate.mark-unplayed.R3` | `mark-unplayed.never-played` |
| `playstate.folder.R1` | `folder.series-mark-played`, `folder.season-mark-played`, `folder.series-mark-unplayed`, `folder.season-mark-unplayed` |
| `playstate.folder.R2` | `folder.series-mark-played`, `folder.season-mark-played` |
| `playstate.folder.R3` | `folder.series-mark-unplayed`, `folder.season-mark-unplayed` |
| `playstate.folder.R4` | `folder.series-with-date` |
| `playstate.folder.R5` | `folder.season-mark-played`, `folder.leaf-updates-ancestor` |
| `playstate.folder.R6` | `folder.keeps-leaf-positions` |
| `playstate.folder.R7` | `folder.leaf-unmarked-after-folder-played` |
| `playstate.user.R1` | `mark-played.basic`, `start.basic` |
| `playstate.user.R2` | `user.explicit-own-id` |
| `playstate.user.R3` | `user.admin-on-behalf-mark`, `user.admin-on-behalf-unmark` |
| `playstate.user.R4` | `user.nonadmin-other-user-forbidden`, `user.nonadmin-other-user-forbidden-unmark`, `user.nonadmin-unknown-user-forbidden` |
| `playstate.user.R5` | `user.admin-unknown-user` |
| `playstate.user.R6` | `user.invalid-guid` |
| `playstate.user.R7` | `user.api-key-without-user (pending)` |
| `playstate.user.R8` | `user.api-key-with-user (pending)` |
| `playstate.user.R9` | `user.isolation` |
| `playstate.item.R1` | `item.unknown-id` |
| `playstate.item.R2` | `item.invalid-guid` |
| `playstate.item.R3` | `item.empty-guid` |
| `playstate.item.R4` | `item.hidden-from-user` |
| `playstate.item.R5` | `item.download-permission-irrelevant` |
| `playstate.auth.R1` | `auth.anonymous-mark-played`, `auth.anonymous-mark-unplayed`, `auth.anonymous-start`, `auth.anonymous-progress`, `auth.anonymous-ping`, `auth.anonymous-stopped` |
| `playstate.auth.R2` | `auth.token-only-header` |
| `playstate.auth.R3` | `auth.legacy-header-not-honored` |
| `playstate.start.R1` | `start.basic` |
| `playstate.start.R2` | `start.basic`, `start.repeat-increments-count` |
| `playstate.start.R3` | `start.basic`, `start.rewatch-keeps-last-played` |
| `playstate.start.R4` | `start.basic`, `start.position-not-persisted` |
| `playstate.start.R5` | `start.no-item-id`, `start.unknown-item` |
| `playstate.start.R6` | `start.hidden-item` |
| `playstate.start.R7` | `start.invalid-item-guid`, `start.malformed-json`, `start.invalid-enum` |
| `playstate.start.R8` | `start.ignored-fields-accepted` |
| `playstate.start.R9` | `start.ignored-fields-accepted` |
| `playstate.start.R10` | `start.activity-log (pending)` |
| `playstate.start.R11` | none (untestable) |
| `playstate.progress.R1` | `progress.below-minimum` |
| `playstate.progress.R2` | `progress.below-minimum` |
| `playstate.progress.R3` | `progress.above-maximum` |
| `playstate.progress.R4` | `progress.mid-short-video` |
| `playstate.progress.R5` | `progress.long-runtime-saves-position (pending)` |
| `playstate.progress.R6` | `progress.position-omitted` |
| `playstate.progress.R7` | `progress.below-minimum`, `progress.does-not-count` |
| `playstate.progress.R8` | `progress.without-start` |
| `playstate.progress.R9` | `progress.below-minimum-clears-position` |
| `playstate.progress.R10` | `progress.no-item` |
| `playstate.progress.R11` | `progress.invalid-body` |
| `playstate.stopped.R1` | `stopped.no-position-is-complete` |
| `playstate.stopped.R2` | `stopped.no-position-is-complete`, `stopped.start-then-no-position` |
| `playstate.stopped.R3` | `stopped.below-minimum`, `stopped.mid-short-video`, `stopped.above-maximum`, `stopped.beyond-runtime` |
| `playstate.stopped.R4` | `stopped.long-runtime-saves-position (pending)`, `stopped.long-runtime-end-is-played (pending)` |
| `playstate.stopped.R5` | `stopped.failed` |
| `playstate.stopped.R6` | `stopped.negative-position` |
| `playstate.stopped.R7` | `stopped.no-item` |
| `playstate.stopped.R8` | `stopped.hidden-item` |
| `playstate.stopped.R9` | `stopped.below-minimum`, `stopped.mid-short-video` |
| `playstate.stopped.R10` | `stopped.no-position-is-complete`, `stopped.without-start` |
| `playstate.stopped.R11` | `stopped.invalid-body` |
| `playstate.resume.R1` | `resume.saved-position-listed-until-played`, `resume.long-runtime-listed (pending)` |
| `playstate.resume.R2` | `resume.saved-position-listed-until-played` |
| `playstate.resume.R3` | `resume.below-minimum-removes-entry` |
| `playstate.ping.R1` | `ping.basic`, `ping.unknown-play-session` |
| `playstate.ping.R2` | `ping.missing-id` |
| `playstate.ping.R3` | `ping.basic`, `ping.no-session-effect` |
| `playstate.session.R1` | `session.now-playing-and-state` |
| `playstate.session.R2` | `session.now-playing-and-state`, `session.defaults` |
| `playstate.session.R3` | `session.transcode-without-job-is-direct-play` |
| `playstate.session.R4` | `session.direct-stream-kept` |
| `playstate.session.R5` | `session.progress-replaces-state` |
| `playstate.session.R6` | `session.stopped-clears-now-playing` |
| `playstate.session.R7` | `session.queue-stored-only-by-stopped` |
| `playstate.session.R8` | `session.media-source-default` |
| `playstate.session.R9` | none (untestable) |
| `playstate.legacy.R1` | `legacy.mark-played-path-user`, `legacy.unknown-item-and-anonymous` |
| `playstate.legacy.R2` | `legacy.mark-unplayed-path-user` |
| `playstate.legacy.R3` | `legacy.other-user-forbidden`, `legacy.admin-on-behalf`, `legacy.unknown-item-and-anonymous` |
| `playstate.legacy.R4` | `legacy.mark-played-path-user` |
