# Policy enforcement: content deletion and downloading

Which `UserPolicy` flags change what a caller may do with an item's file: `EnableContentDeletion`, `EnableContentDeletionFromFolders` and `EnableContentDownloading`. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/policy.json`](../scenarios/policy.json), [`../scenarios/policy2.json`](../scenarios/policy2.json) (playback flags, stream routes, remote limits; libraries `AvTrack`, `Music`, `Rated` and `Tagged`). Fixture state: [`../fixtures.json`](../fixtures.json) (`alpha`, users `alice` and `nodownload`); the other users are created and deleted by the scenarios.

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
- **policy.playback.R2** For a video, PlaybackInfo offers no transcode when `EnableVideoPlaybackTranscoding`, `EnableAudioPlaybackTranscoding` and `EnablePlaybackRemuxing` are **all** false: the source is still listed (one entry, no `ErrorCode`), `SupportsTranscoding` false, no `TranscodingUrl`, and `SupportsDirectPlay`/`SupportsDirectStream` false when the profile cannot play the file as it is (a track or container it does not take). A source the profile direct plays stays direct play (with `SupportsTranscoding` false). With any one of the three on, the offer is the usual HLS `TranscodingUrl` (except audio transcoding alone, R7).
- **policy.playback.R3** For a track (`Audio`), `EnableAudioPlaybackTranscoding=false` alone decides: `SupportsTranscoding` false, no `TranscodingUrl`, and the track is offered as it is whatever the profile says (`SupportsDirectPlay` and `SupportsDirectStream` true), with or without a device profile. The video flags are ignored for a track.
- **policy.playback.R4** The stream routes ignore the three transcoding flags too: a user with all of them off is served `master.m3u8`, `main.m3u8` and the transcoded `stream`/`stream.mp4`/`stream.mp3` when the URL is built by hand.
- **policy.playback.R5** A video the device profile plays as it is (direct play) is also listed as streamable: `SupportsDirectStream` true whatever the flags, `SupportsTranscoding` true when any of the three flags is on and false when all are off. No `TranscodingUrl`.
- **policy.playback.R6** Without a `DeviceProfile` a video is direct play (`SupportsDirectPlay` true), `SupportsDirectStream` is the value of `EnablePlaybackRemuxing` and `SupportsTranscoding` the value of `EnableVideoPlaybackTranscoding` (administrators have both on, so `true`/`true`). This is the "no profile" case that used to answer `SupportsDirectStream` false.
- **policy.playback.R7** With `EnableVideoPlaybackTranscoding` and `EnablePlaybackRemuxing` both false and `EnableAudioPlaybackTranscoding` true, a video the profile cannot play as it is gets a **progressive** offer in place of the HLS one, whatever the transcoding profiles say (even an `hls` one or one for another codec): `SupportsDirectPlay`, `SupportsDirectStream` false, `SupportsTranscoding` true, `TranscodingSubProtocol` `http`, no `TranscodingContainer`, and `TranscodingUrl` is `/videos/{id}/stream?...` (no codec or bitrate parameters; `MediaSourceId`, `AudioStreamIndex`, `SubtitleStreamIndex` and `SubtitleMethod=Encode` when there is a subtitle index, `PlaySessionId`, `ApiKey`, `TranscodeReasons` such as `ContainerNotSupported` or `AudioCodecNotSupported`). With remuxing **or** video transcoding on (audio transcoding either way) the offer is the usual one: HLS `master.m3u8` for an `hls` transcoding profile, `stream.mp4` for an `http` one. The reference serves the progressive URL with the file remuxed (`200`, no range support); Tinyfin serves the original file (`200`/`206`) from `/Videos/{id}/stream`.
- **policy.playback.R8** `EnableSyncTranscoding`, `ForceRemoteSourceTranscoding` (for sources that are local files), `EnabledChannels`/`EnableAllChannels` and `BlockedChannels` change nothing PlaybackInfo answers.

## <a id="stream"></a>Stream routes and item visibility

- **policy.stream.R1** The stream routes do not apply the caller's library, tag or rating rules: `GET /Videos/{id}/stream` (with or without `static`), `/Videos/{id}/master.m3u8`, `/Videos/{id}/main.m3u8`, `/Audio/{id}/stream`, the audio HLS playlists and the subtitle routes answer `200` for an item the caller cannot see (`EnableAllFolders` false with the library not listed, `MaxParentalRating`, `BlockUnratedItems`, `BlockedTags`, `AllowedTags`), as they do for anyone who knows the id. An unknown id is `404`. `PlaybackInfo`, `GET /Items/{id}`, `Download` and `/Audio/{id}/universal` do apply the rules (`404`). Tinyfin matches, with one deliberate difference: an anonymous caller is `401` on these routes (the reference serves them).
- **policy.folders.R1** A user with no enabled folder (`EnableAllFolders` false, `EnabledFolders` empty or not holding the item's library) gets `404` from PlaybackInfo, the item and `Download`, and `200` from the stream routes (R1 above).
- **policy.folders.R2** `BlockedMediaFolders` (like `BlockedChannels`) is accepted, stored and returned, and has no effect: the libraries stay in the user's views and every item stays readable. Tinyfin used to hide the listed libraries.

## <a id="remote"></a>Remote clients

- **policy.remote.R1** `RemoteClientBitrateLimit` (bits per second, `0` = none) caps the bitrate ceiling of a **remote** caller (outside the local network, see `users.auth.R21`) in PlaybackInfo. The ceiling is the request's `MaxStreamingBitrate` (body, else query, else the profile's), lowered to the limit. A video or track whose container bitrate (file size over run time) exceeds it is not direct played: `SupportsDirectPlay` and `SupportsDirectStream` false, `SupportsTranscoding` true, an HLS `TranscodingUrl` whose `TranscodeReasons` is `ContainerBitrateExceedsLimit` (tracks: the `stream.mp3` offer). A request without a `DeviceProfile` is not limited, a local caller is not limited, and the stream routes are not limited. The same `ContainerBitrateExceedsLimit` rule applies to the request's/profile's own ceiling for a local caller (video; tracks already did).
- **policy.remote.R2** [untestable: needs a reachable remote address] `ForceRemoteSourceTranscoding` for a `.strm` source that holds an address, when the request has a `DeviceProfile`: `SupportsDirectPlay`/`SupportsDirectStream` false, `SupportsTranscoding` true, `TranscodingSubProtocol` `http`, `TranscodingContainer` `mp4` and `TranscodingUrl` `/videos/{id}/stream.mp4?...&Static=true&VideoCodec=..&AudioCodec=..&AudioStreamIndex=..` (no `TranscodeReasons`). Without a profile the source stays direct play. Probed on the reference with a source on a reachable HTTP address; Tinyfin builds the same offer and serves the address through its proxy route.

Known differences, observed and not reproduced:

* **Parameters of the offers.** The reference's URLs also carry `DeviceId`, `RequireAvc`, `EnableAudioVbrEncoding`, `Tag`, bitrate parameters and `allowVideoStreamCopy`/`allowAudioStreamCopy`; Tinyfin's carry the ones listed in R7 only, and the path is `/Videos/{id}/...`.
* **Stream copy flag.** For a container-only mismatch with an `hls` profile the reference reports `SupportsDirectStream` false; Tinyfin true (also without any policy). An `http` transcoding profile gets `stream.mp4` on the reference and HLS on Tinyfin.
* **No profile, other containers.** Without a profile the reference direct plays any container (`mkv`); Tinyfin applies its default profile, which transcodes it.
* **Progressive transcode.** `GET /Videos/{id}/stream` without `static` is a remux on the reference (no `Accept-Ranges`); Tinyfin serves the original file.
* `MaxParentalRating`, `BlockedTags`, `AllowedTags`, `BlockUnratedItems` and `EnabledFolders` are enforced on item queries and single-item routes (users/items scenarios), not on the stream routes (R1).

## Not enforced yet

`MaxActiveSessions` is enforced on login (`users.auth.R16`), `EnabledDevices`/`EnableAllDevices` at login (`users.auth.device-restriction`). The playback flags act through PlaybackInfo only (R2-R8, `policy.remote.R1`/`R2`); `EnableMediaPlayback` is stored and has no effect (R1), as on the reference. Also stored and without effect on the reference where probed (`policy.playback.R8`): `EnableSyncTranscoding`, `EnabledChannels`/`BlockedChannels`, `BlockedMediaFolders`. Out of scope: `EnableLiveTvAccess`/`EnableLiveTvManagement` (Live TV is not implemented), `EnableRemoteControlOfOtherUsers`, `EnableSharedDeviceControl` and `EnableSubtitleManagement` (not probed here; subtitle management has its own rules in `subtitles.md`).

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
| `policy.playback.R5` | `policy.playback.direct-play-flags`, `policy.playback.transcoding-off` |
| `policy.playback.R6` | `policy.playback.direct-play-flags` |
| `policy.playback.R7` | `policy.playback.audio-only-transcoding` |
| `policy.playback.R8` | `policy.playback.unrelated-flags` |
| `policy.stream.R1`, `policy.folders.R1` | `policy.stream.folders-off`, `policy.stream.rating-tags` |
| `policy.folders.R2` | `policy.folders.blocked-media-folders` |
| `policy.remote.R1` | `policy.remote.bitrate-limit`, `policy.playback.unrelated-flags` |
