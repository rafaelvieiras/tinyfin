# Subtitles: external files, embedded tracks, conversion and delivery

What a client sees of the subtitles of a video: which files beside it are its subtitles and how they are described, how embedded tracks are listed, which track plays by default, what the subtitle routes serve (formats, offsets, playlists), how PlaybackInfo says a track is delivered, and what the management routes do. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/subtitles.json`](../scenarios/subtitles.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `SubFiles`). The upload and delete scenarios (tag `media-write`) have the server write beside the media, so the media tree must be writable for the server (`scripts/conformance-ref-up.sh` mounts it so). Rule IDs are stable (`subtitles.<topic>.R<n>`).

## Fixture facts this spec relies on

* `Sub Names (2300)` has sixteen subtitle files named in the ways of the rules below, a bitmap one (`.ru.sup`) and four files that are not its subtitles (`Other.srt`, `Sub Names (2300) 2.srt`, `Sub Names (2300)-en.srt`, `.en.txt`, `.en.ttml`).
* `Sub Embedded (2301)` is a Matroska file with three embedded text tracks; `Sub Mov (2302)` an MP4 with a `mov_text` track; `Sub Mixed (2303)` the Matroska file with two external files.
* `Sub Convert (2305)` holds one file of each text format the conversion reads (SubRip, WebVTT, ASS, SSA, SAMI), a non-ASCII one and a CRLF one; `Sub Upload (2306)` and `Sub None (2304)` have no subtitles.

## <a id="discover"></a>External subtitle files

- **subtitles.discover.R1** A file is a subtitle of a video when its name starts with the video's file name without extension (any case) followed by a dot, and ends with a subtitle extension (any case). Only files in the video's own folder count (not `subs/` or `subtitles/` sub folders, which are not scanned for them either). `Name.srt` is a subtitle of `Name.mp4`.
- **subtitles.discover.R2** Not subtitles of the video: another video's file (`Other.srt`), a name that continues without a dot (`Name 2.srt`, `Name-en.srt`), and files of other extensions (`.txt`, `.ttml`, `.nfo`, ...).
- **subtitles.discover.R3** The extension sets the codec: `srt` is `subrip`, `ass` and `ssa` are `ass`, `vtt` is `webvtt`, `smi` and `sami` are `sami`, `sup` is `PGSSUB`. A `.sup` file is a bitmap track (`IsTextSubtitleStream` false); every other one is text. (`.sub`, `.idx` and the other extensions are not specified: the reference decides them by probing the content.)
- **subtitles.discover.R4** The words between the video's name and the extension are split on dots. `forced` sets `IsForced`, `default` sets `IsDefault`, `sdh`, `cc` and `hi` set `IsHearingImpaired` (any case, any position, any combination). Other words are not flags.
- **subtitles.discover.R5** The first remaining word that names a language sets `Language`: a two or three letter code (`en`, `eng`, `fre`; stored as the three-letter code of the language, `fra`), a language name the library's culture list spells with no qualifier (`English`, `French`; not `Spanish` or `Dutch`, whose list names carry one), or a region form of a listed culture (`pt-BR`, `zh-CN`, stored lower-cased: `pt-br`). Words that are no language (`xx`, `pt_BR`, `por-BR`, `en-US`, `pob`) are the `Title`; several of them are joined with dots; a number is a title (`en.1`). With no language the stream has none.
- **subtitles.discover.R6** The streams are ordered by file name, case-insensitively, punctuation before digits before letters (`.2.en.srt` < `.Brazilian Portuguese.srt` < `.en.1.srt` < `.EN.SRT` < `.fr-CA.srt` < `.fr.srt` < `.pt_BR.srt` < `.pt-PT.srt` < `.srt`).
- **subtitles.discover.R7** External subtitle streams come first: they are numbered from 0 in that order, and the file's own streams follow with their indexes moved up by their number (`Index` is the position in `MediaStreams`, video and audio included). `MediaSources[].MediaStreams` and the item's own `MediaStreams` are the same list.
- **subtitles.discover.R8** An external stream has `IsExternal` true and `Path` the file; every subtitle stream (embedded ones too) has `SupportsExternalStream` true (a DVB track false, [`bitmap.md`](bitmap.md)) and `IsTextSubtitleStream` as R3; a video or audio stream has both false.
- **subtitles.discover.R9** `DisplayTitle` is the title (when there is one) followed by the attributes it does not already contain, joined with ` - `: the language's English name or `Undefined`, `Hearing Impaired`, `Default`, `Forced`, the upper-cased codec, `External`. Without a title they are joined alone (`Spanish - Default - SUBRIP - External`; a title `Commentary` gives `Commentary - Undefined - SUBRIP - External`; a title that contains the language name leaves it out).
- **subtitles.discover.R10** `Score` ranks a subtitle stream: 111222 for an external text stream, plus 10000 when forced, plus 1000 when default, minus 10 for a bitmap stream and minus 1 for an embedded one, minus 100 when the stream has no `SupportsExternalStream` (DVB); an embedded track that is neither default nor forced has no score, whatever its codec (a default embedded ASS track scores 112221).
- **subtitles.discover.R11** The list follows the folder: a file added or removed shows up with the next scan or refresh of the item (and at once after an upload or a deletion through the routes), with no change to the video.

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

## <a id="playlist"></a>The playlist of a subtitle stream

- **subtitles.playlist.R1** `GET .../Subtitles/{index}/subtitles.m3u8?segmentLength=N` answers `application/vnd.apple.mpegurl`: `#EXTM3U`, `#EXT-X-TARGETDURATION:N`, `#EXT-X-VERSION:3`, `#EXT-X-MEDIA-SEQUENCE:0`, `#EXT-X-PLAYLIST-TYPE:VOD`, then one `#EXTINF:<seconds>,` and `stream.vtt?CopyTimestamps=true&AddVttTimeMap=true&StartPositionTicks=<s>&EndPositionTicks=<e>&ApiKey=<token>` pair per N seconds of the source's run time (the last one shorter; seconds written without trailing zeros), then `#EXT-X-ENDLIST`. The index is not looked up.
- **subtitles.playlist.R2** `segmentLength` missing is `400` (problem document naming `segmentLength`), not a number `400` (problem document), zero or negative `400` with `Error processing request.`; a caller without a token `401`.

## <a id="delivery"></a>Delivery in PlaybackInfo

- **subtitles.delivery.R1** Only a request that carries a device profile (the POST body) gets `DeliveryMethod` on its subtitle streams; without one, none is set.
- **subtitles.delivery.R2** The profile's `SubtitleProfiles` are tried in order per stream, skipping an entry whose `Language` (one code) is not the stream's. `External`: a text stream that is in the entry's format or converts to it. `Hls`: the same. `Embed`: an embedded track whose format is the entry's. `Encode`: always. A stream no entry serves is `Encode`. Formats are `srt` (also `subrip`, `mov_text`), `vtt`, `ass`, `ssa`, `smi`; text streams other than ASS/SSA convert to `srt` and `vtt`, ASS only stays ASS (`ssa` entries do not take an `ass` stream), bitmap streams never convert.
- **subtitles.delivery.R3** A stream delivered as `External` has `DeliveryUrl` `/Videos/{item id, dashed}/{media source id}/Subtitles/{index}/0/Stream.{format}?ApiKey={token}` and `IsExternalUrl` false; the other methods have no `DeliveryUrl`.
- **subtitles.delivery.R4** When the stream selected by default (subtitles.default.R1) is delivered by `Encode` or `Hls` the source is transcoded and an embedded track cannot stay in the container: no stream is `Embed` then (it is the next entry's, or `Encode`).
- **subtitles.delivery.R5** An external file is never `Embed`.

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
* **Codec of a file with unexpected content.** The reference probes the content: a SubRip text saved as `.vtt` is `subrip` there; Tinyfin names the codec after the extension and does not read the files while scanning. A zero-length or unreadable file is not a stream on the reference and is one here.
* **`.sub` and `.idx`.** Decided by content on the reference (a MicroDVD `.sub` and a VobSub `.idx` were not listed, an MPL2 `.sub` was, as codec `text`); Tinyfin lists neither.
* **Default style of generated ASS/SSA.** A document generated from a WebVTT file has its second cue on layer 1 on the reference; Tinyfin writes layer 0.
* **Embedded tracks and the extraction tool.** Embedded tracks are extracted with whatever `ffmpeg` Tinyfin is configured with, so ASS header details (font names, a byte order mark) follow that build.
* **Queries on the same route.** The reference caches converted documents without all parameters in the key: a request with `copyTimestamps` can be answered with the document of an earlier one with the same index and offsets. Tinyfin has no cache.
* **A dashed media source id.** The reference answers `500` for a `mediaSourceId` written with dashes (it compares the text); Tinyfin folds every id of a path to one spelling before routing (wire.id.R2) and serves it.
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
| `subtitles.playlist.R1`, `R2` | `subtitles.playlist.segments` |
| `subtitles.delivery.R1` | `subtitles.delivery.no-profile`, `subtitles.delivery.external` |
| `subtitles.delivery.R2`, `R3` | `subtitles.delivery.external` |
| `subtitles.delivery.R2`, `R4`, `R5` | `subtitles.delivery.embedded` |
| `subtitles.manage.R1`, `R3` | `subtitles.manage.upload-delete` |
| `subtitles.manage.R2` | `subtitles.manage.errors` |
| `subtitles.manage.R4` | `subtitles.manage.policy` |
| `subtitles.providers.R1` | `subtitles.providers.empty` |
