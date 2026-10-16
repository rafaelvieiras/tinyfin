# Policy enforcement: content deletion and downloading

Which `UserPolicy` flags change what a caller may do with an item's file: `EnableContentDeletion`, `EnableContentDeletionFromFolders` and `EnableContentDownloading`. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/policy.json`](../scenarios/policy.json), [`../scenarios/policy2.json`](../scenarios/policy2.json) (playback flags; libraries `AvTrack` and `Music`). Fixture state: [`../fixtures.json`](../fixtures.json) (`alpha`, users `alice` and `nodownload`); the other users are created and deleted by the scenarios.

## <a id="download"></a>GET /Items/{itemId}/Download

- **policy.download.R1** A user with `EnableContentDownloading` receives the original file: `200`, `Content-Type` from the extension (`video/mp4`), `Accept-Ranges: bytes` and `Content-Disposition: attachment; filename="<file name>"; filename*=UTF-8''<percent-encoded name>`. A `Range` request answers `206` with `Content-Range`. `HEAD` is `405` on the reference.
- **policy.download.R2** A user without `EnableContentDownloading` receives `403`. An administrator without it receives `400` (observed; the reference gives no body). There is no administrator bypass.
- **policy.download.R3** Anonymous is `401` (so is an API key, which belongs to no user), an unknown id `404`, an item the caller cannot read `404`, a folder (library, series, season) `400`.
- Streaming through `GET /Videos/{id}/stream?static=true` is **not** subject to the flag on the reference (`200` for a user without it); Tinyfin matches.

## <a id="delete"></a>DELETE /Items/{itemId}

- **policy.delete.R1** The caller may delete an item when `EnableContentDeletion` is true (any library) or the item's library id is in `EnableContentDeletionFromFolders` (case-insensitive). Otherwise `401` with the JSON string `"Unauthorized access"`. Administrators get no bypass: an administrator without either is `401`. An API key is `401`. Unknown or unreadable items are `404` (before the permission check). Collections and playlists have their own rules (`collections.md`, `playlists.md`).
- **policy.delete.R2** [untestable: a permitted delete destroys the fixture file on the reference; covered by Go tests] For a permitted caller the reference removes the media file (and the movie's folder when no other video is left in it) and the item: `204`. **Divergence (decision, `collections.md`): Tinyfin never deletes media files and answers a permitted caller `403`.** No scenario sends it, since it would destroy the fixture.

## <a id="item"></a>Item permission fields

- **policy.item.R1** `CanDelete` is true exactly when R1 of *delete* would allow the caller (no administrator bypass; false for folder-like items other than collections the caller may manage); `CanDownload` is true when `EnableContentDownloading` is true and the item is not folder-like. Both are false for a user without the respective permission.

## <a id="playback"></a>Playback flags

`EnableMediaPlayback`, `EnableAudioPlaybackTranscoding`, `EnableVideoPlaybackTranscoding` and `EnablePlaybackRemuxing`, on a non-administrator probed with `POST /Items/{id}/PlaybackInfo`, the stream routes and the session reporting routes. Only the transcode offer of PlaybackInfo reacts; no route refuses.

- **policy.playback.R1** `EnableMediaPlayback=false` changes nothing the reference answers: PlaybackInfo keeps its media source (`ErrorCode` absent, direct play on offer as for anyone), `GET /Videos/{id}/stream`, `/Audio/{id}/stream`, the HLS `master.m3u8`/`main.m3u8`, `/Sessions/Playing*`, `/PlayingItems/{id}` and `/Audio/{id}/universal` all answer as for a user with the flag. Tinyfin matches by not enforcing it.
- **policy.playback.R2** For a video, PlaybackInfo offers no transcode when `EnableVideoPlaybackTranscoding`, `EnableAudioPlaybackTranscoding` and `EnablePlaybackRemuxing` are **all** false: the source is still listed (one entry, no `ErrorCode`), `SupportsTranscoding` false, no `TranscodingUrl`, and `SupportsDirectPlay`/`SupportsDirectStream` false when the profile cannot play the file as it is (a track or container it does not take). A source the profile direct plays stays direct play (with `SupportsTranscoding` false). With any one of the three on, the offer is the usual HLS `TranscodingUrl`.
- **policy.playback.R3** For a track (`Audio`), `EnableAudioPlaybackTranscoding=false` alone decides: `SupportsTranscoding` false, no `TranscodingUrl`, and the track is offered as it is whatever the profile says (`SupportsDirectPlay` and `SupportsDirectStream` true), with or without a device profile. The video flags are ignored for a track.
- **policy.playback.R4** The stream routes ignore the three transcoding flags too: a user with all of them off is served `master.m3u8`, `main.m3u8` and the transcoded `stream`/`stream.mp4`/`stream.mp3` when the URL is built by hand.

Known differences, observed and not reproduced:

* **Progressive offer.** With `EnableVideoPlaybackTranscoding` and `EnablePlaybackRemuxing` both false and `EnableAudioPlaybackTranscoding` true, the reference's `TranscodingUrl` for a video is a progressive `/videos/{id}/stream?...` (no codec parameters, `TranscodingSubProtocol` `http`, `SubtitleMethod=Encode`) instead of the HLS `master.m3u8`; Tinyfin keeps offering HLS.
* **No device profile.** The reference answers `SupportsDirectStream` true for a video without a profile when transcoding is allowed (Tinyfin false, as before this wave).
* **Folder access.** A user with no enabled folder (`EnableAllFolders` false) gets `404` from PlaybackInfo, `Download` and the item on both servers, but the reference serves `GET /Videos/{id}/stream` and the HLS routes to that user (`200`/`206`); Tinyfin answers `404` there. Not asserted.
* `MaxParentalRating`, `BlockedTags`, `AllowedTags`, `BlockUnratedItems` and `EnabledFolders` are enforced on item queries (users/items scenarios); whether they gate the stream routes was not probed beyond the folder case above.

## Not enforced yet

`MaxActiveSessions` is enforced on login (`users.auth.R16`). Of the playback flags only the transcode offer of PlaybackInfo is policy-driven (R2, R3); every other flag below is stored and returned but has no effect, as on the reference where probed: `EnableMediaPlayback` (R1). Not probed at all: `EnableRemoteControlOfOtherUsers`, `EnableSharedDeviceControl`, `EnableLiveTvAccess`/`EnableLiveTvManagement`, `EnableSyncTranscoding`, `EnableSubtitleManagement`, `ForceRemoteSourceTranscoding`, `RemoteClientBitrateLimit`, `EnabledChannels`/`EnabledDevices`, `BlockedMediaFolders` and the stream-route behavior of the tag, rating and folder flags.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `policy.download.R1` | `policy.download.default` |
| `policy.download.R2` | `policy.download.denied`, `policy.download.admin-denied` |
| `policy.download.R3` | `policy.download.errors` |
| `policy.delete.R1` | `policy.delete.denied`, `policy.delete.folders-not-matching`, `policy.delete.admin-without-flag` |
| `policy.item.R1` | `policy.item.can-flags`, `policy.delete.denied`, `policy.delete.folders-not-matching`, `policy.delete.admin-without-flag` |
| `policy.playback.R1` | `policy.playback.media-playback-off` |
| `policy.playback.R2` | `policy.playback.transcoding-off`, `policy.playback.transcoding-partial` |
| `policy.playback.R3` | `policy.playback.audio-transcoding-off` |
| `policy.playback.R4` | `policy.playback.transcoding-off` |
