# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Known issues

- A copied HLS job that starts in the middle of a file does not carry the seek in its segment timestamps on a current
  ffmpeg (`playback.md` R9). The shift is the segment muxer's `-output_ts_offset`, an option that muxer honors up to
  ffmpeg 4 and drops from 7 on. Measured on one file, for a job that starts at 7.023 s: ffmpeg 4.4.2 writes
  17.023/23.023/31.023 s, jellyfin-ffmpeg 7.1.4 (the build the image bundles) writes 10.023/23.023/31.023 s, and
  jellyfin-ffmpeg 8.1.3 (the reference's) writes 17/31 s — the shift arrives, the 13 s cut is skipped. The HLS muxer
  honors the option on both builds, but it cuts by its own grid and, on a seek that lands exactly on a key frame,
  writes the file from the start on 7.1.4; the two muxers are not interchangeable without design work against the
  reference. `TestKeyFramesAndSeekedCopy` reports the measurement and skips where the build differs.

### Added

- A `DeviceId` that carries the caller's own account id is the same device without it (`devices.md` R11-R12,
  `devices.json`, 1 scenario): clients that append the account id to their device id after a sign-in (the Android app
  sends both spellings) no longer land in the store twice, and the next login collapses the entries, tokens and
  sessions a previous sign-in left under the suffixed id. **[intentional deviation]** the reference keeps the two
  spellings as two devices.
- A device is a per-device-and-user record (`devices.md` R1-R10, `devices.json`, 5 scenarios): `GET /Devices` has one
  entry per `DeviceId` and account (`admin.devices.R1`-`R3`), a login revokes only that account's earlier token on the
  device, and `userId` is validated but does not narrow the list. `GET`/`POST /Devices/Options` keep one custom name per
  `DeviceId` that shows on every user's entry and outlives `DELETE /Devices`; an authenticated request carrying `Device`
  and/or `Version` refreshes the name and version of its owner's entry; `DELETE /Devices?id=` removes every entry of
  the device and revokes every token issued to it (an unknown id is a `400`, a missing one a `204` no-op); deleting an
  account removes its entries and leaves other accounts' entries on the same device.
- The administration pages report what the reference reports (`admin.md` R5, `config.R6`-`R8`, `admin5.json`, 4
  scenarios): every entry of `GET /Library/MediaFolders` carries `Path` and `Etag`, `GET /System/Configuration` is
  readable by any signed-in user like the network section, and `HardwareAccelerationType` accepts the reference's names
  ignoring case and surrounding whitespace and reports the lower-case one. **[intentional deviation]** any other
  `HardwareAccelerationType` is a `400` that leaves the section unchanged, where the reference answers `500`.
- Library changes reach the open sockets (`scan.md#realtime-push` R1-R8, `scan_push.json`, `playstate.push.R5`-`R6`,
  `playstate_push.json`, 5 scenarios): a scan or refresh that catalogs, updates or forgets items, a library add or
  remove and an item refresh push a batched `LibraryChanged` (the first change starts a 30 s timer, later changes join
  the same message) carrying `ItemsAdded`, `ItemsRemoved`, `ItemsUpdated`, `FoldersAddedTo`, `FoldersRemovedFrom` and
  `CollectionFolders`; each user is told only what their libraries let them see, `ItemsRemoved` aside. The rating
  routes (`POST`/`DELETE /UserItems/{itemId}/Rating` and their legacy form) push a `UserDataChanged` with `Rating` and
  `Likes` like any other user-data write, and an item that sits directly in a library folder now lists that library
  folder as its parent entry, with the `UnplayedItemCount` and `PlayedPercentage` of the leaves it holds.
- Sessions survive a restart (`playstate.md#session` R20): the registry behind `GET /Sessions` writes each session
  through to the store on every update (a capability report, a playback report, a socket connection) and reloads it at
  startup, so a restart no longer empties the dashboard and a client keeps its session id. Live playback state and
  `TranscodingInfo` stay in memory — a restored session reports no now-playing item — and `DELETE /Devices` and account
  deletion remove the stored session with the entry. The reference keeps sessions in memory only.
- A transcode description follows the segment request that started the job (`playstate.md#session` R17-R19,
  `playstate3.json`, 2 scenarios): `TranscodingInfo.Container` is the extension of the requested segment file, spelled
  as it was asked for (`0.ts` gives `ts`, `0.TS` gives `TS`), whatever `SegmentContainer` says; a segment the running or
  finished job already produced, or the one it is writing, is served without restarting the job; and a segment served
  that way describes the stream again when the device has no description on record (which a stopped report leaves it
  without), while a description that is merely hidden stays hidden.
- A video extra gets a `Primary` picture taken from its video, like the item it hangs off (`images.md#frames` R3, 3
  scenarios): local trailers, special features of every kind (featurettes, behind the scenes, deleted scenes,
  interviews, scenes, shorts, samples, clips, unnamed extras), theme videos and the additional parts of a stacked
  movie, whether they hang off a movie, a series or a season; theme songs (audio) get none.
- The `charenc` of a text burn-in comes from an exact implementation of the reference's statistical charset detector
  (`bitmap.md#charset-detection` R1-R20: byte-order marks, coding state machines, CJK distribution and hiragana
  analysis, single-byte language models, Hebrew and Latin-1 probers, escape sequences, single-precision confidences)
  replacing the older heuristic; all 84 recorded reference answers are reproduced, short and ambiguous samples
  included (`TestDetectCharsetCorpus`).
- The media sources on an item (`GET /Items`, `/Items/{id}`, `/Users/{userId}/Items/{id}`) report `SupportsDirectStream`
  and `SupportsTranscoding` from the caller's playback flags like the reference (`playback.md#item-sources` R1,
  scenario `playback.item.stream-flags`): a track is always streamable and transcodable with
  `EnableAudioPlaybackTranscoding`, any other file streamable with `EnablePlaybackRemuxing` and transcodable with
  `EnableVideoPlaybackTranscoding`, whatever its container or path kind.
- `DELETE /Videos/ActiveEncodings` stops the encodes of a play session (`playback.md#encodings` R1-R2, scenarios
  `playback.encodings.delete`/`restart`): both `DeviceId` and `PlaySessionId` are required (`400` problem
  otherwise), the session is matched by `PlaySessionId` alone without regard to case, any signed-in user may call it,
  HLS jobs and progressive streams end and the next segment request starts a new encode. A transcode job is now
  named after the device, the play session and the parameters that change the output (another session never
  reuses a job).
- 10-bit pictures are held to a higher rate like the reference (`playback.md#hls` R17, scenario
  `playback.hls.rate-ten-bit`, fixtures from `scripts/conformance-rate10.sh`): the figure after the 2.5 / 2 step is
  multiplied by 1.5 up to a 4 Mb/s source, 1.2 up to 8, 1.1 up to 12 and 1.05 above, for the master `BANDWIDTH` and the
  encoder `-maxrate` alike (118 measured files, no exception). A target more efficient than the source (H.264 to HEVC)
  no longer lowers the figure.
- `scripts/conformance-media.sh` also builds a dual-layer Dolby Vision stream (base layer plus interleaved
  enhancement layer NAL units, record patched to profile 7 with the layer present) for the Matrix2 library.
- Chapter picture overlays and WebP follow the reference (`chapters.md#images` R15-R16, scenarios
  `chapters.images.count-digits`, `blur-box`, `blur-edges`, `crop-whitespace`, `webp-lossy`): the unplayed count
  uses the reference's measured digit bitmaps (24 px for one or two digits, 18 px from three) and circle mask;
  `blur` is Skia's box-filter blur on an opaque picture surrounded by nothing (edges lose alpha: dark in a JPEG,
  transparent in a PNG/lossless WebP; sigma capped at 532, two boxes of 1.5x window from window 256); WebP below
  quality 100 is a lossy VP8 key frame from a new pure-Go encoder, and a WebP quality outside 0-100 is an empty
  `200` body. `cropWhitespace` is proven a no-op for chapter pictures. The conformance runner gains
  `expect.image` (size and pixel asserts) and `hexprefix`/`hexcontains`/`bytes_lte`/`bytes_gte` text ops.
- Burn-in fallback fonts and plain-text `.mks`, wave 29 (`bitmap.md` R8, `subtitles.md` R13, `playback.md` R17,
  scenarios `subtitles4.*`, fixtures from `scripts/conformance-subs4.sh`): the Docker image ships DejaVu Sans and
  WenQuanYi Zen Hei with a `fonts.conf` (`deploy/fonts.conf`), the same fallback faces as the reference image
  (libass drew no text at all in the distroless image: no fontconfig configuration); a host without any gets a
  generated minimal configuration for the ffmpeg process and the first system font directory as `fontsdir`;
  decoded frames of a burned-in missing font with Latin, CJK, Cyrillic and Greek text are identical to the
  reference's. A file named `.mks` with plain-text content is served as the format it holds at every position
  (it answered `404` before; the reference never does). A copied sound is cut at the position asked for also when
  the picture is clamped to the start of a short file. The conformance runner gains the text assertions
  `minlen`/`maxlen` and the capability `ffmpeg:noise-bsf` (`-ffmpeg`, default `$TINYFIN_FFMPEG_PATH`; always
  there for `-reference`).
- HLS copy decisions and announced rates follow the reference (`playback.md#hls` R15-R16, scenario
  `playback.hls.copy-limits`): a picture is copied only when its profile, level, reference frames, bit depth
  and sample aspect also fit `h264-profile`/`hevc-profile`/`Profile`, `h264-level`/`hevc-level`/`Level`,
  `MaxRefFrames`, `MaxVideoBitDepth` and `RequireNonAnamorphic`, and `AllowVideoStreamCopy` is not false; a copied
  High 10 picture is written as constrained baseline, a copied 8-bit HEVC one as profile 1; the rate cap and
  the variant bandwidth use the reference's source-rate ladder instead of a flat ten times (a file without sound
  adds no sound bandwidth).
- A re-encoded HLS sound follows the reference (`playback.md#hls` R14, scenario `playback.hls.encoded-audio`):
  the channel count is the source's (five-or-more-channel sources are no longer folded to stereo by default),
  capped by `AudioChannels`/`MaxAudioChannels`/`TranscodingMaxAudioChannels`, three or four channels become
  two, MP3 stays stereo; rate 128 kb/s a channel (256 kb/s for three or four, 640 kb/s for six) lowered to
  `AudioBitrate`; a six-channel source folded to two gets `volume=2`; `libfdk_aac`, `libmp3lame`, `libopus`
  are used when the ffmpeg has them.
- Encoded HLS segments match the reference (`playback.md#hls` R10-R13, `playback10.json`, 5 new scenarios,
  fixtures from `scripts/conformance-hls3.sh`): an H.264 picture is encoded with the reference's own encoder
  settings (`veryfast` at `crf` 23 under a rate cap held to ten times the source's picture rate, light motion
  search, closed GOPs, BT.709 tags, High profile with B-frames unless the request pins a profile and level),
  so the first picture of a segment has the reference's two-frame decode lead and a segment is byte-identical
  to the reference's on the same encoder build; HEVC targets use `libx265` with the reference's settings. The
  scale is written with the reference's display-size expressions (width, height, both, or the source's size
  under a bit rate), `MaxHeight`/`Height` now limit the picture, and the 416 default width drops the height
  limit. `Profile`/`Level` are read besides `h264-profile`/`h264-level`, `hevc-profile` pins HEVC Main. A
  segment that opens less than 5 s before the end starts its picture 5 s before it (the copied sound is cut at
  the position asked for when ffmpeg can). Output carries no metadata or chapters (no language descriptor in
  the transport stream), and a transcode job is keyed by the size, bit rate and profile parameters too.

- Chapter pictures against the reference, round two (`chapters.md#images` R6, R15, R16, `chapters4.json`, 6 new
  scenarios): the size rules follow the reference to the pixel (width and height, then `maxHeight` and `maxWidth` in
  sequence, ties to the even number with the ratio worked out first, fill by a divisor rounded up, a negative fill
  cancels it, a side that rounds to nothing serves the stored still); `blur`, `percentPlayed`, `unplayedCount`,
  `foregroundLayer` and `backgroundColor` make the picture again and are drawn like the reference (the unplayed
  digits use a built-in font); `format=Webp` and an `Accept: image/webp` caller get a lossless WebP (`Vary: Accept`
  on every picture answer). `ImagePath` stays hidden on purpose.
- Probe and container parity, fifth pass (`playback.md#offer` R19-R24, `playback9.json`, 5 new scenarios, `Probe3` library from
  `scripts/conformance-probe3.sh`): the `Container` of a video item and of its listed sources is named after the probed format
  (Matroska told from WebM by its streams, `ts`, `asf`, `mpeg`, `ogg`, the MP4 family under one name; the listed source keeps the
  extension when it is one of the names), whatever the extension; MPEG-TS sources carry `Timestamp` (`None`, `Zero`, `Valid` from
  the 192-byte M2TS header); files are probed as deeply as the reference does (`-analyzeduration 200M -probesize 1G -threads 0`),
  which fixes the stream bit rates of transport streams; the bit depth follows the pixel format (9 to 16 bits, none for `gbrp`);
  `mlp` tracks are estimated like `truehd`; a transcode names `VideoCodecNotSupported` / `AudioCodecNotSupported` when no
  streaming transcoding profile produces the source codec (the open "reason lists with conditions" difference); `.3g2 .amv .asf
  .dvr-ms .f4v .m2t .m2v .mk3d .mpe .mts .mxf .nsv .ogm .qt .rm .rmvb .tp .wtv .xvid` files are scanned. `probeVersion` is 4, and
  the items table gains `format` and `ts_timestamp` (migration 40).
- Subtitle burn-in, third pass (`bitmap.md#burnin` R5-R7, `subtitles3.json`, scenarios from `scripts/conformance-subs3.sh`):
  the `charenc` of a burned-in external text file is detected for many more encodings (UTF-32 byte order marks, EUC-JP,
  EUC-KR, Big5, GB18030 told from Korean, the Cyrillic, Greek, Hebrew, Arabic and Thai code pages and the Latin ones, a
  short UTF-8 sample as the reference reads it), checked against a corpus of 84 samples the reference was observed to
  answer for; the attachments (fonts) of the input file become libass' fonts directory like the reference; an external
  picture sidecar (`.sup`, `.idx`/`.sub`, `.mks`) selected for burn-in is marked a picture track by the route, so its canvas
  is probed and it is read from the seek position too.

- Subtitle sidecars by content and the rest of the subtitle matrix (`subtitles.md#sidecar`, `subtitles2.json`,
  10 new scenarios, fixtures from `scripts/conformance-subs2.sh`): subtitle files are probed, not trusted by
  extension (a SubRip `.vtt` is `subrip`, an empty or unreadable file is no stream), VobSub `.idx`/`.sub` pairs
  are `DVDSUB` streams per index track, Matroska `.mks` files list their tracks, and a sidecar keeps its own
  language tag (`und` too). The subtitle routes serve sidecars by the reference's rules (`idx`, `sub`, `mks`,
  `sup`, `smi`, mismatched content, 400 for the formats that are not served). `SubtitleProfiles` take any
  format name for a text track, a bad `Method` is a 400, and the delivery follows the transcode/direct-play
  situation. Burn-in passes `-canvas_size`, scales picture subtitles to the output picture, reads sidecar
  pictures as a second input, orders the text chain as the reference (colour tags, scale, pixel format, then
  `subtitles`), extracts embedded text first and passes `charenc` for external files with a language.

- HLS seek and segment plan (`docs/contract/specs/playback.md#hls`, `playback5.json`, 3 new scenarios): a
  segment request starts the transcode at its own `runtimeTicks` (ffmpeg `-ss`, segments numbered from the
  requested one) instead of waiting for one that runs from the start of the file; a request further on or
  earlier restarts it, one past the end of the file is the last seconds of it (200, was 404). Segment
  timestamps are the source's plus 10 s like the reference. A copied picture in a Matroska/WebM file gets
  the reference's key frame plan in its media playlist (key frames read with ffprobe, cached in memory and
  on disk) and is cut there with the segment muxer; the playlists no longer start a transcode. A repeated
  query name reads as its first value on the HLS routes; the master `BANDWIDTH` counts the audio of three
  to five channels, an mp3 first choice and flac like the reference.
- Offer and stream parity, third pass (`playback.md#offer` R9-R12, `playback6.json`, 5 new scenarios): a stream bit rate the
  container does not state comes from the Matroska statistics tags (`BPS`, `NUMBER_OF_BYTES` over `DURATION`) or, for audio, the
  reference's per-codec estimate (AAC 192000/320000, lossless 480000 per channel, ...); media streams carry `BitDepth`,
  `VideoRange`/`VideoRangeType` (SDR, HDR10, HLG), `IsAnamorphic`, `AverageFrameRate`/`RealFrameRate`/`ReferenceFrameRate`,
  `ColorPrimaries`/`ColorSpace`/`ColorTransfer`, `IsAVC`/`NalLengthSize`, `CodecTag`, `AudioSpatialFormat` and the `Localized*`
  labels of audio; MP4-family sources carry the probe's format name (or the one the profile lists) and a Matroska `.strm` is
  always `mkv`; a `.strm` that cannot be probed is transcoded for a user with `ForceRemoteSourceTranscoding`. `probeVersion` is
  2, so the next incremental scan probes files again. The conformance media has a `Matrix` library (Matroska, HDR, 10-bit,
  anamorphic, a served Matroska `.strm`).
- Probe and offer parity, fourth pass (`playback.md#offer` R13-R18, `playback7.json`, 5 new scenarios): media streams carry
  Dolby Vision (`DvVersionMajor`/`DvVersionMinor`/`DvProfile`/`DvLevel`/`RpuPresentFlag`/`ElPresentFlag`/`BlPresentFlag`/
  `DvBlSignalCompatibilityId`, `VideoDoViTitle`), `Hdr10PlusPresentFlag` (read from the first frame) and the full
  `VideoRangeType` set (`DOVI*`, `HDR10Plus`); the derived video bit rate, the channel layout label, `Level`, `CodecTag`
  and `opus`/`vorbis` estimates above 2 channels follow the reference. The offer evaluates `Video` `CodecProfile` conditions
  (`VideoRangeType`, `Width`, `VideoBitDepth`, ...) with their transcode reasons, ignores the audio codec when no audio track is
  default, and names the source container after the probed format (`mkv,webm`, `ts`, `asf`, `mpeg`, `ogg`, 3gp in the MP4
  family). `probeVersion` is 3. `scripts/conformance-media.sh` builds a `Matrix2` library (about 100 items; needs docker, the
  jellyfin-ffmpeg image, `mkvmerge`, and downloads `dovi_tool` and `hdr10plus_tool`).
- Picture subtitles (`docs/contract/specs/bitmap.md`, `bitmap.json`, 11 new scenarios, real PGS/DVD/DVB fixtures
  from `scripts/conformance-media.sh` and `scripts/conformance-pgs.py`): picture tracks carry the subtitle
  canvas `Width`/`Height`, `SupportsExternalStream` is false for DVB and `Score` follows the reference (none for an
  embedded track that is neither default nor forced; one hundred lower without external support); an `External` profile
  entry naming the track's codec (`pgssub`, `dvdsub`, `dvbsub`) delivers it as a file and `Stream.{format}` serves a PGS
  track as a `.sup` stream and a DVD track as a Matroska file (DVB: `404`; text formats: `400`). The overlay burn-in is
  now verified with real tracks (`TestBitmapBurnIn_RealFFmpeg`) and against the reference's transcode.
- Chapter pictures (`chapters.md#images`, `chapters2.json`, 4 new scenarios): with
  `EnableChapterImageExtraction` on a library a still is taken at each chapter (15 s for a chapter that starts at 0)
  during the scan (`ExtractChapterImagesDuringLibraryScan`) or by the new `Extract Chapter Images` task, set as `ImageTag`
  on `Chapters` and served at `/Items/{id}/Images/Chapter/{index}`.
- HLS parity (`docs/contract/specs/playback.md#hls`, `playback3.json`, 5 new scenarios): the master
  playlist has the reference's variant line (`BANDWIDTH`, `CODECS`, `RESOLUTION`, `FRAME-RATE`) and, for
  `SubtitleMethod=Hls`, the `#EXT-X-MEDIA:TYPE=SUBTITLES` group pointing at `Subtitles/{n}/subtitles.m3u8`;
  the media playlist is a VOD list of `hls1/main/<n>.ts` segments with `runtimeTicks` and
  `actualSegmentLengthTicks`, laid out before the encode starts (key frames forced on the segment grid);
  `StartTimeTicks` is carried in the links and refused on segments; the failure codes of the three routes
  match; a direct-playable source is remuxed instead of `404`. The encoder follows the announced
  `h264-profile`/`h264-level`, `MaxWidth`, `VideoBitrate` and `AudioBitrate`.
- Offer parity, second pass (`playback.md#offer`, `policy.md`, `playback4.json`, 3 new scenarios): a `.strm` whose address is
  reachable goes through the profile (`Bitrate` is the probed rate of the address, `Container` the probe's format name);
  `TranscodingUrl` writes `EstimateContentLength`, `TranscodeSeekInfo=Bytes`, the entry's `EnableAudioVbrEncoding` (default
  true) and ends with `allowVideoStreamCopy=false` / `allowAudioStreamCopy=false` when the request forbids copying (also for
  `ForceRemoteSourceTranscoding`, whose progressive offer now has the reference's members and order); the progressive offer
  of a user limited to audio transcoding has the reference's members and order too; a transcoded source keeps its container;
  media sources always carry `MediaAttachments`, `Formats`, `RequiredHttpHeaders` and `UseMostCompatibleTranscodingProfile`.
  `BreakOnNonKeyFrames` is no longer written (the reference never writes it). The conformance media has a served `.strm`
  (`scripts/conformance-strm-serve.sh`).
- Scan probe version (`probeVersion`, migration `0039`): scan signatures record the probe revision that wrote
  them, so the incremental scan probes items scanned by an older revision again (stream bit rate and frame
  rate on items scanned before they were kept) without `-scan-force-probe`.
- PlaybackInfo offer parity (`docs/contract/specs/playback.md#offer`, `playback2.json`, 4 new scenarios): a
  transcoded source is never a direct stream (also for a container-only mismatch); a request without a
  `DeviceProfile` direct plays any container; `.strm` addresses go through the profile; an `http`
  transcoding entry gives a progressive `/videos/{id}/stream.<container>` that is served by a transcode;
  `TranscodingUrl` carries the reference's members in its order (lower case dashed path, `DeviceId`, bit
  rates, `Tag`, per-codec source limits, ...); video sources carry `Bitrate`. The scanner now records the
  stream bit rate and frame rate.
- Policy, second pass (`docs/contract/specs/policy.md`, `policy2.json`, 7 new scenarios): with only audio
  transcoding allowed a video gets a progressive `/Videos/{id}/stream` offer instead of HLS; a video the
  profile plays is also `SupportsDirectStream`, and without a profile `SupportsDirectStream` follows
  remuxing and `SupportsTranscoding` video transcoding; `RemoteClientBitrateLimit` caps remote callers
  and a profile/request `MaxStreamingBitrate` now ends direct play of a video (`ContainerBitrateExceedsLimit`);
  `ForceRemoteSourceTranscoding` for remote `.strm` sources. The stream and HLS routes no longer apply
  folder, tag or rating rules (as the reference), and `BlockedMediaFolders` has no effect. New `Tagged` fixture library.
- Playback flags of `UserPolicy` (`docs/contract/specs/policy.md`, `policy2.json`): PlaybackInfo offers no
  transcode (`SupportsTranscoding` false, no `TranscodingUrl`) to a user with video transcoding, audio
  transcoding and remuxing all off, and a track is offered as it is when `EnableAudioPlaybackTranscoding`
  is off; `EnableMediaPlayback` and the stream routes are not gated, as on the reference. 4 new scenarios.
- Music, fifth slice (`docs/contract/specs/music.md`, `music5.json`): a track's and an album's
  `People` (composer, album artist, artists; `Role` always sent), the credits as `/Persons`
  entries, `Person`/`PersonTypes`/`PersonIds` filters on them (an item id stands for the person
  of its name; `/Persons` ignores `IncludeItemTypes`); `GET /Items/Latest` lists tracks that sit
  in no album as themselves under an artist folder or when libraries of other kinds are around,
  and an album folder as the album; the by-ids Instant Mix routes require `id` (`400`).
- Music videos library (`musicvideos`, `docs/contract/specs/musicvideos.md`, `musicvideos.json`):
  every video file is a `MusicVideo` named after its file (year from a trailing `(YYYY)`), a
  folder with a single clip is that clip, `-partN` files stack into one clip (`PartCount`), other
  folders are `Folder` items; `<musicvideo>` `.nfo` files give title, artists, album, year,
  genres and plot; `<name>-thumb.jpg` is the Thumb, `<name>.jpg`/`-poster.jpg` and `folder.jpg`
  the Primary. The clips' artists are `MusicArtist` items (`/Artists`, `ArtistIds`, `Artists`,
  genre-filtered, with `MusicVideoCount`), their genres are music genres, `/Items/Counts` has
  `MusicVideoCount`, and the clips list, search, play, stream, transcode and carry play state
  like any video. 17 new scenarios (`musicvideos.*`) pass on the reference and on Tinyfin.
- Music, fourth slice (WebUI comparison, `docs/contract/specs/music.md`, `music4.json`):
  `GET /Items/Latest` groups tracks as albums (music library scope ignores the types, artists only
  when asked for, `GroupItems=false` lists tracks) and honors `ParentId` in any letter case; the
  `ContributingArtistIds` filter; `GET /Artists/{id}/Similar`, `/Albums/{id}/Similar` and
  similar tracks/albums/artists by shared genre; a track's `SortName` keeps the
  `disc - track - title` form.
- Music, third slice (`docs/contract/specs/music.md`): audio transcoding and Instant Mix.
  `GET /Audio/{id}/stream` without `static=true` encodes (AAC by default; MP3, AAC, M4A, Opus,
  FLAC, WAV by `Container`/`AudioCodec`/extension) and streams the result; `GET
  /Audio/{id}/universal` serves the file when its container is accepted and fits
  `MaxStreamingBitrate`, else an http encode or an HLS master/media playlist (`/Audio/{id}/
  master.m3u8`, `main.m3u8`); PlaybackInfo of a track decides direct play or an audio
  `TranscodingUrl` (reasons, `AudioBitrate`, `MaxStreamingBitrate`) from the device profile.
  `InstantMix` for items, songs, albums, artists, playlists and music genres (a random order of
  the tracks sharing a genre with the seed). 7 new scenarios (`music.mix.*`, `music.transcode.*`).
- Music, second slice (`docs/contract/specs/music.md`): `GET /MusicGenres`, `/MusicGenres/{name}`
  (with `ItemCounts`) and `Genres`/`GenreIds` filters for tracks, albums and artists; `GET
  /Artists/{name}`; artist pictures (`folder.jpg`/`poster.jpg` Primary, `backdrop.jpg`/`fanart.jpg`
  Backdrop, parent backdrop on tracks and albums); `artist.nfo` and `album.nfo` (overview, genres,
  year, MusicBrainz ids and their `ExternalUrls`); disc folders (`CD1`, `Disc 2`) as part of the
  album, albums directly under the library root, loose files at the root and in artist folders;
  lyrics (`.lrc`/`.txt` beside the track, `HasLyrics`, `Lyric` media stream, `GET
  /Audio/{id}/Lyrics`). Tag values are no longer split on `;`, as on the reference. `/Genres`
  leaves music items out. 11 new scenarios (`music2.*`).
- Content policy: `GET /Items/{itemId}/Download` serves the original file as an attachment
  (ranges, `Content-Disposition`) and enforces `EnableContentDownloading` (403, 400 for an
  administrator without it, 401 for an API key). `DELETE /Items/{itemId}` now follows
  `EnableContentDeletion` / `EnableContentDeletionFromFolders` with no administrator bypass (401
  without permission; a permitted caller is still refused with 403, files are never deleted) and
  `CanDelete`/`CanDownload` on items reflect the policy. The first-run and seeded administrators
  get `EnableContentDeletion`. Contract: `specs/policy.md`, 8 scenarios.
- Administration (`docs/contract/specs/admin.md`): `GET /ScheduledTasks/{taskId}`,
  `DELETE /ScheduledTasks/Running/{taskId}` and `POST /ScheduledTasks/{taskId}/Triggers`; the task list now
  has the reference's Ids and three real tasks (library scan, trickplay generation, activity-log
  cleanup) with progress, last result, persisted triggers and a scheduler that fires them. Logs
  (`GET /System/Logs`, `/System/Logs/Log`, daily files under `<data dir>/log`), snapshot backups
  (`GET /Backup`, `POST /Backup/Create`, `GET /Backup/Manifest`; restore validates and answers 501)
  and empty `/Plugins`, `/Packages`, `/Repositories` lists (no plugin system, by design).
- Music libraries (`CollectionType` `music`, first slice): a folder that holds audio files is a
  `MusicAlbum`, a root folder an artist (`MusicArtist`), each audio file an `Audio` item with its
  ID3/Vorbis tags (title, artist, album, album artist, track, disc, year, genre; the file and
  folder names when untagged), duration and stream info. The album picture is a `cover`/`folder`/
  `poster` file or a picture embedded in a track (extracted with ffmpeg). Artists the tags name
  without a folder are artists too. New: `GET /Artists`, `GET /Artists/AlbumArtists`,
  `ArtistIds`/`AlbumArtistIds`/`AlbumIds` filters on `/Items`, `GET /Audio/{id}/stream` (the file
  as it is, with ranges) and direct play decisions for tracks. `MediaType` of albums and artists is
  now `Unknown`. Contract: `specs/music.md`, 14 scenarios. Left out: Instant Mix, lyrics,
  MusicGenres, music videos, audio transcoding.
- Chapters: the scan reads the chapter marks of a file (`ffprobe -show_chapters`, stored per item,
  migration 0026 re-probes existing items once) and `Chapters` is returned on a single read and
  with `Fields=Chapters` (title, or `Chapter N` when the file gives none). No chapter pictures.
  `GET /MediaSegments/{itemId}` now validates the id (400/404) and `includeSegmentTypes` like the
  reference; the list is still always empty.
- Trickplay: libraries with `EnableTrickplayImageExtraction` get seek-thumbnail sprite sheets
  (320 px or the video's width, 10 s interval, 10x10 JPEG grids) made by ffmpeg in a background
  job after each scan (`ExtractTrickplayImagesDuringLibraryScan`), incremental and bounded
  (`-trickplay-concurrency` / `TINYFIN_TRICKPLAY_CONCURRENCY`, default 1), stored under the data
  directory or beside the media (`SaveTrickplayWithMedia`). Served by
  `GET /Videos/{itemId}/Trickplay/{width}/tiles.m3u8` and `/{index}.jpg` and listed in the
  `Trickplay` member of the item; `RegenerateTrickplay=true` on a refresh regenerates. Contract:
  `specs/trickplay.md`, 10 scenarios.
- HLS honors the chosen audio track (`AudioStreamIndex`) and burns the selected subtitle in
  (`SubtitleMethod=Encode`): text tracks, embedded or sidecar, through libass, bitmap tracks through
  `overlay`. PlaybackInfo accepts `AudioStreamIndex`/`SubtitleStreamIndex` with a `MediaSourceId`
  (query or body), no longer direct plays a source whose selected subtitle needs `Encode`/`Hls`,
  and writes the tracks, `SubtitleMethod` and `TranscodeReasons` into the `TranscodingUrl`. A new
  track in a play session starts a new transcode job. Fonts: system fontconfig, or
  `-fonts-dir`/`TINYFIN_FONTS_DIR`.
- A scan forgets items whose file or folder is gone (children, extras, collection
  and playlist membership with them); users' play state is kept for when the
  file returns. An inaccessible or empty library folder forgets nothing.
  Refreshing a series, season or folder item re-walks it.
- `.ignore` files (empty: whole folder; otherwise gitignore-like name patterns,
  nearest file wins), hidden folders and the fixed exclusion names in every
  library type; extras-named folders directly under a movie library root are
  ordinary folders.
- DVD and Blu-ray folders and `.iso`/`.img` images as movies (`VideoType`,
  `IsoType`, DVD media info from the main title set, `Data` streams).
- `.strm` files (address or path): item, media source, on-demand probe and
  proxied stream.
- Real-time library monitor (Linux, inotify) for libraries whose options carry
  `EnableRealtimeMonitor`.
- Library options applied by a scan: `EnablePhotos`, `SeasonZeroDisplayName`,
  `EnableEmbeddedTitles`.
- Contract: `specs/scan.md`, 23 scenarios; the runner can change the media tree
  (`fs` steps, `-media-local-root`) and wait for a background change (`retryMs`).

### Fixed

- Device routes match their query parameter names without regard to case, as every other route does (`devices.md` R13):
  `DELETE /Devices?Id=…`, the spelling a case-insensitive binding accepts, answered `204` here and deleted nothing
  because the handler read `id` only, so an admin tool silently failed to remove a device.

### Changed

- A picture served once is answered from a copy kept beside the database instead of being read off the media path
  again (`images.md#cache` R1, untestable as a scenario): a client that scrolls a library asks for dozens of pictures
  per screen and asks again on every scroll back, so on a remote library — an rclone, WebDAV or NFS mount — the same
  kilobytes crossed the mount over and over. The copy is keyed by what identifies the picture (item, kind, index, tag)
  and by the source's size and modification time, so a replaced picture is never served stale, and the response keeps
  the file's own `ETag` and `Last-Modified`. Copies are capped at 2 GiB (`-image-cache-size`, `0` disables) with the
  least recently used dropped past it; a `404` still means the library has no such picture.
- `GET /Items?SearchTerm` matches without regard to accents and punctuation,
  ranks (starts with, ends a word, seasons and episodes, rest) and, with no
  type list, also finds genre, studio and person names; an episode is no
  longer found by the name of its series.
- The `Genres` and `Studios` filters of `GET /Items` take `|`-separated values,
  match any of them and ignore case; `Person` takes `PersonTypes`;
  `GenreIds`, `StudioIds` and `PersonIds` are honored.
- `GET /Items/{itemId}/Similar`, `/Items/Filters` and `/Items/Filters2` are
  real (they used to answer empty), `GET /Studios` lists the studios.
- `.nfo` files: `<studio>`, `<actor>`, `<director>`, `<credits>` and `<writer>`
  are read; repeated genres and studios in another case collapse to one.
- A media source is named after its file, like the reference.
- `ImageTags`, `BackdropImageTags` and `ImageBlurHashes` are always present on an
  item (empty when it has no picture).
- Library scans are much faster on slow filesystems: files whose size and
  mtime are unchanged since their last successful probe reuse the stored media
  info instead of running `ffprobe`; scan workers default to 8, `ffprobe`
  processes are bounded globally, up to 3 libraries scan in parallel, and
  sidecar images are found with one directory listing per folder. New options
  `-scan-concurrency`, `-scan-probe-concurrency`, `-scan-library-parallelism`,
  `-scan-force-probe` (env `TINYFIN_SCAN_*`). Each scan logs probed/skipped
  counts and duration. New migration `0018_scan_signatures`.

### Added

- Subtitles. External subtitle files beside a video (`.srt .ass .ssa .vtt .smi
  .sami .sup`, named `<video>.<lang>[.forced][.default][.sdh].<ext>` or with a
  free title, any case) are listed as `MediaStreams` ahead of the video's own
  streams, in the reference's file-name order, with `Language`, `Title`,
  `IsForced`/`IsDefault`/`IsHearingImpaired`, `IsExternal`, `Path`, `Score`
  and the `DisplayTitle` the reference builds (language names come from the
  culture list, now `internal/culture`). The folder listing is read once per
  directory by the scan, so adding, removing or renaming a file is seen on
  the next scan without re-probing the video. `HasSubtitles` covers them.
  `GET /Videos/{id}/{mediaSourceId}/Subtitles/{index}/Stream.{format}` (and
  `.../{startPositionTicks}/Stream.{format}`) serves them, and embedded text
  tracks (extracted with ffmpeg), as `srt`, `vtt`, `ass`, `ssa`, `json` and
  `ttml` with `startPositionTicks`, `endPositionTicks`, `copyTimestamps` and
  `addVttTimeMap`; `.../subtitles.m3u8` lists the WebVTT windows.
  PlaybackInfo sets `DefaultSubtitleStreamIndex`/`DefaultAudioStreamIndex`
  (from `SubtitleMode`, `SubtitleLanguagePreference`) and, when the request
  carries a device profile, `DeliveryMethod`/`DeliveryUrl` per subtitle
  stream. `POST /Videos/{id}/Subtitles` and `DELETE
  /Videos/{id}/Subtitles/{index}` manage external files; the provider search
  routes answer like a server with no subtitle provider, and
  `/FallbackFont/Fonts` lists nothing. The stream routes require a token
  (the reference serves them anonymously). New contract area `subtitles`
  (`docs/contract/specs/subtitles.md`, 23 scenarios), a text-body assertion
  (`expect.text`) in the conformance runner, and a lazy `SubFiles` fixture
  library. Not done: burning a subtitle into the transcoded video and
  subtitle groups in HLS manifests.
- `GET /Search/Hints`: ranked hints over media items and over genre, studio and
  person names, with the include flags, media types, parent and paging of the
  reference (spec `docs/contract/specs/search.md`).
- Browse by name: `GET /Genres`, `/Studios`, `/Persons`, `/Years` and their
  single forms, openable by id through `/Items/{id}`; `GET /Movies/Recommendations`,
  `/Items/Suggestions`, `/Items/{id}/Collections`, `/Items/{id}/Intros`,
  `/Movies|Shows|Trailers/{id}/Similar`, `GET /Items/Root`
  (spec `docs/contract/specs/browse.md`).
- Playlists: `POST /Playlists`, read and update, entries (add, remove, move,
  list), owners, open access and shares, the Playlists view, deletion by the
  owner (spec `docs/contract/specs/playlists.md`). New migrations
  `0022_playlists` and `0023_named_items`.
- `GET`/`POST /System/Ping`, `GET /GetUtcTime`, `GET /Localization/Options`,
  `GET /Localization/ParentalRatings` (spec `docs/contract/specs/misc.md`).
- Contract fixtures: lazy libraries `Search`, `SearchShows` and `Punct`.
- Coverage tracker: `docs/COVERAGE.md` (overview, per controller and per
  subsystem) and `docs/coverage/` (every operation of the Jellyfin 12.2
  OpenAPI document with a status computed from the registered routes and the
  passing contract scenarios, plus hand-written subsystem features), generated
  by `go run ./cmd/conformance coverage` and validated in CI with `-check`.
  The README carries a generated summary and badges.
- Local artwork follows the reference: `png`, `jpg`, `jpeg`, `webp`, `tbn` and
  `gif` pictures in any case; `<video>`/`<video>-poster`/`poster`/`folder`/
  `cover`/`default`/`movie` for `Primary`; `Logo`, `Banner`, `Art`, `Thumb` and
  `Disc` names; every `fanart`/`background`/`art`/`backdrop<N>`/`extrafanart/`
  match as an indexed `Backdrop`; season pictures from the series folder
  (`seasonNN-poster`, `season-specials-poster`) and the season folder; episode
  `<episode>-thumb`/`<episode>` pictures; folder items and the library root's
  own pictures; `ImageBlurHashes`, tags that change with the file, `Art` in
  `ImageTags`, `Backdrop/{index}` answering 404/400 like the reference and
  immutable caching for tagged requests.
- Trailers, special features, theme songs and theme videos are cataloged as
  items owned by their movie, series or season and served by
  `/Items/{id}/LocalTrailers`, `/SpecialFeatures`, `/ThemeSongs`,
  `/ThemeVideos`, `/ThemeMedia` and the user-scoped legacy forms; items carry
  `ExtraType`, `LocalTrailerCount`, `SpecialFeatureCount` and (stacked movies)
  `PartCount`; `/Videos/{id}/AdditionalParts` lists the other files of a
  stacked movie. Extras never show up in listings, counts or searches. New
  migration `0020_extras`.
- Other versions of a movie and files of one episode with the same numbering
  and name are media sources of one item (selectable by `MediaSourceId`).
- Season folders that share a number are counted in the series' `ChildCount`;
  a folder named like an episode (`S26E01`) is a plain folder; `E5E6`,
  `E05E06E07` and `Show Title3` numbering; a movie folder that also holds a
  sub folder is a `Folder` item; a folder of a series with no video in it is a
  plain `Folder` (not a season); `ico`, `wbmp` and camera raw pictures are
  photos.
- Conformance: specs `images.md` and `extras.md`, 35 new scenarios, lazy
  fixture libraries `Art`, `ArtShows`, `Extras`, `ExtraShows`, `Resid` and
  `PhotoTypes`, and `{{library.<key>.id}}` templates.
- Collections (`BoxSet`): `POST /Collections` (name, ids), `POST` and
  `DELETE /Collections/{id}/Items`, and `DELETE /Items/{id}` for collections,
  gated on the `EnableCollectionManagement` policy (administrators always). A
  collection links its members without moving them, lists them in
  premiere-date order, derives its year, genres and lowest rating from them,
  reports played state derived from the leaves below (and writes a played mark
  to them), and is found by `IncludeItemTypes=BoxSet` under the collections
  library or a content library, in `CollapseBoxSetItems` and in
  `/Items/Counts`. The `Collections` library is created with the first
  collection and shown as a view only while a collection is visible.
- Library scans build collections from movie `<set>` names when the library
  option `AutomaticallyAddToCollection` is on and at least two movies share a
  name: new members join existing collections, nothing is removed, a refresh of
  one library leaves collections alone. `POST /Library/VirtualFolders` keeps
  the `LibraryOptions` it is sent. New migration `0020_collections`
  (`items.set_name`, unique collection links); contract: `specs/collections.md`.
- Library scans follow the reference's naming without metadata: a movie folder
  keeps its text as the name (`Foo (2015)`) and the year is read from it; loose
  movie files, folders of several movies, stacked parts and versions are
  cataloged, nested folders become `Folder` items; episode numbering covers
  `Show - 05`, bare and four digit numbers, `Season 2 Episode 13`, `[S02E15]`,
  dates, `S01E02E03` and `1x05-06` (with `IndexNumberEnd`), and episode names
  are cut at the year and release tokens.
- Folders of a series that are not `Season N` become seasons named after them,
  extras folders are skipped, folders inside a season are `Folder` items, an
  episode naming a season without a folder gets an empty season, and a season
  lists the episodes that carry its number.
- Home-videos libraries catalog pictures as `Photo` items and folders holding
  pictures as `PhotoAlbum`.
- An `.nfo` with several `<episodedetails>` is one multi-episode item (titles
  joined with ` / `).
- `RecursiveItemCount` on folders, seasons and series; `MediaType` is `Unknown`
  for everything that is not playable; listings carry `ChildCount` and
  `RecursiveItemCount` only when asked for in `Fields`.
- Network `EnableRemoteAccess=false` answers `503` to requests from outside the
  local network.
- Sort names drop the articles `the`, `a` and `an` anywhere as whole words.

- Home-videos libraries are scanned: nested folders become `Folder` items and
  video files `Video` items named after the file, with probed media, an
  optional `<name>.nfo` and the same playback path as movies; hidden entries,
  sidecar folders and trailer or sample files are left out.
- Episodes without a `SxxExx` pattern (specials, loose files in season folders
  and files directly in a series folder) are cataloged: named by the `.nfo`
  title or the file name, numbered only by what the name or the `.nfo` says,
  under their season folder or a season created for them (`Season N`,
  `Season Unknown`); season 0 is named `Specials`.
- A series takes `RunTimeTicks` from its `tvshow.nfo` `<runtime>` (a `0` is
  returned as `0`).
- User configuration validation per the users contract: `null` for a boolean,
  `SubtitleMode` or a list member and non-GUID list entries answer `400`,
  `SubtitleMode` is a lenient enumeration (numbers, any letter case, comma
  combinations), numbers and booleans are accepted for string members, and a
  `null` or non-object body answers `400`; also applied to the `Configuration`
  of `POST /Users`.
- `GET|POST /Auth/Keys` and `DELETE /Auth/Keys/{key}` specified and matched:
  listing members (`Id` `0`, `IsActive` `false`, no `DateRevoked`), verbatim
  application names and exact, case-sensitive token revocation.
- `GET /Items` query semantics per the items contract: library-views form, scope
  and parent rules, library access restriction (also for `Ids`), filters, search
  ranking, multi-key sorting, paging and `Fields` shaping; `GET /Items/{id}`
  returns a problem document for unknown or hidden items.
- Playstate area per the playstate contract: mark played/unplayed
  (`datePlayed`, play count and position rules, acting-user rules, folder marks
  that never write to the leaves), `POST|DELETE /UserFavoriteItems/{id}`,
  playback start/progress/stopped reporting with resume thresholds, live session
  state (`NowPlayingItem`, `PlayState`, queue) readable through
  `GET /Sessions?deviceId=`, `ExcludeActiveSessions` on `/UserItems/Resume`, and
  the `POST|DELETE /Users/{userId}/PlayedItems/{itemId}` aliases the official
  web client still calls.
- `GET|POST /System/Configuration/network` (administrator) with
  `LocalNetworkSubnets`: when set, only those subnets and loopback count as the
  local network for the remote-access login rule, the password-reset PIN file
  and `IsInNetwork`; unset keeps the private and link-local ranges.
- Conformance runner: server-file steps (`-server-data-dir`, `-read-file-cmd`),
  named configuration sections, the `origin:non-loopback` capability, risky
  scenarios ordered last behind a sole-administrator guard, and `-strict`.
- Executable behavior contract for Jellyfin 12+ wire compatibility under
  `docs/contract/` (users, items, playstate) with a conformance runner
  (`cmd/conformance`) that runs the same scenarios against a reference server
  and against Tinyfin.

### Changed

- Titles, sort titles, original titles and plots from `.nfo` files are stored
  without surrounding white space, and sort keys ignore it.
- Episodes are named after their file (without the extension) when no `.nfo`
  title exists, and an episode `.nfo` `<season>`/`<episode>` replaces the
  numbers of the file name.
- The `IsFolder` query parameter of `GET /Items` is ignored, as on the
  reference; `Filters=IsFolder` is unchanged.
- Playback info and stream requests for an item with no media file (a series,
  season or folder) answer `400`.
- Identifiers are written as 32 lowercase hex digits without dashes in every
  response (`wire.id.R1`); ids in dashed or upper-case form are still accepted
  in paths, query parameters and bodies (`wire.id.R2`). Existing databases are
  migrated in place (migration 0013) and a persisted dashed server id is
  folded on startup, so clients see a one-time `ServerId` spelling change.
- Item sort names follow the reference: a leading article is dropped, accents
  fold to ASCII, digit runs are zero-padded and episodes order by season and
  episode (`items.sort.R8`, `R9`); `SortName` is returned in listings only when
  requested through `Fields`. Existing items pick the new key up on their next
  library scan.
- `IsHd` and `Is4K` select resolution buckets and combine as a union
  (`items.filter.R17`).
- A progress or stopped report below the minimum resume position no longer
  sets `LastPlayedDate`; a negative progress position is stored as given.
- QuickConnect: authorizing an already authorized code answers 500 and
  authorizing moves `DateAdded` one minute forward, as on the reference.
- Compatibility scope is Jellyfin 12+ only: pre-12 legacy routes and legacy
  authentication channels are not part of the contract (decision D14).
