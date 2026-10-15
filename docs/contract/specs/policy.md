# Policy enforcement: content deletion and downloading

Which `UserPolicy` flags change what a caller may do with an item's file: `EnableContentDeletion`, `EnableContentDeletionFromFolders` and `EnableContentDownloading`. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/policy.json`](../scenarios/policy.json). Fixture state: [`../fixtures.json`](../fixtures.json) (`alpha`, users `alice` and `nodownload`); the other users are created and deleted by the scenarios.

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

## Not enforced yet

`MaxActiveSessions` is enforced on login (`users.auth.R16`). `EnableMediaPlayback`, `EnableAudioPlaybackTranscoding`, `EnableVideoPlaybackTranscoding` and `EnablePlaybackRemuxing` are stored and returned but ignored by PlaybackInfo and the stream routes.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `policy.download.R1` | `policy.download.default` |
| `policy.download.R2` | `policy.download.denied`, `policy.download.admin-denied` |
| `policy.download.R3` | `policy.download.errors` |
| `policy.delete.R1` | `policy.delete.denied`, `policy.delete.folders-not-matching`, `policy.delete.admin-without-flag` |
| `policy.item.R1` | `policy.item.can-flags`, `policy.delete.denied`, `policy.delete.folders-not-matching`, `policy.delete.admin-without-flag` |
