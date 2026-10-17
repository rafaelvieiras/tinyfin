# Playback: track selection and burned-in subtitles in PlaybackInfo and HLS

What a client sees when it asks for a particular audio track or a subtitle that has to be rendered into the picture: what PlaybackInfo says (direct play or not, the `TranscodingUrl` and the parameters in it), and what the HLS routes of that URL answer. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/playback.json`](../scenarios/playback.json), [`../scenarios/playback2.json`](../scenarios/playback2.json), [`../scenarios/playback4.json`](../scenarios/playback4.json) (the offer). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `AvTrack`; library `Strm` holds a `.strm` whose address `scripts/conformance-strm-serve.sh <media-root>` serves, it must be running for `playback.offer.strm-http`).

## Fixture facts this spec relies on

* `Av Multi (2400)` is a Matroska file, H.264 video (index 0), AAC audio `eng` (1, default), MP3 audio `por` (2), embedded SubRip `eng` (3, default) and ASS `por` (4).
* The scenarios use a profile whose only direct play entry is `mkv` / `h264` / `aac`, with a `ts` / `h264` / `aac` HLS transcoding profile; the `SubtitleProfiles` vary per step.

## <a id="audio"></a>Audio track

- **playback.audio.R1** `AudioStreamIndex` (query or body) selects the audio track of the media source named by `MediaSourceId`: the source's `DefaultAudioStreamIndex` is that index. Without the track, `DefaultAudioStreamIndex` is the default audio track.
- **playback.audio.R2** When the selected track is one the profile can direct play, the answer is the direct play one (`SupportsDirectPlay` true, no `TranscodingUrl`). When it is not, `SupportsDirectPlay` is false, `SupportsTranscoding` true, `TranscodingSubProtocol` `hls`, `TranscodingContainer` the transcoding profile's container, and `TranscodingUrl` is the `master.m3u8` of the video with `AudioStreamIndex=<n>` and `TranscodeReasons` naming `AudioCodecNotSupported`.
- **playback.audio.R3** The selection is ignored when `MediaSourceId` is missing, when the index is not an audio stream of the source and when it is past the streams: `DefaultAudioStreamIndex` stays the default track.
- **playback.audio.R4** The same fields in the POSTed body have the same effect.
- **playback.hls.R1** `GET` of the `TranscodingUrl` answers `200` `application/vnd.apple.mpegurl`: `#EXTM3U`, an `#EXT-X-STREAM-INF` entry and the `main.m3u8` it points at, whose query keeps `AudioStreamIndex`, `SubtitleStreamIndex` and `SubtitleMethod` of the URL. A change of track in a play session is a new URL, hence a new transcode.

## <a id="subtitle"></a>Burned-in subtitles

- **playback.subtitle.R1** A request with a device profile whose delivery for the selected subtitle (the requested `SubtitleStreamIndex`, else the default one, subtitles.default.R1) is `Encode` is not direct played: `SupportsDirectPlay` and `SupportsDirectStream` false, `SupportsTranscoding` true, and the `TranscodingUrl` carries `SubtitleStreamIndex=<n>`, `SubtitleMethod=Encode`, `AudioStreamIndex=<default or selected>` and `TranscodeReasons` naming `SubtitleCodecNotSupported` (after `AudioCodecNotSupported` when both apply, comma separated).
- **playback.subtitle.R2** `SubtitleStreamIndex=-1` is no subtitle: it is reported as `DefaultSubtitleStreamIndex` and nothing is burned in, even when the default track would be.
- **playback.subtitle.R3** A selected subtitle delivered by `External` or `Embed` does not make the answer a transcode, and the `TranscodingUrl` (when there is one for another reason) has no `SubtitleMethod`.
- **playback.subtitle.R4** When the source is transcoded for another reason (an audio track the profile cannot take), an embedded track whose delivery was `Embed` becomes `Encode` and the URL asks for it (`SubtitleMethod=Encode`, `SubtitleStreamIndex`). A `SubtitleStreamIndex` that names no stream is reported as sent in `DefaultSubtitleStreamIndex` and has no other effect.
- **playback.subtitle.R5** A delivery of `Hls` for the selected track also ends direct play, with `SubtitleMethod=Hls`, `SubtitleStreamIndex` and `SubtitleCodecNotSupported`.

## <a id="offer"></a>The offer: direct play or transcode

What `POST /Items/{id}/PlaybackInfo` says about a video, from the device profile. Probed with a matrix of profiles (hls and http transcoding entries) against mp4, mkv, avi and hevc files, plus multi-audio and `.strm` sources.

- **playback.offer.R1** A source the profile does not play as it is is never offered as direct stream: `SupportsDirectPlay` and `SupportsDirectStream` false, `SupportsTranscoding` true, also when only the container does not fit (codecs the profile takes) or only an audio track does not. `TranscodeReasons` lists what the profile cannot take (`ContainerNotSupported` alone for a container-only mismatch). The URL names the transcoding entry's codecs (`VideoCodec=h264`, never `copy`); what the transcode copies is decided when it runs (a codec the source already has is copied).
- **playback.offer.R2** The transcoding entry is the first `Video` entry whose `Context` is not `Static`. Its `Protocol` (case-insensitive) `hls` gives `TranscodingSubProtocol` `hls`, `TranscodingContainer` the entry's container and `TranscodingUrl` `/videos/{id}/master.m3u8?...` (with `SegmentContainer`); anything else, a missing protocol included, gives `TranscodingSubProtocol` `http`, `TranscodingContainer` the entry's container and `/videos/{id}/stream.<container>?...` (no `SegmentContainer`).
- **playback.offer.R3** A request without a `DeviceProfile` states no limit: direct play (`SupportsDirectPlay`, `SupportsDirectStream`, `SupportsTranscoding` true, no `TranscodingUrl`) for any container and codec, an audio track selection included. (The policy flags of `policy.md#playback` still apply.)
- **playback.offer.R4** `.strm` files holding an address are offered through the profile like a file with the container and codecs the address holds (probed when first played): direct play when the profile takes them (`Protocol` `Http`, `IsRemote` true, `Path` the address, `Container` the container name, `Bitrate` the probed bit rate of the address), else the transcode offer, whose input is the address and whose `Container` stays the source's (the probe's format name, `mov,mp4,m4a,3gp,3g2,mj2` for mp4, unless the profile's direct play entries list one of its names). A `.strm` with a local path, an empty one or an address that cannot be probed is offered as it is.
- **playback.offer.R5** The members of the `TranscodingUrl` and their order: `DeviceId`, `MediaSourceId`, `VideoCodec` (the entry's list, commas kept), `AudioCodec` (the source's codec when the entry lists it, else the entry's list), `AudioStreamIndex`, `SubtitleStreamIndex` (when a subtitle is burned in or delivered through the playlist), `VideoBitrate` (the bit rate ceiling minus the audio rate), `AudioBitrate` (the track's, else 192000), `AudioSampleRate` (only when the audio codec is kept), `MaxFramerate`, `SegmentContainer`, `SegmentLength`, `MinSegments` (hls entries), `PlaySessionId`, `ApiKey`, `TranscodingMaxAudioChannels`, `EnableSubtitlesInManifest`, `EnableMpegtsM2TsMode`, `EstimateContentLength` (`True` when the entry sets it), `TranscodeSeekInfo` (`Bytes` only), `CopyTimestamps` (the entry's, `True` when set), `RequireAvc=false`, `EnableAudioVbrEncoding` (the entry's, `true` when it does not say), `Tag`, `SubtitleMethod`, then `<videocodec>-level`, `-videobitdepth`, `-profile`, `-audiochannels` (the last when the audio codec is kept) of the source, `<audiocodec>-profile` and `-audiochannels` (a `VideoAudio` codec profile's `AudioChannels` limit) when the audio codec is kept, `TranscodeReasons`, and last `allowVideoStreamCopy=false` / `allowAudioStreamCopy=false` (see R7). The path is lower case with the dashed id. Values that vary (`PlaySessionId`, `Tag`, `ApiKey`, `DeviceId`) are only checked for presence.
- **playback.offer.R7** A request that forbids copying (`AllowVideoStreamCopy` / `AllowAudioStreamCopy` false, in the body, else in the query) ends the `TranscodingUrl` with `allowVideoStreamCopy=false` and/or `allowAudioStreamCopy=false` (video first); true or absent writes nothing. `BreakOnNonKeyFrames` of the entry is never written to the URL (probed with hls, http, every container and codec combination).
- **playback.offer.R8** A transcoded video source keeps the `Container` of the source (not the transcoding entry's) and every media source carries `MediaAttachments` `[]`, `Formats` `[]`, `RequiredHttpHeaders` `{}` and `UseMostCompatibleTranscodingProfile` false.
- **playback.offer.R6** `GET` of an `http` offer (and of any `Videos/{id}/stream` request that carries `TranscodeReasons` and no `Static=true`) answers `200` with the source transcoded as one progressive stream in the URL's container (`video/mp4`, `video/x-matroska`, ...), no range support.
- Every video media source carries `Bitrate`, the rate of the file (`playback.offer.R5` scenario).

## Known differences

Observed on the reference and deliberately not reproduced (not asserted by a scenario that runs against both):

* **Offer values.** `AudioBitrate`, `MaxFramerate`, `<codec>-level` and `AudioSampleRate` come from what the scanner probed. Items scanned before the scanner kept the stream bit rate and frame rate are probed again by the next incremental scan (the scan signature records a probe version, `probeVersion`), so the member is missing only until that scan has run.
* **Playlist shape.** The reference writes a VOD playlist of `hls1/main/<n>.ts` segments with `runtimeTicks`/`actualSegmentLengthTicks` in their query; Tinyfin writes the playlist ffmpeg produces (`seg_00000.ts`, event type, `#EXT-X-ENDLIST` at the end).
* **Rendering.** The burn-in is `libass` through ffmpeg's `subtitles` filter for text tracks (embedded or sidecar files, rendered at the source resolution before the scale) and the `overlay` filter for bitmap tracks (PGS, DVD, DVB). Text uses fontconfig's system fonts, or `-fonts-dir` / `TINYFIN_FONTS_DIR` when set; the reference's server-wide fallback font setting is not used. The rendering is checked by Go tests with a real ffmpeg (text only), not by a scenario; bitmap tracks are covered by argument tests only.
* **Subtitles inside the playlist.** Delivery `Hls` ends direct play but the playlists carry no subtitle group; the client gets the picture without that track.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `playback.audio.R1`, `R2` | `playback.audio.selection` |
| `playback.audio.R3` | `playback.audio.ignored` |
| `playback.audio.R4` | `playback.audio.body` |
| `playback.subtitle.R1`, `R2` | `playback.subtitle.encode` |
| `playback.subtitle.R3`, `R4` | `playback.subtitle.no-transcode` |
| `playback.subtitle.R5` | `playback.subtitle.hls` |
| `playback.hls.R1` | `playback.hls.master` |
| `playback.offer.R1` | `playback.offer.container-only` |
| `playback.offer.R2`, `R6` | `playback.offer.http-profile` |
| `playback.offer.R3` | `playback.offer.no-profile` |
| `playback.offer.R4` | `playback.offer.strm-http` (needs `scripts/conformance-strm-serve.sh`) |
| `playback.offer.R5` | `playback.offer.url-params`, `playback.offer.url-flags` |
| `playback.offer.R7`, `R8` | `playback.offer.url-flags` |
