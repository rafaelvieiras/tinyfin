# Scan: what a library scan catalogs and forgets

How a library scan decides what is in the catalog: files and folders it leaves out, items it forgets when their file is gone, disc structures and images, and `.strm` files. Everything here was probed on the 12.2 reference with online metadata off; the examples are the fixture trees the scenarios use.

Scenarios: [`../scenarios/scan.json`](../scenarios/scan.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy libraries `Ignore`, `Discs`, `DiscIso`, `Strm`, `StrmShows`, `Forget`, `ForgetRoot`, `ForgetShows`). The scenarios of the `forget` group change the media tree: they need the runner to see it (`-media-local-root`, capability `fs:write`) and put every file back when they end. Rule IDs are stable (`scan.<topic>.R<n>`).

## <a id="ignore"></a>What a scan leaves out

- **scan.ignore.R1** A `.ignore` file that is empty, or holds nothing but white space (a byte order mark and blank lines included), excludes its folder: nothing in that folder or below it is cataloged, and the folder is not an item. In a library's root folder it empties the library.
- **scan.ignore.R2** A `.ignore` file with content holds one pattern per line, `gitignore`-like and case-insensitive: `*` (anything but a slash), `?`, `[abc]` and `[!abc]` classes, a leading `!` to include again what an earlier line excluded (the last matching line decides), a trailing `/` to match folders only, `#` lines and blank lines are skipped, trailing white space and carriage returns are ignored. A pattern is matched against the name of a file or folder, at any depth below the folder that holds the `.ignore`; a folder it matches is not walked at all. A file with only comments or blank lines excludes nothing, unless it has no characters but white space (scan.ignore.R1).
- **scan.ignore.R3** A pattern with a slash anywhere but at its end (`sub/*.mp4`, `/name`, a full path) never matches anything; only the prefix `**/` is accepted, and `**/name` is the same as `name`.
- **scan.ignore.R4** The rules that apply to a folder are those of the nearest `.ignore`: its own, else its closest ancestor's, ancestors above the library's root folder included. The files are not merged: a folder with a `.ignore` of its own does not inherit the patterns of the ones above it.
- **scan.ignore.R5** `.nomedia` files have no effect.
- **scan.ignore.R6** Folders whose name starts with a dot, and folders named (any case) `@eaDir`, `#recycle`, `@Recycle`, `$RECYCLE.BIN`, `lost+found`, `metadata`, `extrafanart`, `extrathumbs`, `System Volume Information`, `Subs` or `Thumbs.db`, are never cataloged, whatever the library type; folders such as `Backup`, `Trash`, `@tmp`, `#snapshot` or `Sub` are.
- **scan.ignore.R7** In a movie library, a folder with an extras name (`extras`, `trailers`, `featurettes`, `behind the scenes`, ... and `backdrops`, `sample`) directly under the library's root folder is an ordinary folder: a `Folder` item holding its videos as movies named after their files, never collapsed into a movie named after the folder. The videos of `sample` are not cataloged (the folder is). Below a movie's own folder the same names are that movie's extras (extras.find.*).

## <a id="forget"></a>What a scan forgets

- **scan.forget.R1** A library scan, and the refresh of a folder, drops the items whose file or folder is gone, with everything under them (children, extras) and whatever pointed at them. A folder that held several movies and now holds one is that movie: the `Folder` item goes, the surviving movie takes its place.
- **scan.forget.R2** A user's play state (played, play count, position, favorite, rating) is kept when an item is forgotten: the item has it again when its file comes back.
- **scan.forget.R3** A library folder that cannot be read, or that has no entry at all, is skipped by the scan: nothing under it is forgotten. A folder with entries, even if none of them is media, is scanned: the library is emptied of what it held.
- **scan.forget.R4** Refreshing a series, a season or a folder item re-walks it: files that appeared are cataloged, items whose file or folder is gone are forgotten (a season whose folder is gone included), the rest of the library is not looked at. A series and its season folders stay while their folders exist, even with no episode.
- **scan.forget.R5** A collection or playlist loses a forgotten member (its child count follows) and stays, even empty.

## <a id="disc"></a>Disc structures and images

- **scan.disc.R1** A folder that holds a `VIDEO_TS` folder with at least one `.VOB` file in it, a `BDMV` folder, or `VIDEO_TS.IFO` with `.VOB` files in the folder itself, is one movie: `Type` `Movie`, `IsFolder` false, `VideoType` `Dvd` or `BluRay`, `Path` the folder, named after the folder as written. Everything else in that folder (other videos, sub folders) is not cataloged. A folder that is the only content of its parent folder collapses into the movie like any movie folder (items.names.R12).
- **scan.disc.R2** A `VIDEO_TS` folder that has no `.VOB` file (only `.IFO` and `.BUP`) is not a disc folder: beside other files it is a movie of its own named `VIDEO_TS`, `VideoType` `Dvd`, `Path` that folder, and the parent stays a `Folder` item. A folder with `VIDEO_TS.IFO` in it and nothing else is a DVD movie named after the folder.
- **scan.disc.R3** A DVD movie is described from its VOB files: the main title set (the `VTS_nn` group of the most data, without its `_0` menu part) is read in order, the streams (a `Data` track `dvd_nav_packet` first, then video and audio) are the first file's, `RunTimeTicks` is the sum of the files' durations, `Container` is the container the file probes as (`mpeg`) and the media source's `Size` that of the first file. When the files cannot be read there are no streams, no duration, no size.
- **scan.disc.R4** A Blu-ray movie has no `Container` and no `Size`; its streams and duration come from the disc when it can be read as a disc (a stream-only folder is not one).
- **scan.disc.R5** A `.iso` or `.img` file is a movie, `VideoType` `Iso`, `Path` the file, `Size` its size, named like any movie file. `IsoType` is `Dvd` or `BluRay` when the image holds a `VIDEO_TS` or `BDMV` folder, absent otherwise. A readable DVD image has the streams and the duration of its video.
- **scan.disc.R6** The media source of a disc names itself after the item's file or folder and what is in it: `<name>/DVD`, `<name>/Bluray`; an image with an unknown disc `<name>/ISO`. It carries `VideoType`, `IsoType`, `Protocol` `File`, and every ordinary movie's media source `VideoType` `VideoFile`.
- **scan.disc.R7** An image is streamed as the file it is. A lone `.vob`, `.m2ts` or `.ts` file is an ordinary video.

## <a id="strm"></a>.strm files

- **scan.strm.R1** A `.strm` file is a video file of a movie, series or home-video library: an item of the usual type whose `Path` is the `.strm` file and `LocationType` `FileSystem`. Nothing is probed during a scan (no streams, no duration).
- **scan.strm.R2** When the first line of the file is an address (`http://`, `https://`, `rtmp://`, ...) the item's media source has that address as `Path`, `Protocol` `Http` (`Rtmp`, ...), `IsRemote` true; when it is a local path or the file is empty the source's `Path` stays the `.strm` file, `Protocol` `File`, `IsRemote` false. `Container` is `strm`, `Size` the size of the `.strm` file.
- **scan.strm.R3** Asking for the playback info of a `.strm` item always offers one source with `SupportsDirectPlay`, `SupportsDirectStream` and `SupportsTranscoding` true; for an address the media is probed at that moment and its streams and duration are kept on the item.
- **scan.strm.R4** The stream of an address is the remote body, proxied (`200`, the remote's `Content-Type` and `Content-Length`, `Accept-Ranges: none`); an address that cannot be read is `500` `text/plain`. The stream of a `.strm` that holds a local path or nothing is the `.strm` file itself, `application/octet-stream`.

## <a id="options"></a>Library options a scan applies

`LibraryOptions` has about forty members; they are stored and echoed back whole. These change what a scan catalogs:

- **scan.options.R1** `EnablePhotos` (default true): off, the pictures of a home-videos library are not cataloged, and a folder that holds only pictures is a plain `Folder` instead of a `PhotoAlbum`.
- **scan.options.R2** `SeasonZeroDisplayName` (default `Specials`) is the name of season 0, whatever its folder is called.
- **scan.options.R3** `EnableEmbeddedTitles` (default false): on, a video's name is the `title` tag of the file when it has one (an `.nfo` title still wins).

Members the reference accepts and does not apply to a scan here: `Enabled` (a disabled library disappears from the views and its items from listings), `EnableAutomaticSeriesGrouping`, `SaveLocalMetadata` and `MetadataSavers` (writing `.nfo` files), `EnableEmbeddedEpisodeInfos`, `EnableEmbeddedExtrasTitles`, `AutomaticRefreshIntervalDays`, `PreferredMetadataLanguage`, `MetadataCountryCode`, `TypeOptions`, chapter-image and trickplay extraction, subtitle, lyric and media-segment fetcher members. `DisabledLocalMetadataReaders` has no effect on the reference's scan of `.nfo` files either. `AutomaticallyAddToCollection` is covered by collections.md, `EnableRealtimeMonitor` below, `PathInfos` (several folders) by the library routes.

## <a id="monitor"></a>Real-time monitor

- **scan.monitor.R1** A library whose options say `EnableRealtimeMonitor` true is watched: a file or folder that appears, changes or goes away below one of its folders is cataloged or forgotten without any refresh, once the change has been quiet for a while (tens of seconds on the reference; 30 s here). A library created without the option (the default of a request body that does not carry it) is not watched.
- **scan.monitor.R2** Only the first-level folder of the library that changed is re-walked, as a refresh of it would; a change among the library folder's own files is a scan of the library.

## <a id="realtime-push"></a>Realtime push of library changes

A change to what the libraries hold is announced to the open `/socket` connections as
`{"MessageType":"LibraryChanged","Data":{...}}`. The web client's views refresh on it. Probed black-box against the reference: nothing is sent for a refresh that changed nothing; the message comes in batches.

- **scan.push.R1** A scan or refresh that catalogs an item sends a `LibraryChanged` whose `ItemsAdded` contains the item's id and whose `FoldersAddedTo` contains the folder it was added to. It is not sent at once: the first change after a quiet period starts a timer (30 s on both the reference and Tinyfin) and the message is sent when it fires.
 A rescan the realtime monitor starts counts as a scan: what it catalogs is pushed the same way, and a client that never asks for a refresh still hears about it (an explicit scan that follows finds nothing new and therefore sends nothing).
- **scan.push.R2** An item a scan forgets is in `ItemsRemoved` of the message, and `FoldersRemovedFrom` names the folder it left.
- **scan.push.R3** Changes that land before the timer fires share one message: two movies cataloged a few seconds apart arrive in the same `ItemsAdded`.
- **scan.push.R4** `Data` always carries the arrays `FoldersAddedTo`, `FoldersRemovedFrom`, `ItemsAdded`, `ItemsRemoved`, `ItemsUpdated` and `CollectionFolders` (the library folders, plus the playlists folder, whenever something was added or removed; otherwise empty) and `IsEmpty` `false`.
- **scan.push.R5** The message is sent to every user's sockets, each one told only what their libraries let them see: a user without access to a library gets none of its `ItemsAdded`, and no message at all when nothing in the batch is visible to them. `ItemsRemoved` is passed on to everyone (the items no longer exist to be checked).
- **scan.push.R6** A refresh of an item (`POST /Items/{itemId}/Refresh`) puts the item in `ItemsUpdated` of a message.
- **scan.push.R7** Creating a library puts its library folder id (the `ItemId` of `GET /Library/VirtualFolders`) and, once scanned, the items it catalogs in `ItemsAdded`, and the user root folder (`GET /Items/Root`) in `FoldersAddedTo` and `ItemsUpdated`.
- **scan.push.R8** Removing a library (as the web client does, with `refreshLibrary=true`) puts its items in `ItemsRemoved` (asserted only as an array) and the user root folder in `FoldersRemovedFrom` and `ItemsUpdated`.

## Known differences

- **Container names.** The reference gives an item's `Container` as the probed format name (`mov,mp4,m4a,3gp,3g2,mj2`) and its media source the file extension (`mp4`); Tinyfin gives the same value on both. Disc media sources follow the reference.
- **Image probing.** What `ffprobe` makes of placeholder bytes depends on the build (the reference's build reads zero bytes of a `.img` as a one-frame image); the scenarios do not assert it.
- **Blu-ray streams.** When the installed `ffprobe` cannot open a disc folder (`bluray:` support is a build option), Tinyfin falls back to the largest `.m2ts` file of `BDMV/STREAM` for the streams and the duration; the reference reads the disc's main playlist. A folder whose index files are placeholders but whose stream file is real therefore has streams here and none on the reference.
- **UDF images.** `IsoType` is read from the ISO 9660 directory; for an image without one (UDF only) it is looked up near the start of the file. Not verified against a real Blu-ray image.
- **Streaming a disc folder.** A disc folder has no single file to send. The reference answers `500` to the first request for it and `200` with no body afterwards; Tinyfin answers `200` with no body when the disc was read (it has streams) and `500` when it was not. Not asserted.
- **Watching.** The monitor uses inotify and exists on Linux only; other platforms ignore the option. The quiet period before a folder is re-walked is 30 s on Tinyfin and about 40 s on the reference.
- **Empty `.strm`.** The reference reports `Size` `0` for it, Tinyfin leaves the member out.
- **Updated items.** The reference puts items in `ItemsUpdated` that a refresh merely touched (a changed modification time re-saves every item of the folder); Tinyfin lists only an item whose own data changed and the item of an item refresh. Not asserted beyond `scan.push.R6`. A change of a file's modification time alone, the `POST /Items/{itemId}` metadata edit (not implemented) and the deletion of media through `DELETE /Items/{itemId}` (never done by Tinyfin) therefore push nothing.
- **Parental limits.** Whether a user may see an item is decided by the libraries they may access; a user's parental rating or tag limits do not narrow the message.
- **Library removal without a refresh.** With `refreshLibrary=false` the reference forgets the removed library's items only with the next library scan, so its message comes then; Tinyfin forgets them at once and announces them at once. Creating a library with `refreshLibrary=false` announces its library folder alone (the items follow with the first scan) on both.
