# Subtitles: external files, embedded tracks, conversion and delivery

What a client sees of the subtitles of a video: which files beside it are its subtitles and how they are described, how embedded tracks are listed, which track plays by default, what the subtitle routes serve (formats, offsets, playlists), how PlaybackInfo says a track is delivered, and what the management routes do. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/subtitles.json`](../scenarios/subtitles.json) and [`../scenarios/subtitles2.json`](../scenarios/subtitles2.json) (sidecars by content, the route and delivery matrices). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `SubFiles`) and `scripts/conformance-subs2.sh` (lazy library `Subs2`). The upload and delete scenarios (tag `media-write`) have the server write beside the media, so the media tree must be writable for the server (`scripts/conformance-ref-up.sh` mounts it so). Rule IDs are stable (`subtitles.<topic>.R<n>`).

## Fixture facts this spec relies on

* `Sub Names (2300)` has sixteen subtitle files named in the ways of the rules below, a bitmap one (`.ru.sup`) and four files that are not its subtitles (`Other.srt`, `Sub Names (2300) 2.srt`, `Sub Names (2300)-en.srt`, `.en.txt`, `.en.ttml`).
* `Sub Embedded (2301)` is a Matroska file with three embedded text tracks; `Sub Mov (2302)` an MP4 with a `mov_text` track; `Sub Mixed (2303)` the Matroska file with two external files.
* `Vob Pair (3900)` has a VobSub `.idx` and `.sub` pair; `Vob Lang (3901)` two pairs (`.en` and `.fr.forced`); `Vob IdxOnly (3902)` an index with no data file; `Vob SubOnly (3903)` a data file with no index; `Vob Multi (3904)` one index with two tracks (`en`, `fr`). `Mks Files (3905)` has three `.mks` files (a SubRip track flagged default, a PGS track, a text file that is not Matroska). `Sub Odd (3906)` has files whose content is not what their extension says (a SubRip `.vtt`, a MicroDVD `.sub`, an MPL2 `.sub`, a SubViewer `.sub`, a WebVTT `.ass`, SAMI in `.smi`, SubRip in `.sup`, an empty `.srt`, a `.srt` of plain words).
* `Sub Convert (2305)` holds one file of each text format the conversion reads (SubRip, WebVTT, ASS, SSA, SAMI), a non-ASCII one and a CRLF one; `Sub Upload (2306)` and `Sub None (2304)` have no subtitles.

## <a id="discover"></a>External subtitle files

- **subtitles.discover.R1** A file is a subtitle of a video when its name starts with the video's file name without extension (any case) followed by a dot, and ends with a subtitle extension (any case). Only files in the video's own folder count (not `subs/` or `subtitles/` sub folders, which are not scanned for them either). `Name.srt` is a subtitle of `Name.mp4`.
- **subtitles.discover.R2** Not subtitles of the video: another video's file (`Other.srt`), a name that continues without a dot (`Name 2.srt`, `Name-en.srt`), and files of other extensions (`.txt`, `.ttml`, `.nfo`, ...).
- **subtitles.discover.R3** The extension sets the codec: `srt` is `subrip`, `ass` and `ssa` are `ass`, `vtt` is `webvtt`, `smi` and `sami` are `sami`, `sup` is `PGSSUB`. A `.sup` file is a bitmap track (`IsTextSubtitleStream` false); every other one is text. The extension is only a hint: what the file holds decides ([`subtitles.sidecar.R1`](#sidecar)).
- **subtitles.discover.R4** The words between the video's name and the extension are split on dots. `forced` sets `IsForced`, `default` sets `IsDefault`, `sdh`, `cc` and `hi` set `IsHearingImpaired` (any case, any position, any combination). Other words are not flags.
- **subtitles.discover.R5** The first remaining word that names a language sets `Language`: a two or three letter code (`en`, `eng`, `fre`; stored as the three-letter code of the language, `fra`), a language name the library's culture list spells with no qualifier (`English`, `French`; not `Spanish` or `Dutch`, whose list names carry one), or a region form of a listed culture (`pt-BR`, `zh-CN`, stored lower-cased: `pt-br`). Words that are no language (`xx`, `pt_BR`, `por-BR`, `en-US`, `pob`) are the `Title`; several of them are joined with dots; a number is a title (`en.1`). With no language the stream has none.
- **subtitles.discover.R6** The streams are ordered by file name, case-insensitively, punctuation before digits before letters (`.2.en.srt` < `.Brazilian Portuguese.srt` < `.en.1.srt` < `.EN.SRT` < `.fr-CA.srt` < `.fr.srt` < `.pt_BR.srt` < `.pt-PT.srt` < `.srt`).
- **subtitles.discover.R7** External subtitle streams come first: they are numbered from 0 in that order, and the file's own streams follow with their indexes moved up by their number (`Index` is the position in `MediaStreams`, video and audio included). `MediaSources[].MediaStreams` and the item's own `MediaStreams` are the same list.
- **subtitles.discover.R8** An external stream has `IsExternal` true and `Path` the file; every subtitle stream (embedded ones too) has `SupportsExternalStream` true (a DVB track false, [`bitmap.md`](bitmap.md)) and `IsTextSubtitleStream` as R3; a video or audio stream has both false.
- **subtitles.discover.R9** `DisplayTitle` is the title (when there is one) followed by the attributes it does not already contain, joined with ` - `: the language's English name or `Undefined`, `Hearing Impaired`, `Default`, `Forced`, the upper-cased codec, `External`. Without a title they are joined alone (`Spanish - Default - SUBRIP - External`; a title `Commentary` gives `Commentary - Undefined - SUBRIP - External`; a title that contains the language name leaves it out).
- **subtitles.discover.R10** `Score` ranks a subtitle stream: 111222 for an external text stream, plus 10000 when forced, plus 1000 when default, minus 10 for a bitmap stream and minus 1 for an embedded one, minus 100 when the stream has no `SupportsExternalStream` (DVB); an embedded track that is neither default nor forced has no score, whatever its codec (a default embedded ASS track scores 112221).
- **subtitles.discover.R11** The list follows the folder: a file added or removed shows up with the next scan or refresh of the item (and at once after an upload or a deletion through the routes), with no change to the video.

## <a id="sidecar"></a>Sidecar files by content

- **subtitles.sidecar.R1** The streams of a subtitle file are what probing its content finds, not what its extension says: a SubRip text saved as `.vtt` is a `subrip` stream, WebVTT saved as `.ass` is `webvtt`, SubRip saved as `.sup` is `subrip`, an MPL2 `.sub` is `text`, a SubViewer `.sub` is `subviewer`, SAMI in a `.smi` is `sami`. There is no fallback to the extension: a file that holds no subtitle the probe can read (empty, plain words, a MicroDVD `.sub`) is not a stream. `TimeBase` is the stream's own (`1/1000` for SubRip, `1/100` for ASS, `1/1000000` for MPL2, `1/90000` for a VobSub payload).
- **subtitles.sidecar.R2** What the probe lacks is filled from the name (R4, R5 of discover): `Language` is the file's own language tag when it has one (a VobSub index spells it as written, `en` and `fr`, and an undetermined one is `und`, shown as `Undetermined` in `LocalizedLanguage` and `DisplayTitle`; the language of the name is not used then), else the name's; `Title` the file's, else the name's; a stream is `IsDefault` when the name says `default` or the file holds several streams and flags it, `IsForced` and `IsHearingImpaired` when the name or the file says so.
- **subtitles.sidecar.R3** A VobSub `.idx` is listed once per track of the index (codec `DVDSUB`, `Width`/`Height` the index's `size:`, `Path` the `.idx`), and only when a `.sub` of the same name stands beside it; that `.sub` is the payload of the pair and is not listed on its own. An `.idx` with no `.sub` is not a stream; a `.sub` with no `.idx` is a `DVDSUB` stream of its own without a size (`TimeBase` `1/90000`) when its content is a VobSub payload. Several tracks in one index are several streams (the first flagged default in the index is `IsDefault`).
- **subtitles.sidecar.R4** A Matroska subtitle file (`.mks`) gives one stream per subtitle track it holds, with the track's codec, language, title and flags (a PGS track is `PGSSUB` with its size, a SubRip track `subrip`). The name's flags and language apply as in R2.

## <a id="embedded"></a>Embedded tracks

- **subtitles.embedded.R1** The tracks of the media file are `Type` `Subtitle` streams with the file's `Codec` (`subrip`, `ass`, `mov_text`, `PGSSUB` for PGS, ...), `Language` (a language the file leaves undetermined is none), `Title`, `IsDefault` and `IsForced` from the container; `IsExternal` is false and there is no `Path`.
- **subtitles.embedded.R2** In a source with external files the embedded tracks keep their order after the video and audio streams, indexes moved up (R7).

## <a id="default"></a>The track that plays by default

- **subtitles.default.R1** `MediaSources[].DefaultSubtitleStreamIndex` is the stream the caller's `SubtitleMode` and `SubtitleLanguagePreference` select (absent for none): the streams are ranked external first, then default ones, then (with no language preference) the not forced ones or (with one) the forced ones, then the ones in a preferred language, then by index. `Default` takes the first external, forced or default stream; `Always` the first not forced one (with a preference: the first in a preferred language, forced or not, none otherwise); `OnlyForced` the first forced one (in a preferred language when one is set); `None` none; `Smart` is `Default` without a preference and with one the first in a preferred language, none when the audio that plays is in one.
- **subtitles.default.R2** `DefaultAudioStreamIndex` is the audio stream flagged default, else the first.
- **subtitles.default.R3** Both are set on the item's media sources and on PlaybackInfo's, for the caller.

## <a id="stream"></a>Serving a subtitle stream

`GET /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/Stream.{format}` and `.../Subtitles/{index}/{startPositionTicks}/Stream.{format}`.

- **subtitles.stream.R1** The formats are `srt` (`application/x-subrip`), `vtt` (`text/vtt`), `ass` and `ssa` (`text/x-ssa`), `json` (`application/json`) and `ttml` (`application/ttml+xml`), any case. The body is `Content-Length`-framed, with no trailing newline for WebVTT, JSON and TTML and with a blank line after every SubRip cue.
- **subtitles.stream.R2** A file asked for in its own format (`srt` for a `.srt`, `vtt` for a `.vtt`, `ass` for an `.ass` or `.ssa`, `ssa` for an `.ssa`) is served as it is: no conversion, offsets ignored (WebVTT still takes the time map header, R4).
- **subtitles.stream.R3** Otherwise the cues are parsed and written: HTML-like tags (`<i>`, `<b>`, `<u>`) are kept for WebVTT and SubRip and become override tags for ASS/SSA (`{\i1}`..`{\i0}`) and `<span tts:fontStyle="italic">` for TTML; line breaks are `\N` in ASS/SSA and `<br />` in TTML. From an `.ass` file the override tags are dropped for WebVTT and TTML and kept as written for SubRip and JSON; from an `.ssa` file they become the HTML-like tags everywhere. A SAMI file gives its text with `<br>` as a line break; an empty `&nbsp;` entry ends the previous cue. CRLF files and a UTF-8 BOM are read like LF ones.
- **subtitles.stream.R4** `startPositionTicks` (route or query, the query wins) moves every cue back by that many ticks unless `copyTimestamps` is `true`: a cue that then ends at or before zero is dropped, one that starts before zero starts at zero (a negative value moves cues forward); `endPositionTicks` drops the cues that start after it in the original timeline; `addVttTimeMap=true` adds `X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000` after the `WEBVTT` line.
- **subtitles.stream.R5** WebVTT text has `&` written `&amp;`. JSON is `{"TrackEvents":[{"Id":"1","Text":...,"StartPositionTicks":...,"EndPositionTicks":...}]}` with `Id` the cue's position from 1 as a string and the text encoded with `\uXXXX` (upper-case hex) for `"`, `&`, `'`, `<`, `>`, `+`, `` ` `` and everything outside ASCII, `\n`, `\r`, `\t`, `\\` as usual.
- **subtitles.stream.R6** An embedded text track is extracted with the media file's own tooling as SubRip (`subrip`, `mov_text`, ...), ASS (`ass`, `ssa`) or WebVTT and then handled like a file of that format (R2-R5); a track asked for in the format it is extracted in is served as extracted. Bitmap tracks (`PGSSUB`, `DVDSUB`) are not converted to text: they are burned into the video (delivery method `Encode`) or served as they are (`bitmap.route.R1`, [`bitmap.md`](bitmap.md)).
- **subtitles.stream.R7** The `Stream.` file name and the format are case-insensitive; the item id may be dashed or in any case; the media source id is the id of the item or of one of its versions (any case); `subrip` and `webvtt` name the SubRip and WebVTT writers, always converting, served as `application/octet-stream`.
- **subtitles.stream.R8** Failures: a format that is not served (`xyz`, `txt`, `m3u8`) `400` and `Stream` with no format `404`; an item id or index or start position that is not a number or GUID `400` (problem document naming `routeItemId`, `routeIndex`, `routeStartPositionTicks`); a query value that is not a number or boolean `400` (problem document naming the parameter); an item that does not exist (the zero GUID too) `400` with `Error processing request.`; a media source that is none of the item's `500`; an index no stream has, a negative one or one of a video or audio stream `500` (the reference's unhandled error, reproduced); a bitmap track `400`. Plain-text failures are `text/plain` with the body `Error processing request.`.
- **subtitles.stream.R9** **Divergence:** the reference serves these routes without any token. Tinyfin answers `401` to an anonymous request (the delivery URLs carry the caller's token as `ApiKey`). The scenario is skipped on the reference.

- **subtitles.stream.R10** The formats a text track (embedded or in a file) is served in are `srt`, `vtt`, `ass`, `ssa`, `json` and `ttml` (R1); `subrip` and `webvtt` always convert and are `application/octet-stream` (R7); `smi`, `sami`, `sub`, `idx`, `sup`, `mks` and unknown names are `400`. This holds for every text codec (`subrip`, `ass`, `mov_text`, `webvtt`, ...).
- **subtitles.stream.R11** A file is read by the parser of its extension. When that parser finds no cue (the content is another format) the file can only be asked for in the format of its own extension, which serves it as it is (`vtt` for the `.vtt` of SubRip text, `ass` for the `.ass` of WebVTT text, `sub` for a `.sub` as `text/vnd.dvb.subtitle`, `smi` for a SAMI file as `application/smil+xml`); every other format is `400`. A SAMI file is also served converted (`srt`, `vtt`, `ass`) and `sami` is `400`. A `.sup` file whose content is text is served as it is only for the format `pgssub` (`application/octet-stream`); `srt`, `vtt` and `sup` are `400`.
- **subtitles.stream.R12** A VobSub pair: `idx` serves the `.idx` file as it is (`application/octet-stream`); `srt` and `vtt` read the index as text, one cue per `timestamp:` line (the text of a cue is what follows the comma, the file position, and its end is zero); `sub`, `dvdsub`, `vobsub`, `mks`, `sup` and unknown names are `400`. A `.sub` with no index serves only `sub` (as it is, `text/vnd.dvb.subtitle`). A Matroska subtitle file is served like the track it holds: a text track in the formats of R10 (`mks` is `400`), a PGS track as `sup`, `pgssub` or `dvdsub` (`application/octet-stream`, [`bitmap.route.R1`](bitmap.md#route)) and every other format `400`.

## <a id="playlist"></a>The playlist of a subtitle stream

- **subtitles.playlist.R1** `GET .../Subtitles/{index}/subtitles.m3u8?segmentLength=N` answers `application/vnd.apple.mpegurl`: `#EXTM3U`, `#EXT-X-TARGETDURATION:N`, `#EXT-X-VERSION:3`, `#EXT-X-MEDIA-SEQUENCE:0`, `#EXT-X-PLAYLIST-TYPE:VOD`, then one `#EXTINF:<seconds>,` and `stream.vtt?CopyTimestamps=true&AddVttTimeMap=true&StartPositionTicks=<s>&EndPositionTicks=<e>&ApiKey=<token>` pair per N seconds of the source's run time (the last one shorter; seconds written without trailing zeros), then `#EXT-X-ENDLIST`. The index is not looked up.
- **subtitles.playlist.R2** `segmentLength` missing is `400` (problem document naming `segmentLength`), not a number `400` (problem document), zero or negative `400` with `Error processing request.`; a caller without a token `401`.

## <a id="delivery"></a>Delivery in PlaybackInfo

- **subtitles.delivery.R1** Only a request that carries a device profile (the POST body) gets `DeliveryMethod` on its subtitle streams; without one, none is set.
- **subtitles.delivery.R2** The profile's `SubtitleProfiles` are tried in order per stream, skipping an entry whose `Language` (one code) is not the stream's. `External`: a text stream that is in the entry's format or converts to it. `Hls`: the same. `Embed`: an embedded track that fits the entry (R6). `Encode`: never taken by an entry; a stream no entry serves is `Encode`. A text stream other than ASS/SSA is offered in any format name but the SSA family and the picture names (R6); an ASS/SSA stream only in `ass` (an `ssa` entry does not take it); a picture stream only in the name of its own codec (`bitmap.delivery.R3`, `R5`).
- **subtitles.delivery.R3** A stream delivered as `External` has `DeliveryUrl` `/Videos/{item id, dashed}/{media source id}/Subtitles/{index}/0/Stream.{format}?ApiKey={token}` and `IsExternalUrl` false; the other methods have no `DeliveryUrl`.
- **subtitles.delivery.R4** When the stream selected by default (subtitles.default.R1) is delivered by `Encode` or `Hls` the source is transcoded and an embedded track cannot stay in the container: no stream is `Embed` then (it is the next entry's, or `Encode`).
- **subtitles.delivery.R5** An external file is never `Embed`.

- **subtitles.delivery.R6** The format of an entry is any name for a text stream: the reference does not check it against what it can write. `xyz`, `json`, `ttml`, `text`, `srt` and `subrip` all take a SubRip track for `External` (the URL ends in the name as the entry spells it, `Stream.xyz`), `Hls` and `Embed`. `External` refuses `ass`, `ssa`, `sub`, `sup`, `pgs`, `pgssub`, `vobsub`, `dvdsub`, `dvbsub` and `hdmv_pgs_subtitle` for a text stream; `Hls` and `Embed` refuse only `ass` and `ssa` (an embedded SubRip track is not embedded as ASS).
- **subtitles.delivery.R7** An `Hls` entry serves a text stream only when the source is transcoded; an `Embed` entry only when it is served as it is (R4). A stream that is neither embedded nor in a text format (a picture stream in a text entry) is `Encode`.

## <a id="manage"></a>Upload and delete

- **subtitles.manage.R1** `POST /Videos/{itemId}/Subtitles` with `{Language, Format, IsForced, IsHearingImpaired, Data}` (`Data` base64) saves the file beside the video as `<video>.<Language>[.forced][.sdh].<Format>`; when that name is taken `<video>.<Language>[...].<n>.<Format>` with `n` from 0 up to a free one. `204`. The item lists the new stream at once (the language as the library names it: `fre` becomes `fra`).
- **subtitles.manage.R2** `Language`, `Format` and `Data` are required (`400`, problem document naming each missing one, also with a bad item id); a format that is not a subtitle extension `400` with `Error processing request.`; an item id that is not a GUID `400` (problem document naming `itemId`); an item that does not exist `400` with `Error processing request.`.
- **subtitles.manage.R3** `DELETE /Videos/{itemId}/Subtitles/{index}` removes the file of that external stream: `204`, the streams after it move up. An index past the streams, negative, or of an embedded track is `400` with `Error processing request.`; an item id or index that is not a GUID or number `400` (problem document); an item that does not exist `400` with `Error processing request.`.
- **subtitles.manage.R4** Upload needs an administrator or the `EnableSubtitleManagement` policy (`403` otherwise, `401` anonymous); deleting needs an administrator.

## <a id="providers"></a>Providers and fonts

- **subtitles.providers.R1** With no subtitle provider installed: `GET /Items/{itemId}/RemoteSearch/Subtitles/{language}` answers `200` and `[]` (`404` for an item that does not exist, `400` problem for an id that is not a GUID, `isPerfectMatch` ignored); `POST /Items/{itemId}/RemoteSearch/Subtitles/{subtitleId}` answers `204` for any id (`404` for an unknown item); `GET /Providers/Subtitles/Subtitles/{subtitleId}` answers `500` with `Error processing request.` (the reference's unhandled error); all three need the subtitle management policy (`403`) and a token (`401`). `GET /FallbackFont/Fonts` answers `[]` and `GET /FallbackFont/Fonts/{name}` an empty `200`, for any authenticated caller.

## Known differences

Observed on the reference and deliberately not reproduced (not asserted by a scenario that runs against both):

* **Burned-in subtitles.** A subtitle delivered by `Encode` is burned into the picture and a default one that needs `Encode` or `Hls` makes the answer a transcode, as on the reference; see [`playback.md`](playback.md). HLS manifests carry no subtitle group.
* **Default style of generated ASS/SSA.** A document generated from a WebVTT file has its second cue on layer 1 on the reference; Tinyfin writes layer 0.
* **Embedded tracks and the extraction tool.** Embedded tracks are extracted with whatever `ffmpeg` Tinyfin is configured with, so ASS header details (font names, a byte order mark) follow that build.
* **Queries on the same route.** The reference caches converted documents without all parameters in the key: a request with `copyTimestamps` can be answered with the document of an earlier one with the same index and offsets. Tinyfin has no cache.
* **A dashed media source id.** The reference answers `500` for a `mediaSourceId` written with dashes (it compares the text); Tinyfin folds every id of a path to one spelling before routing (wire.id.R2) and serves it.
* **Text named `.mks`.** A file named `.mks` whose content is plain text (not Matroska) is served by the reference unevenly: some positions answered `200` and others `404` for the same content. Tinyfin answers `404` for it unless its content is ASS or a plain-text track; not asserted.
* **Content of another encoding.** The charset a burned-in external text file is read in (`bitmap.burnin.R5`) is detected by rules over the bytes; the reference's detector is statistical and disagrees on short or ambiguous samples (listed in `bitmap.md`).
* **Providers.** There is no subtitle provider plugin system: search lists nothing, nothing can be downloaded.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `subtitles.discover.R1`, `R2`, `R4`, `R5` | `subtitles.discover.names` |
| `subtitles.discover.R3`, `R6`, `R7`, `R8` | `subtitles.discover.codecs-and-paths` |
| `subtitles.discover.R7` | `subtitles.embedded.mixed` |
| `subtitles.discover.R9` | `subtitles.discover.display-title` |
| `subtitles.discover.R10` | `subtitles.discover.score` |
| `subtitles.discover.R11` | `subtitles.manage.upload-delete` |
| `subtitles.sidecar.R1`, `R2` | `subtitles2.sidecar.odd-content` |
| `subtitles.sidecar.R1`, `R2`, `R3` | `subtitles2.vobsub.scan` |
| `subtitles.sidecar.R1`, `R4` | `subtitles2.mks.scan` |
| `subtitles.embedded.R1` | `subtitles.embedded.streams` |
| `subtitles.embedded.R2` | `subtitles.embedded.mixed` |
| `subtitles.default.R1`, `R2` | `subtitles.default.modes` |
| `subtitles.default.R3` | `subtitles.delivery.no-profile` |
| `subtitles.stream.R1`, `R2`, `R3` | `subtitles.stream.formats`, `subtitles.stream.ass-and-sami` |
| `subtitles.stream.R3`, `R5` | `subtitles.stream.text-edge` |
| `subtitles.stream.R4` | `subtitles.stream.offsets` |
| `subtitles.stream.R6` | `subtitles.stream.embedded` |
| `subtitles.stream.R7` | `subtitles.stream.route-forms` |
| `subtitles.stream.R8` | `subtitles.stream.errors` |
| `subtitles.stream.R9` | `subtitles.stream.auth` |
| `subtitles.stream.R10` | `subtitles2.embedded.route`, `subtitles2.mks.route` |
| `subtitles.stream.R11` | `subtitles2.sidecar.odd-route`, `subtitles2.mks.route` |
| `subtitles.stream.R12` | `subtitles2.vobsub.route`, `subtitles2.mks.route` |
| `subtitles.playlist.R1`, `R2` | `subtitles.playlist.segments` |
| `subtitles.delivery.R1` | `subtitles.delivery.no-profile`, `subtitles.delivery.external` |
| `subtitles.delivery.R2`, `R3` | `subtitles.delivery.external` |
| `subtitles.delivery.R2`, `R4`, `R5` | `subtitles.delivery.embedded` |
| `subtitles.delivery.R2`, `R3`, `R6`, `R7` | `subtitles2.delivery.embedded-matrix` |
| `subtitles.delivery.R6`, `R7` | `subtitles2.delivery.bitmap-matrix` |
| `subtitles.delivery.R5`, `R6`, `R7` | `subtitles2.delivery.external-matrix` |
| `subtitles.manage.R1`, `R3` | `subtitles.manage.upload-delete` |
| `subtitles.manage.R2` | `subtitles.manage.errors` |
| `subtitles.manage.R4` | `subtitles.manage.policy` |
| `subtitles.providers.R1` | `subtitles.providers.empty` |
