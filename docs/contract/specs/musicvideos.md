# Music videos

The `musicvideos` library type: clips of songs, cataloged as `MusicVideo` items, with their artists, album and picture files. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/musicvideos.json`](../scenarios/musicvideos.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `MusicVideos`). Rule IDs are stable (`musicvideos.<topic>.R<n>`).

## Fixture facts this spec relies on

`MusicVideos/` holds 13 video files (2 to 3 s, h264/aac, 6 fps, 320x240) in these shapes:

* Loose in the root: `Plain Clip.mp4`, `Dua Lipa - Levitating (2020).mp4`.
* `Artist Nine/` (two clips and a picture): `Nine Song.mp4` with `Nine Song-thumb.jpg`; `Second Song.mkv` with `Second Song.jpg` and `Second Song.nfo` (title `Second Song Nfo`, artists `Nfo Artist` and `Guest Artist`, album `Nfo Album`, year 1999, genres `Synthpop` and `Dance`, a plot); `folder.jpg` (250x250).
* Folders that hold a single clip: `Tenth Artist/Album One/Deep Clip (2004).mp4` (with an nfo: title `Deep Clip`, artist `Tenth Artist`, year 2004), `Solo Folder (2010)/Solo Clip (2010).mp4`, `Nest/Inner/Deep.mp4` (with a 200x200 `folder.jpg` in `Inner`).
* `Mixed/One.mp4` and `Mixed/Sub/Two.mp4`; `Docs Dir/Main.mp4` beside `Docs Dir/Notes/readme.txt`.
* `Part Dir/Song - part1.mp4` and `Song - part2.mp4`; `Ver Dir/Song.mp4` and `Song - 4K.mp4`.

## <a id="scan"></a>Scan

- **musicvideos.scan.R1** Every video file of the library is one item of `Type` `MusicVideo`: `IsFolder` false, `MediaType` `Video`, `Container`, `RunTimeTicks`, `MediaStreams` and `MediaSources` read from the file (a `.mkv` is `mkv`). The library is cataloged wherever the file sits, whatever the folder names are; no folder name or file name is taken for an artist or an album.
- **musicvideos.scan.R2** A clip is named after its file without the extension (`Plain Clip`, `Song - 4K`), never after its folder; `SortName` is the lower-cased name.
- **musicvideos.scan.R3** A folder becomes a `Folder` item with the clips and folders it holds as children, except when it is a single-clip folder (rule R4). Folders with a dot-name, the usual system folders (`@eaDir`, `#recycle`, ...) and those a `.ignore` excludes are left out as in every library. Folders without any clip (`Docs Dir/Notes`) are listed. The library's root folder is itself a `Folder` item (shared by all library types).
- **musicvideos.scan.R4** A folder that directly holds exactly one clip (after stacking, R7) and no sub folder at all is not an item: the clip takes its place under the folder's parent (`Tenth Artist/Album One/Deep Clip` hangs off `Tenth Artist`, `Mixed/Sub/Two` off `Mixed`), and the folder's picture files apply to the clip (images.R3). A folder that holds one clip and any sub folder, even a folder of no media (`Docs Dir`), stays a folder, and so does a folder (`Nest`, `Tenth Artist`) whose only content are folders.
- **musicvideos.scan.R5** A `(YYYY)` at the end of the file name is the clip's `ProductionYear` (`Dua Lipa - Levitating (2020)` is 2020) and stays in the name; a file name without it has no `ProductionYear`. An nfo `<year>` wins (nfo.R1).
- **musicvideos.scan.R6** `Years` and `SortBy=ProductionYear` use that year; the year of a clip alone in `Solo Folder (2010)` is the one in its file name.
- **musicvideos.scan.R7** Files named `<name> - part1`, `<name> - part2` (probed with `part`; Tinyfin also stacks `cd`, `dvd`, `disc`, `disk` and `pt` as for movies) are the parts of one clip: the item is the first part, `PartCount` is the number of parts (2), the other parts are not items of their own. A lone `... - part1` is an ordinary clip.
- **musicvideos.scan.R8** Files that only share a name prefix (`Song`, `Song - 4K`) are separate clips (movies would group them as versions): each one has a single media source and no `PartCount`.
- **musicvideos.scan.R9** A library refresh catalogs the clips that appeared and forgets the ones whose file is gone (scan.forget.*); unchanged files are not probed again.

## <a id="nfo"></a>The nfo file

- **musicvideos.nfo.R1** `<name>.nfo` beside the clip, with a `<musicvideo>` root: `<title>` is the `Name`, each `<artist>` an entry of `Artists` (in order) and `ArtistItems` (`{Name, Id}`, the id of the artist item, musicvideos.artists.R1), `<album>` the `Album`, `<year>` the `ProductionYear`, each `<genre>` a `Genres` entry (and `GenreItems`), `<plot>` the `Overview`; `<premiered>`, `<tag>`, `<studio>` and the like apply as for movies. The nfo of the clip of a single-clip folder is read the same way.
- **musicvideos.nfo.R2** Without an nfo a clip has `Artists` and `ArtistItems` as empty lists, and no `Album`, `Overview` or `AlbumArtist`.

## <a id="images"></a>Pictures

- **musicvideos.images.R1** `<name>-thumb.jpg` (also `.png`) is the clip's `Thumb` image; it does not make a `Primary`.
- **musicvideos.images.R2** `<name>.jpg`, `<name>.png`, `<name>-poster.jpg` is the clip's `Primary` image (`PrimaryImageAspectRatio` that of the picture); the image routes serve both as `image/jpeg`.
- **musicvideos.images.R3** `folder.jpg` (and the usual folder picture names) of a folder is that folder's `Primary` image; when the folder has a single clip (R4) it is the clip's `Primary` instead. Clips that merely sit beside a `folder.jpg` do not take it.

## <a id="artists"></a>Artists and genres

- **musicvideos.artists.R1** The names in the clips' `Artists` are `MusicArtist` items: `GET /Artists` lists them in name order (`Guest Artist`, `Nfo Artist`, `Tenth Artist`; `ParentId` narrows to the library), `GET /Artists/AlbumArtists` lists none (a clip has no album artist), `GET /Artists/{name}` and `GET /Items/{id}` answer an artist, the same id as in `ArtistItems`.
- **musicvideos.artists.R2** An artist of clips has `MusicVideoCount` (its clips), `SongCount` 0, `AlbumCount` 0, `ChildCount` = `MusicVideoCount`, and `Genres` (name order) from its clips. `GET /Artists?Genres=` lists the artists whose clips carry the genre. `GET /Items?ArtistIds=<id>` and `Artists=<name>` (any of a clip's artists) list the clips.
- **musicvideos.genres.R1** The genres of clips are music genres: `GET /MusicGenres` lists them (`Type` `MusicGenre`, `ParentId` narrows to the library) and `GET /Genres` leaves them out, as it does for songs (music.genre.R3). `Genres=` filters clips by exact, case-insensitive tag value.

## <a id="routes"></a>Routes

- **musicvideos.filters.R1** `GET /Items` with `IncludeItemTypes=MusicVideo` lists the clips; `Recursive`, `ParentId` (a folder lists its children, the library its first level, clips and folders), `SearchTerm`, `Years`, `Genres`, `Artists`, `ArtistIds`, `SortBy` (`SortName`, `ProductionYear`), `Limit`/`StartIndex`, `MediaTypes=Video` and `Filters=IsFavorite|IsUnplayed` apply as for any item. `IncludeItemTypes=Video` does not match them (that type is the home-videos one).
- **musicvideos.filters.R2** Sorting by `ProductionYear,SortName` descending puts the clips without a year last.
- **musicvideos.latest.R1** `GET /Items/Latest` (with `ParentId` the library, or `IncludeItemTypes=MusicVideo`) lists the clips, newest first, as plain `MusicVideo` items (not grouped); `Limit` cuts the list.
- **musicvideos.counts.R1** `GET /Items/Counts` has `MusicVideoCount`, the number of clips the caller can see.
- **musicvideos.library.R1** A library of type `musicvideos` is a `CollectionFolder` view with `CollectionType` `musicvideos`, listed by `GET /UserViews` and `GET /Library/VirtualFolders`.
- **musicvideos.playback.R1** `POST /Items/{id}/PlaybackInfo` offers one media source (`Container` `mkv` or `mp4`) with `SupportsDirectPlay` true for a profile that accepts the container and codecs, and no `TranscodingUrl`.
- **musicvideos.playback.R2** `GET /Videos/{id}/stream?static=true` and `GET /Items/{id}/Download` send the file (`video/mp4`, `video/x-matroska`), ranges included.
- **musicvideos.playback.R3** A profile that cannot play the file gets `SupportsDirectPlay` false, `SupportsTranscoding` true and an HLS `TranscodingUrl` (`.../master.m3u8`), as for movies.
- **musicvideos.playstate.R1** Played, favorite and the play-state filters work on a clip as on a movie; the activity log says "is playing" for a video.

## Known differences

* **Extracted frames.** The reference gives a clip without a picture file a `Primary` image extracted from its video (images.md); Tinyfin leaves it without. Scenarios only assert pictures that come from files.
* **`ItemCount`.** On the reference `GET /Items/Counts` has `ItemCount` equal to the number of items of every type (clips, folders, movies...); Tinyfin keeps it at 0 (see the counts note in `internal/api/items_counts.go`). Not asserted.
* **Genre counts.** `GET /MusicGenres?Fields=ItemCounts` has `MusicVideoCount` per genre on the reference (1 here); Tinyfin sends 0 (the `ChildCount` total is right). Not asserted.
* **Search.** The reference also finds the artists of clips by `SearchTerm` (`/Items?SearchTerm=nfo` and `/Search/Hints` return `Nfo Artist`); Tinyfin lists only the clips. Not asserted.
* **Artist items.** The reference answers an artist of clips with `Path` under its metadata folder, `LocationType` `FileSystem`, `IsFolder` absent and the empty-list members; Tinyfin's has no path, `LocationType` `Virtual` and `IsFolder` true, as for the artists of tracks that have no folder. `PartCount` is sent by the reference in listings without `Fields`; Tinyfin sends it on a single read and when `Fields` names it.
* **Folder refresh.** Refreshing a single folder item or the real-time monitor re-walk the folder as in the home-videos library (a folder that now holds one clip stays a folder item until the next library scan). Not asserted.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `musicvideos.scan.R1`, `R2` | `musicvideos.scan.list` |
| `musicvideos.scan.R3`, `R4` | `musicvideos.scan.folders` |
| `musicvideos.scan.R5`, `R6` | `musicvideos.scan.names` |
| `musicvideos.scan.R7`, `R8` | `musicvideos.scan.stack` |
| `musicvideos.scan.R9` | `musicvideos.scan.incremental` |
| `musicvideos.nfo.R1` | `musicvideos.nfo.fields` |
| `musicvideos.nfo.R2` | `musicvideos.nfo.none` |
| `musicvideos.images.R1`, `R2`, `R3` | `musicvideos.images.local` |
| `musicvideos.artists.R1`, `R2` | `musicvideos.artists.listing` |
| `musicvideos.genres.R1` | `musicvideos.genres` |
| `musicvideos.filters.R1`, `R2` | `musicvideos.filters` |
| `musicvideos.latest.R1` | `musicvideos.latest` |
| `musicvideos.counts.R1` | `musicvideos.counts` |
| `musicvideos.library.R1` | `musicvideos.views` |
| `musicvideos.playback.R1`, `R2` | `musicvideos.playback.direct` |
| `musicvideos.playback.R3` | `musicvideos.playback.transcode` |
| `musicvideos.playstate.R1` | `musicvideos.playstate` |
