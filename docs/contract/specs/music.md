# Music libraries: scan, model and navigation (slices 1 and 2)

A library with `CollectionType` `music`: the tagged audio files of an `Artist/Album/NN - Title.ext` tree, the `Audio`, `MusicAlbum` and `MusicArtist` items they make, the artist listings, the album picture and the direct audio stream. Probed on the 12.2 reference with online metadata off (the container has no network).

Scenarios: [`../scenarios/music.json`](../scenarios/music.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `Music`). Rule IDs are stable (`music.<topic>.R<n>`).

Slice 1 is the scan, the model, the artist listings, pictures and the direct stream. Slice 2 ([genres](#genre), [artist lookup and pictures](#artist2), [nfo](#nfo), [disc folders and loose files](#layout), [lyrics](#lyrics)) was probed the same way, on a second lazy library `Music2` (scenarios: [`../scenarios/music2.json`](../scenarios/music2.json)). What both leave out is listed under [Not covered](#not-covered).

## Fixture facts this spec relies on

* `Artist One/Album Alpha (2019)/` holds `01 - First Song.mp3` (3 s) and `02 - Second Song.mp3` (2 s), both ID3 v2.3 tagged (`Album Alpha`, album artist and artist `Artist One`, track `n/2`, disc `1/1`, date `2019`, genre `Rock`), and a `cover.jpg` (300x300) beside them.
* `Artist One/Album Beta/01 - Opening.flac` (2 s) carries Vorbis comments: `Album Beta`, track `1`, disc `2`, date `2021`, genre `Jazz`.
* `Artist Two/Album Gamma/01 - Untagged Track.mp3` has no tags at all.
* `Artist Two/Album Delta/01 - Embedded Cover.mp3` has ID3 tags (no date, no genre) and a JPEG picture embedded in the file; the folder has no picture file.
* `Various Artists/Mix Tape/01 - Guest Spot.mp3` has artist `Guest Singer` and album artist `Various Artists`.

## <a id="scan"></a>Scan

- **music.scan.R1** Under a music library root, a folder that directly holds audio files is a `MusicAlbum`, a folder at the library root that holds none (it holds albums) is a `MusicArtist`, and each audio file is an `Audio` item. The chain is track -> album (`ParentId`) -> artist (`ParentId`) -> library root. A recursive listing narrowed to one of the three types returns those items: 6 `Audio`, 5 `MusicAlbum`, 3 `MusicArtist` folders in the fixture (see [music.artist.R3](#artist) for artists with no folder).
- **music.scan.R2** A track's `Name` is its `title` tag; a file with no title tag is named after the file without its extension (`01 - Untagged Track`).
- **music.scan.R3** `IndexNumber` is the track number and `ParentIndexNumber` the disc number (`1/2` reads as 1); both are absent when the tag is. `ProductionYear` and `PremiereDate` come from the date tag (a bare year is January 1st). `Genres` come from the genre tag.
- **music.scan.R4** An album is named after the `album` tag of its tracks, else after its folder (`Album Alpha`, not the folder `Album Alpha (2019)`; `Album Gamma` for the untagged one). It takes `AlbumArtist`, `ProductionYear`, `PremiereDate` and `Genres` from its tracks.
- **music.scan.R5** A track's `RunTimeTicks` is the file's duration (the exact value depends on the ffprobe build for MP3 files, so scenarios assert it only on the FLAC file); an album's `RunTimeTicks` and `CumulativeRunTimeTicks` are the sum over its tracks.
- **music.scan.R6** ID3 (MP3) and Vorbis comments (FLAC) are read; tag names are case-insensitive (`ALBUMARTIST`, `album_artist`).
- **music.scan.R7** A file with no tags has no `Album`, no `AlbumArtist` and empty `Artists`, `ArtistItems` and `AlbumArtists`, but its `AlbumId` is still its folder's album.

## <a id="track"></a>The track

- **music.track.R1** A track (`Type` `Audio`) has `MediaType` `Audio`, `IsFolder` false, `Album`, `AlbumId` (the album item), `AlbumArtist`, `AlbumArtists` (`[{Name, Id}]`), `Artists` (names) and `ArtistItems` (`[{Name, Id}]`), `Container` (the file extension), and one media source named after the file without its extension holding one `Audio` stream.
- **music.track.R2** `ArtistItems` and `AlbumArtists` ids are the ids of `MusicArtist` items: the artist folder when its name equals the name (case-insensitive), else the artist made from the name. The track artist and the album artist are separate members (`Guest Singer` vs `Various Artists`).
- **music.track.R3** `AlbumPrimaryImageTag` is the album's Primary image tag, when the album has a picture.

## <a id="album"></a>The album

- **music.album.R1** An album (`MusicAlbum`) has `IsFolder` true, `MediaType` `Unknown`, `ParentId` its artist folder, `AlbumArtist`, `AlbumArtists`, `Artists`, `ArtistItems` and `Genres`.
- **music.album.R2** `ChildCount` and `RecursiveItemCount` are the number of its tracks.
- **music.album.R3** `ImageTags.Primary` is present when the album has a picture (rules under [images](#image)).
- **music.album.R4** The children of an album (`ParentId`) are its tracks, in disc then track order (`SortBy=SortName`).

## <a id="artist"></a>The artist

- **music.artist.R1** An artist (`MusicArtist`) has `IsFolder` true, `MediaType` `Unknown`, `SongCount` (tracks whose `Artists` or `AlbumArtist` name it), `AlbumCount` (albums whose `Artists` or `AlbumArtist` name it) and `ChildCount` = `SongCount` + `AlbumCount`. Untagged files count for nobody.
- **music.artist.R2** The children of an artist folder (`ParentId`) are its album folders.
- **music.artist.R3** A name that tags give to a track but that has no folder in the library is still an artist: it is listed by `/Artists`, can be read by id, counts its songs and albums, and has no `ParentId`. The reference creates such an artist item the first time it is listed or read; from then on it appears in a recursive `IncludeItemTypes=MusicArtist` listing too (before that the listing holds the folder artists only). Tinyfin makes it at scan time, so it always does.

## <a id="list"></a>Listings and filters

- **music.list.R1** `GET /Items` with `IncludeItemTypes` `Audio`, `MusicAlbum` or `MusicArtist` (`Recursive=true`, optionally `ParentId` the library) lists those items.
- **music.list.R2** `GET /Artists` answers `{Items, TotalRecordCount, StartIndex}` of `MusicArtist` items that appear in the `Artists` of some track, in name order. It takes the item query parameters (`SearchTerm`, `Limit`, `StartIndex`, `Fields`, `userId`).
- **music.list.R3** `GET /Artists/AlbumArtists` lists the artists that are the `AlbumArtist` of some album or track (the fixture: `Artist One`, `Artist Two`, `Various Artists`; `Guest Singer` is a track artist only, `Various Artists` an album artist only).
- **music.list.R4** `/Items` takes `ArtistIds` (items whose artists, track or album, include that artist's name), `AlbumArtistIds` (items whose album artist is that artist) and `AlbumIds` (the tracks of those albums).

## <a id="image"></a>Pictures

- **music.image.R1** A `cover`, `folder` or `poster` picture (`.jpg` or `.png`) beside the tracks (`front.jpg` is not one) is the album's Primary image (`GET /Items/{albumId}/Images/Primary` is the file, `Content-Type` `image/jpeg`).
- **music.image.R2** A picture embedded in an audio file is that track's Primary image, and the album's when the folder has no picture file.
- **music.image.R3** A track or album with neither has no Primary image: `GET .../Images/Primary` is `404` and `ImageTags` is empty. A track whose album has a picture still has no `ImageTags.Primary` of its own, only `AlbumPrimaryImageTag`.

## <a id="stream"></a>Playback

- **music.stream.R1** `GET /Audio/{itemId}/stream?static=true` (and `stream.{container}`) is the file as it is: `200`, the file's content type (`audio/mpeg` for MP3, `audio/flac` for FLAC), `Accept-Ranges: bytes`.
- **music.stream.R2** A `Range` request is answered `206` with `Content-Range`.
- **music.stream.R3** `POST /Items/{itemId}/PlaybackInfo` for a track with no device profile offers the file as it is: one media source with `SupportsDirectPlay` and `SupportsDirectStream` true, `Container` the file's, `Id` the item's.
- **music.stream.R4** An unknown item is `404`; an item that is not a track (an album) is `400`.

## Slice 2 fixture facts

* `Music2/Artist Three/` holds `artist.nfo` (biography, genres `Pop` and `Dance`, a MusicBrainz artist id), `folder.jpg` (400x400) and `backdrop.jpg`; its `Album Epsilon/` holds five MP3 tracks (genre `Pop`, one `Pop;Rock`), `album.nfo` (title `Epsilon Deluxe`, review, year, genres `Pop`/`Electronic`, MusicBrainz album and release-group ids), `01 - Synced Words.lrc`, `02 - Plain Words.txt`, `04 - Edge Lyrics.lrc`, and a track with a `lyrics` tag in the file (`05`).
* `Music2/Artist Four/` holds `artist.jpg`, `fanart.jpg`, a loose track `Loose In Artist.mp3` and `Album Zeta/` with `CD1/` and `CD2/` disc folders (one track each, no disc tag; the CD1 track has genre `Funk`) and a `folder.jpg`.
* `Music2/Artist Five/` has only `CD1/` and `CD2/` (tracks tagged album `Discs Only`), `Music2/Flat Album/` one track, and `Music2/Loose At Root.mp3` (artist `Root Singer`, album `Root Album`).

## <a id="genre"></a>Music genres

- **music.genre.R1** `GET /MusicGenres` answers `{Items, TotalRecordCount, StartIndex}` of items of `Type` `MusicGenre`: the distinct `Genres` of the audio, albums and artists the caller can see, in name order (`SortOrder` is ignored by the reference; `StartIndex`, `Limit`, `SearchTerm`, `ParentId` and the other item query parameters apply). A genre is one tag value: `Pop;Rock` is a genre of its own, a tag is not split on `;` (same for the artist tag).
- **music.genre.R2** With `Fields=ItemCounts` each item has `SongCount` (tracks), `AlbumCount`, `ArtistCount` and `ChildCount` (their sum); without it none of them is sent. Artists count through the genres of their `artist.nfo`, or of their tracks when they have none.
- **music.genre.R3** `GET /Genres` with no `ParentId` leaves the music items out (the fixture's music genres are not in it); with a music library as `ParentId` it lists that library's genres, typed `MusicGenre`.
- **music.genre.R4** `GET /MusicGenres/{name}` answers the genre item (`Type` `MusicGenre`), the name matching without regard to case.
- **music.genre.R5** `/Items` takes `Genres` (`|`-separated values, exact tag value, case-insensitive) and `GenreIds` for `Audio`, `MusicAlbum` and `MusicArtist` alike.

## <a id="artist2"></a>Artist lookup and artist pictures

- **music.artist.R4** `GET /Artists/{name}` answers the artist item of that name as `/Items/{id}` does (`SongCount`, `AlbumCount`, `ChildCount`, `Genres`, `Overview`...), the name matching without regard to case; an artist known only from track tags (`Root Singer`) answers too. `Genres` come from `artist.nfo` when it names some, else from the artist's tracks. Anonymous is `401`.
- **music.artist.R5** `SongCount` counts the loose tracks of an artist folder (and the tracks of the albums it holds); `AlbumCount` counts album items only (a loose track's album tag makes no album).
- **music.image.R4** The artist folder's `folder.jpg`, `poster.jpg` or `<folder name>.jpg` is the artist's Primary image, and `backdrop.jpg`/`fanart.jpg`/`background.jpg` a Backdrop (`BackdropImageTags`, `GET .../Images/Backdrop/0`). `artist.jpg` is not a picture of the artist.
- **music.image.R5** A track (and an album) takes the backdrop of its artist: `ParentBackdropItemId` is the artist, `ParentBackdropImageTags` its tags.

## <a id="nfo"></a>nfo files

- **music.nfo.R1** `artist.nfo` in the artist folder: `<biography>` is the `Overview`, `<genre>` entries the `Genres`, `<musicbrainzartistid>` is `ProviderIds.MusicBrainzArtist`; the artist keeps its folder name. `ExternalUrls` lists `MusicBrainz Artist` (`https://musicbrainz.org/artist/{id}`).
- **music.nfo.R2** `album.nfo` in the album folder: `<review>` is the `Overview`, `<year>` the `ProductionYear`, `<musicbrainzalbumid>` and `<musicbrainzreleasegroupid>` are `ProviderIds.MusicBrainzAlbum` and `.MusicBrainzReleaseGroup` (`ExternalUrls`: `MusicBrainz Album` `release/{id}`, `MusicBrainz Release Group` `release-group/{id}`). `<title>` and `<genre>` also apply, but the reference only takes them on a later pass (a fresh scan names the album after the tags and lists the tracks' genres; a refresh switches to the nfo's): scenarios assert neither. Tinyfin applies them at the first scan.

## <a id="layout"></a>Disc folders and loose files

- **music.disc.R1** A folder of tracks inside an album folder named like a disc (`CD1`, `Disc 2`, `disk_3`) does not make an album of its own: its tracks are tracks of the album that holds the folders (`AlbumId`, `ChildCount` and `RecursiveItemCount` count them, `ParentId=<album>` lists them, in `SortName` order).
- **music.disc.R2** A folder that holds only disc folders is itself the album (named after the tags: `Discs Only`, not the folder `Artist Five`), as a child of the library root, and the artist from the tags is made from the name.
- **music.disc.R3** A folder with audio files and no other folder with audio is an album at any depth, also directly under the library root (`Flat Album`).
- **music.loose.R1** A folder at the library root that holds albums is an artist even when it also holds audio files (`Artist Four`): the loose file is a track whose `ParentId` is the artist and that has `Album` from its tags but no `AlbumId`; the artist's children (`ParentId`) are that track and the albums.
- **music.loose.R2** A loose file at the library root is a track of the library root, `Album` from the tags, no `AlbumId`; its artist exists as an artist item.
- **music.loose.R3** An album folder directly under the library root has the library root as `ParentId` and no artist folder; its tracks name it by `AlbumId`.

## <a id="lyrics"></a>Lyrics

- **music.lyrics.R1** A lyrics file beside the track, named like it with the extension `.lrc` or `.txt`, makes `HasLyrics` true, and `GET /Audio/{itemId}/Lyrics` answers `{Metadata: {}, Lyrics: [...]}`. The item's `MediaStreams` gain a stream `Type` `Lyric`, `Index` after the audio stream, with the file as `Path`.
- **music.lyrics.R2** A timed (`.lrc`) document lists one line per leading `[mm:ss.xx]` stamp (`Text`, `Start` in ticks, `Cues` `[]`), in time order (equal stamps keep file order); an empty text after a stamp is a line with `Text` `""`. Lines with no stamp (`[ti:..]`, `[offset:..]`, untimed text) are dropped; the `offset` tag is not applied; `Metadata` stays `{}`.
- **music.lyrics.R3** A `.txt` document lists one line per text line (`Text` only, no `Start`, no `Cues`), including the empty one after a final newline.
- **music.lyrics.R4** `Start` is `(minutes * 60 + seconds) * 10^7` plus the fraction as ticks (`[00:02.50]` is `25000000`).
- **music.lyrics.R5** A track with no lyrics file is `404` (`HasLyrics` false), also when the audio file carries a lyrics tag: embedded lyrics are not read. An unknown item is `404`, anonymous `401`.

## Slice 2 differences

* **Disc folders.** The reference also makes `Folder` items of `CD1`/`CD2` (children of the album, parents of the tracks, listed by a recursive `ParentId=<album>` listing, and `AlbumIds=<album>` lists them instead of the tracks). Tinyfin has no such items: the tracks' `ParentId` is the album.
* **Unknown artist name.** `GET /Artists/{name}` for a name nothing carries creates and answers an artist on the reference; Tinyfin answers `404`.
* **Genre ids.** A `MusicGenre` has the id of the genre of the same name (the reference uses another id space for music genres); `GenreIds` accepts it.
* **Lyrics files.** Only `.lrc` and `.txt` are read, `.lrc` first. Writing (`POST`/`DELETE /Audio/{id}/Lyrics`, remote search) is not implemented.
* **Genres of an album after a refresh** follow the nfo only once the reference refreshed twice; see [music.nfo.R2](#nfo).

## <a id="not-covered"></a>Not covered by either slice

* Instant Mix (`/Items/{id}/InstantMix`, `/Artists/InstantMix`, `/MusicGenres/{name}/InstantMix`: the reference answers a random order of the tracks sharing the seed's genre, `TotalRecordCount` the pool size, `Limit` cutting the page), `/Items/{id}/Similar` for music, music videos.
* Audio transcoding: without `static=true` the reference answers with an AAC transcode (`audio/aac`); Tinyfin serves the file as it is on every `/Audio/{id}/stream` request (`/Audio/{id}/universal` is not routed), and a profile whose Audio direct play entries do not list the file's container gets `NoCompatibleStream`.
* `People` on tracks and albums (the reference lists the artists as `Artist`/`AlbumArtist` people), MusicBrainz ids read from tags (only the nfo ones are), `ReplayGain`, `Folder` items for disc folders (see [slice 2 differences](#lyrics)), lyrics upload/delete/search, `.elrc`/`.srt` lyrics.
* Multi-valued tags are not split (`;` is part of a name, as on the reference).

## Known differences

* **`SortName`.** The reference keeps `0001 - 0001 - First Song` (disc, track, title) as the sort name of a track; Tinyfin stores the normalized sort key like for every other item (`0000000001  0000000001  first song`). The order of tracks in a listing is the same.
* **Track runtime.** ffprobe 4.4 (the one Tinyfin was built against here) reports a slightly longer duration for an MP3 file than the reference's ffprobe (`2.0375 s` vs `2.0 s`).
* **Path of an artist with no folder.** The reference reports a path under its metadata folder; Tinyfin has none (`Path` absent).
* **Empty members.** The reference always sends `Genres`, `Tags`, `Studios`, `Taglines`, `ProviderIds`, `LockedFields` and the other empty collections on a single read; Tinyfin follows its general rule for those members, not a music-specific one (`Artists`, `ArtistItems` and `AlbumArtists` are always sent on tracks and albums).
* **Artist `DateLastMediaAdded`** is always `0001-01-01T00:00:00.0000000Z`, as on the reference.
* **`SupportsTranscoding`** is false on a track's media source (no audio transcoding), true on the reference.
* **Items created by a concurrent scan.** A library removed while a scan runs is not written to by the rest of the pass.

## Rule -> scenarios

| Rule | Scenarios |
|---|---|
| `music.scan.R1` | `music.scan.library` |
| `music.scan.R2` | `music.track.id3`, `music.track.untagged` |
| `music.scan.R3` | `music.track.id3`, `music.track.vorbis` |
| `music.scan.R4`, `R5` | `music.album.detail` |
| `music.scan.R5`, `R6` | `music.track.vorbis` |
| `music.scan.R7` | `music.track.untagged` |
| `music.track.R1`, `R2`, `R3` | `music.track.id3`, `music.track.guest-artist` |
| `music.album.R1`, `R2`, `R3` | `music.album.detail` |
| `music.album.R4` | `music.album.tracks` |
| `music.artist.R1`, `R2` | `music.artist.detail` |
| `music.artist.R3` | `music.artists.list` |
| `music.list.R1` | `music.scan.library` |
| `music.list.R2`, `R3` | `music.artists.list` |
| `music.list.R4` | `music.album.tracks`, `music.list.artist-filter` |
| `music.image.R1`, `R2`, `R3` | `music.image.album` |
| `music.stream.R1`, `R2` | `music.stream.direct` |
| `music.stream.R3` | `music.playback.info` |
| `music.stream.R4` | `music.stream.unknown` |
| `music.genre.R1`, `R2`, `R3` | `music2.genres.list` |
| `music.genre.R4` | `music2.genres.single` |
| `music.genre.R5` | `music2.genres.filter` |
| `music.artist.R4`, `R5`, `music.nfo.R1` | `music2.artist.by-name` |
| `music.image.R4`, `R5` | `music2.artist.images` |
| `music.nfo.R2` | `music2.album.nfo` |
| `music.disc.R1`, `R2`, `R3` | `music2.disc.folders` |
| `music.loose.R1`, `R2`, `R3` | `music2.loose.files` |
| `music.lyrics.R1`, `R2`, `R4` | `music2.lyrics.timed` |
| `music.lyrics.R3` | `music2.lyrics.plain` |
| `music.lyrics.R5` | `music2.lyrics.none` |
