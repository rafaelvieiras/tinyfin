# Playback: track selection and burned-in subtitles in PlaybackInfo and HLS

What a client sees when it asks for a particular audio track or a subtitle that has to be rendered into the picture: what PlaybackInfo says (direct play or not, the `TranscodingUrl` and the parameters in it), and what the HLS routes of that URL answer. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/playback.json`](../scenarios/playback.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `AvTrack`).

## Fixture facts this spec relies on

* `Av Multi (2400)` is a Matroska file, H.264 video (index 0), AAC audio `eng` (1, default), MP3 audio `por` (2), embedded SubRip `eng` (3, default) and ASS `por` (4).
* The scenarios use a profile whose only direct play entry is `mkv` / `h264` / `aac`, with a `ts` / `h264` / `aac` HLS transcoding profile; the `SubtitleProfiles` vary per step.

## <a id="audio"></a>Audio track

* **playback.audio.R1** `AudioStreamIndex` (query or body) selects the audio track of the media source named by `MediaSourceId`: the source's `DefaultAudioStreamIndex` is that index. Without the track, `DefaultAudioStreamIndex` is the default audio track.
* **playback.audio.R2** When the selected track is one the profile can direct play, the answer is the direct play one (`SupportsDirectPlay` true, no `TranscodingUrl`). When it is not, `SupportsDirectPlay` is false, `SupportsTranscoding` true, `TranscodingSubProtocol` `hls`, `TranscodingContainer` the transcoding profile's container, and `TranscodingUrl` is the `master.m3u8` of the video with `AudioStreamIndex=<n>` and `TranscodeReasons` naming `AudioCodecNotSupported`.
* **playback.audio.R3** The selection is ignored when `MediaSourceId` is missing, when the index is not an audio stream of the source and when it is past the streams: `DefaultAudioStreamIndex` stays the default track.
* **playback.audio.R4** The same fields in the POSTed body have the same effect.
* **playback.hls.R1** `GET` of the `TranscodingUrl` answers `200` `application/vnd.apple.mpegurl`: `#EXTM3U`, an `#EXT-X-STREAM-INF` entry and the `main.m3u8` it points at, whose query keeps `AudioStreamIndex`, `SubtitleStreamIndex` and `SubtitleMethod` of the URL. A change of track in a play session is a new URL, hence a new transcode.

## <a id="subtitle"></a>Burned-in subtitles

* **playback.subtitle.R1** A request with a device profile whose delivery for the selected subtitle (the requested `SubtitleStreamIndex`, else the default one, subtitles.default.R1) is `Encode` is not direct played: `SupportsDirectPlay` and `SupportsDirectStream` false, `SupportsTranscoding` true, and the `TranscodingUrl` carries `SubtitleStreamIndex=<n>`, `SubtitleMethod=Encode`, `AudioStreamIndex=<default or selected>` and `TranscodeReasons` naming `SubtitleCodecNotSupported` (after `AudioCodecNotSupported` when both apply, comma separated).
* **playback.subtitle.R2** `SubtitleStreamIndex=-1` is no subtitle: it is reported as `DefaultSubtitleStreamIndex` and nothing is burned in, even when the default track would be.
* **playback.subtitle.R3** A selected subtitle delivered by `External` or `Embed` does not make the answer a transcode, and the `TranscodingUrl` (when there is one for another reason) has no `SubtitleMethod`.
* **playback.subtitle.R4** When the source is transcoded for another reason (an audio track the profile cannot take), an embedded track whose delivery was `Embed` becomes `Encode` and the URL asks for it (`SubtitleMethod=Encode`, `SubtitleStreamIndex`). A `SubtitleStreamIndex` that names no stream is reported as sent in `DefaultSubtitleStreamIndex` and has no other effect.
* **playback.subtitle.R5** A delivery of `Hls` for the selected track also ends direct play, with `SubtitleMethod=Hls`, `SubtitleStreamIndex` and `SubtitleCodecNotSupported`.

## Known differences

Observed on the reference and deliberately not reproduced (not asserted by a scenario that runs against both):

* **Playlist shape.** The reference writes a VOD playlist of `hls1/main/<n>.ts` segments with `runtimeTicks`/`actualSegmentLengthTicks` in their query; Tinyfin writes the playlist ffmpeg produces (`seg_00000.ts`, event type, `#EXT-X-ENDLIST` at the end).
* **Parameters of the URL.** The reference's `TranscodingUrl` also carries bitrates, `h264-*`/`aac-*` limits, `Tag`, `DeviceId`, `RequireAvc` and similar; Tinyfin writes only `MediaSourceId`, `VideoCodec`, `AudioCodec`, `SegmentContainer`, `PlaySessionId`, `ApiKey`, `AudioStreamIndex`, `SubtitleStreamIndex`, `SubtitleMethod` and `TranscodeReasons`. The path is `/Videos/<id>/master.m3u8`, not lower case with dashes.
* **Rendering.** The burn-in is `libass` through ffmpeg's `subtitles` filter for text tracks (embedded or sidecar files, rendered at the source resolution before the scale) and the `overlay` filter for bitmap tracks (PGS, DVD, DVB). Text uses fontconfig's system fonts, or `-fonts-dir` / `TINYFIN_FONTS_DIR` when set; the reference's server-wide fallback font setting is not used. The rendering is checked by Go tests with a real ffmpeg (text only), not by a scenario; bitmap tracks are covered by argument tests only.
* **Direct stream.** A selected track whose audio only is unsupported is served by Tinyfin with the picture copied and `SupportsDirectStream` true; the reference reports `false` there.
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
