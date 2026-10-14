# Extras: trailers, special features, theme media, parts and versions

The files that belong to a movie, a series or a season without being items of the library's own catalog: local trailers, special features (featurettes, interviews, ...), theme songs and theme videos, the additional parts of a stacked movie and the other versions of a movie or episode. What the reference treats as one, what it calls it and how it exposes it was probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/extras.json`](../scenarios/extras.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy libraries `Extras` and `ExtraShows`). Rule IDs are stable (`extras.<topic>.R<n>`).

## Fixture facts this spec relies on

* The movie `Ex All (2220)` holds every kind of extra: `Ex All (2220)-trailer.mp4`, `trailers/Folder Trailer.mp4`, one video in each of `extras/`, `featurettes/`, `behind the scenes/`, `deleted scenes/`, `interviews/`, `scenes/`, `samples/`, `shorts/`, `clips/`, `other/`, a `-featurette` file, `theme.mp3`, `theme-music/Second Theme.mp3`, `backdrops/Loop.mp4`, and things that are none of them (`extras/sub/Deep.mp4`, `theme-music/not-audio.mp4`, `soundtrack.mp3`).
* An extra is a short video (or a tone for audio) copied from one file; its item is found through its owner, never through the library.

## <a id="find"></a>What is an extra

- **extras.find.R1** Local trailers of a movie (or series or season) are the videos of its `trailers` folder and its files named `<anything>-trailer`, `<anything>.trailer`, `<anything>_trailer` (any case) or exactly `trailer`. They are `Type` `Trailer`, `ExtraType` `Trailer`, listed by `GET /Items/{itemId}/LocalTrailers`. A file named `<name> trailer` (a space) is not one.
- **extras.find.R2** Special features are the videos of the folders `extras` and `extra` (`ExtraType` `Unknown`), `featurettes` (`Featurette`), `behind the scenes` (`BehindTheScenes`), `deleted scenes` (`DeletedScene`), `interviews` (`Interview`), `scenes` (`Scene`), `samples` (`Sample`), `shorts` (`Short`), `other` (`Unknown`) and `clips` (`Clip`), any case; `Type` `Video`. Only the plural forms count: videos in `featurette/`, `interview/`, `short/`, `others/`, `clip/`, `specials/`, `bonus/` are movies of an ordinary folder, and videos in `sample/` are not cataloged at all. Videos below a sub folder of an extras folder (`extras/sub/`) are not extras.
- **extras.find.R3** Files beside the owner are typed by their ending, joined by `-`: `-featurette` (`Featurette`), `-behindthescenes` (`BehindTheScenes`), `-deleted` and `-deletedscene` (`DeletedScene`), `-interview` (`Interview`), `-scene` (`Scene`), `-short` (`Short`), `-sample` and `_sample` (`Sample`), `-clip` (`Clip`), `-extra` and `-other` (`Unknown`). Plural and spaced endings (`-featurettes`, `-deleted scene`, `-behind the scenes`) are not extras: with the movie's name in front they are other versions of the movie (extras.versions.R1), with any other prefix other movies. `sample.mp4`, `Name.sample.mp4` and `Name sample.mp4` are cataloged nowhere (not extras either).
- **extras.find.R4** An extra's `Name`: a file in an extras folder is named like a loose movie file (items.names.R10) with a trailing ` trailer`/` sample` or a `-`, `.` or `_` ending word taken off (`Folder Trailer.mp4` is `Folder`, `Making Of.mp4` is `Making Of`, `Foo - Sample.mp4` is `Foo -`); a typed file whose prefix is empty or the movie's name takes the kind's default name (`Trailer`, `Featurette`, `Behind The Scenes`, `Deleted Scene`, `Interview`, `Scene`, `Short`, `Sample`, `Clip`, `Extra`), numbered ` 2`, ` 3` in path order when several share it; with another prefix it is that prefix (`x-trailer.mp4` is `x`). Extras inherit the owner's `ProductionYear`.
- **extras.find.R5** Lists are ordered by sort name: the lower-cased name for videos, the name as it is for audio (so `Second Theme` comes before `Theme Song`), ties by path.
- **extras.find.R6** Not extras: a file that starts with the movie's name followed by `.`/`_`/`-` and more (`Name.featurette.mp4`, `Name-featurettes.mp4`) is another version of the movie and is cataloged nowhere else; `Name featurette.mp4` is a movie.
- **extras.find.R7** Extras belong to a movie only when it has its folder: movies that share a folder have none, and the folder's trailers, extras folders, theme files and `-trailer` files are not cataloged at all (they appear nowhere).
- **extras.find.R8** A movie whose folder also holds a sub folder (items.names.R12) keeps what its extras folders and theme files hold, but loses everything, themes included, when a video that is an extra by name (`Name-trailer.mp4`) sits next to it.

## <a id="themes"></a>Theme media

- **extras.theme.R1** Theme songs are the audio files (`mp3`, `flac`, `m4a`, `ogg`, `wav`, `wma`, `aac`, `opus`) named `theme` or `theme<digits>` beside the owner (`Name` `Theme Song`, numbered when several) and the audio files of `theme-music/` (named after the file); not the videos of `theme-music/`, `theme-song.mp3`, `theme-1.mp3`, `themes.mp3`, `Name-theme.mp3`. `Type` `Audio`, `ExtraType` `ThemeSong`, `MediaType` `Audio`.
- **extras.theme.R2** Theme videos are the videos of `backdrops/` (named after the file; not its audio files or sub folders). `theme.mp4` beside the owner is a movie. `Type` `Video`, `ExtraType` `ThemeVideo`.
- **extras.theme.R3** `GET /Items/{itemId}/ThemeSongs` and `ThemeVideos` answer `{OwnerId, Items, TotalRecordCount, StartIndex}`. `OwnerId` is the item asked about; with `InheritFromParent=true` and none of its own, the closest ancestor that has some (episode, then season, then series) supplies them and `OwnerId` is that ancestor.
- **extras.theme.R4** `GET /Items/{itemId}/ThemeMedia` answers `ThemeVideosResult`, `ThemeSongsResult` and `SoundtrackSongsResult` with the same envelope; the last is always empty with `OwnerId` all zeros. `InheritFromParent` applies to each result on its own.

## <a id="item"></a>The extra as an item

- **extras.item.R1** An extra is in no listing: not `Items` with any filter (`IncludeItemTypes` `Trailer`, `Video` or `Audio` finds none), not a library's children or counts (`/Items/Counts` `TrailerCount` is 0), not a search result. `Items?Ids=<id>` and `GET /Items/{itemId}` return it.
- **extras.item.R2** Its `Type` is `Trailer`, `Audio` (theme songs) or `Video`; `ExtraType` as in the rules above; `IsFolder` false; `MediaType` `Video`/`Audio`; `Path` its file; `MediaSources` has one source whose `Id` is the item's; `LocalTrailerCount` and `SpecialFeatureCount` are 0; no `ParentId` that names a folder.
- **extras.counts.R1** `LocalTrailerCount` and `SpecialFeatureCount` of a movie, series or season are the lengths of its two lists. A single read (`GET /Items/{itemId}`) always has them; a listing has them only when `Fields` names them.

## <a id="routes"></a>Routes

- **extras.routes.R1** `GET /Items/{itemId}/LocalTrailers` and `/SpecialFeatures` answer `200` with a bare array of items (empty for an item without any, for a folder, for a library root); `GET /Items/{itemId}/ThemeSongs`, `/ThemeVideos`, `/ThemeMedia` the objects above; `GET /Videos/{itemId}/AdditionalParts` `{Items, TotalRecordCount, StartIndex}`. An id that is not a GUID is `400` (problem document naming `itemId`), an unknown item `404` (problem document), an anonymous caller `401`.
- **extras.routes.R2** The user-scoped forms `GET /Users/{userId}/Items/{itemId}/LocalTrailers` and `/SpecialFeatures` answer the same; an unknown `userId` is `404`. An optional `userId` query parameter on the plain forms is validated the same way.

## <a id="series"></a>Series and seasons

- **extras.series.R1** A series owns what its own folder holds (`trailers/`, `extras/`, ..., `theme.mp3`, `theme-music/`, `backdrops/`, `<series folder name>-featurette.mp4`); a season owns what its folder holds, including `-trailer`/`-featurette` files whose prefix is the name of an episode (named after that prefix, `Extra Show S01E01`); an episode owns none. Files in the series folder or a season folder that are extras by name are never episodes.

## <a id="parts"></a>Stacked parts and versions

- **extras.parts.R1** Files that differ only by a part marker (`- part1`, ` cd1`, `-disc1`, `.pt1`, a gap in the numbers is fine) are one movie: its file is the first part, its `PartCount` is the number of files and `GET /Videos/{itemId}/AdditionalParts` lists the others (`Type` `Video`, no `ExtraType`, named after the file's name) in part order. A movie that is not stacked has no `PartCount`.
- **extras.versions.R1** The other versions of a movie (a file that starts with the folder's name and then `-`, `.`, `_`, ` - ` and more) and of an episode (items.episodes.R11) are media sources of the one item: `MediaSources[0]` is its own file (named after the file), the others are named after what follows the shared name (`4K`) and carry their own ids; the stream route serves the file of a `MediaSourceId` that names one of them.
- **extras.audio.R1** Audio files in a movie library are not cataloged: `soundtrack.mp3` beside the movie, `Name.mp3`, `soundtrack/01.mp3` (the folder is a plain `Folder` with no children). The library holds no `Audio` item.

## Known differences

Observed on the reference and deliberately not reproduced (not asserted by a scenario that runs against both):

* **Extracted frames.** The reference gives every extra a `Primary` picture extracted from its video (`ImageTags.Primary`), and `ThemeSong` items have `PremiereDate` `0001-01-01`; Tinyfin's extras have no picture and no premiere date.
* **Order of the media sources of several versions.** The reference's order of the sources of a movie's versions is not the file name order; Tinyfin lists the movie's own file first, then the others by name.
* **Transcoding a version.** Versions are direct-play sources; asking for a transcode of one starts from the item's own file.
* **Extras of a folder that holds an extra by name and a sub folder.** Reproduced as extras.find.R8; the reason the reference attaches nothing in that shape is not known.
* **`ExtraTypes` parameter.** `GET /Items?ExtraTypes=` is accepted by the reference and returns everything it would without it; Tinyfin ignores it as well.
* **Sort names of extras.** The wire `SortName` of a trailer is its lower-cased name and of a theme song its name as it is; Tinyfin normalizes both like any other item.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `extras.find.R1` | `extras.local.trailers` |
| `extras.find.R2`, `R3`, `R5` | `extras.local.special-features` |
| `extras.find.R4` | `extras.local.trailers`, `extras.local.names-and-numbering` |
| `extras.find.R6` | `extras.local.not-extras` |
| `extras.find.R7` | `extras.local.unowned` |
| `extras.find.R8` | `extras.local.folder-shape` |
| `extras.theme.R1`, `R2`, `R4` | `extras.theme.songs-and-videos` |
| `extras.theme.R3` | `extras.theme.inherit` |
| `extras.item.R1`, `extras.audio.R1` | `extras.item.hidden-from-listings` |
| `extras.item.R2` | `extras.local.trailers` |
| `extras.counts.R1` | `extras.local.counts`, `extras.series.series-and-season` |
| `extras.routes.R1` | `extras.routes.errors`, `extras.local.trailers` |
| `extras.routes.R2` | `extras.routes.legacy-user-forms` |
| `extras.series.R1` | `extras.series.series-and-season`, `extras.theme.inherit` |
| `extras.parts.R1` | `extras.parts.stacked` |
| `extras.versions.R1` | `extras.versions.movie` |
