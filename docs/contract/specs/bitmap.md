# Picture subtitles (PGS, DVD, DVB)

The subtitle tracks that are pictures, not text: how a media file's PGS (Blu-ray), DVD and DVB tracks appear in the streams of an item, how they are offered to a player (burned into the video, kept in the container, or as a file), the route that serves them as files, and the burn-in itself. Probed on the 12.2 reference with online metadata off. Text tracks are in [`subtitles.md`](subtitles.md).

Scenarios: [`../scenarios/bitmap.json`](../scenarios/bitmap.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `Bitmap`; the PGS stream is written by `scripts/conformance-pgs.py` because ffmpeg has no PGS encoder, the DVD and DVB tracks are ffmpeg re-encodes of it). Rule IDs are stable (`bitmap.<topic>.R<n>`).

## Fixture facts this spec relies on

* All four are 4 s MKV files, 320x240, with an AAC track (index 1) and picture cues showing the words BURN IN as white block letters at the bottom, from 0.5 s to 3.5 s.
* `Bmp Pgs (2650)`: one PGS track "eng" at index 2. `Bmp Dvd (2651)`: one DVD track "eng". `Bmp Dvb (2652)`: one DVB track "eng" (a cue with no end time). All three are flagged default.
* `Bmp Mixed (2653)`: SubRip "eng" (2, default), PGS "por" (3), DVD "spa" (4); only the first is flagged default.

## <a id="streams"></a>The streams of an item

- **bitmap.streams.R1** A picture track is a `Subtitle` stream with `Codec` `PGSSUB` (Blu-ray PGS), `DVDSUB` or `DVBSUB`, `IsTextSubtitleStream` false and `IsExternal` false; `IsDefault`/`IsForced`/`Language` come from the container, and `DisplayTitle` is `<Language> [- Default] [- Forced] - <CODEC>` (`English - Default - PGSSUB`, `Portuguese - PGSSUB`).
- **bitmap.streams.R2** A PGS or DVD track has `SupportsExternalStream` true and the `Width`/`Height` of the picture it draws on (the file's own subtitle size, here 320 and 240). `Score` is present only for a track that is default or forced (`subtitles.discover.R10`): a default picture track scores 112211, the default text track beside it 112221; a picture track that is neither default nor forced has none.
- **bitmap.streams.R3** A DVB track has `SupportsExternalStream` false, no `Width`/`Height`, and a score one hundred lower than the same track in another codec (a default DVB track is 112111).

## <a id="delivery"></a>How PlaybackInfo offers them

- **bitmap.delivery.R1** A picture track is never converted to text: an `Hls` or `External` entry for a text format (`vtt`, `srt`, ...) does not take it, so it is `Encode` (burned into the video) while the text track beside it keeps its method. It has no `DeliveryUrl`.
- **bitmap.delivery.R2** When the selected track (`SubtitleStreamIndex`) is delivered by `Encode`, the source is transcoded: `SupportsDirectPlay` false and `TranscodingUrl` carrying `SubtitleMethod=Encode`, the `SubtitleStreamIndex` and `TranscodeReasons=SubtitleCodecNotSupported`.
- **bitmap.delivery.R3** An `Embed` entry whose `Format` is the track's codec name (`pgssub`, `dvdsub`, `dvbsub`, in any case) keeps the track in the container: the track is `Embed`, and when it is the selected one the source can still be direct played. A track whose codec no entry names is `Encode`.
- **bitmap.delivery.R4** When the selected track is not `Embed`, nothing is embedded (`subtitles.delivery.R4`): every track is `Encode` and the source is transcoded. A container that cannot hold the track (the profile's direct play container does not match the file) transcodes and burns it as well.
- **bitmap.delivery.R5** An `External` entry whose `Format` is the track's codec name (case-insensitive, `pgssub` for PGS, `dvdsub` for DVD, `dvbsub` for DVB) delivers the track as a file: `External`, `DeliveryUrl` `/Videos/{item id, dashed}/{media source id}/Subtitles/{index}/0/Stream.{format}?ApiKey={token}` with the format spelled as the entry spells it (`.PGSSUB`), `IsExternalUrl` false.
- **bitmap.delivery.R6** Other names do not match: `sup`, `pgs`, `hdmv_pgs_subtitle`, `vobsub` for a PGS or DVD track are `Encode`. A DVB track is delivered `External` by an entry named `dvbsub` although the route does not serve it (`bitmap.route.R3`).

## <a id="route"></a>The track as a file

- **bitmap.route.R1** `GET /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/Stream.{format}` for a PGS track answers `200` `application/octet-stream`, the track as a `.sup` stream (it starts with `PG`); for a DVD track `200` `video/x-matroska`, a Matroska file that holds only that track. A `.sup` sidecar file is served as it is. Start position, end position and `copyTimestamps` are accepted and ignored.
- **bitmap.route.R2** The formats that name a picture track are `pgssub`, `sup`, `pgs`, `hdmv_pgs_subtitle`, `dvdsub` and `vobsub` (any case), all meaning "the track as stored"; for a DVD track the container extension `mks` is one as well. Anything else (`srt`, `vtt`, `ass`, `dvbsub` on a PGS or DVD track, `dvd_subtitle`, `mks` on a PGS track, unknown names) is a `400`: picture tracks are not converted.
- **bitmap.route.R3** A DVB track has no file: `404` for every format.

## <a id="hls"></a>HLS

- **bitmap.hls.R1** A picture track is never listed in the master playlist's `#EXT-X-MEDIA:TYPE=SUBTITLES` group (`playback.hls.R7`): with the text track selected and `Hls` delivery the group has that one track and no `Subtitles/3/`, `Subtitles/4/` URI.
- **bitmap.hls.R2** With a picture track selected the master has no subtitle group at all and its variants carry `SubtitleMethod=Encode`: the picture is in the video.

## <a id="burnin"></a>The burn-in

- **bitmap.burnin.R1** [untestable: a property of the picture's pixels; covered by a Go test with a real ffmpeg] A burned-in picture track is decoded to frames and overlaid on the video at the position its cues give (the cue canvas is the picture's size), shown from the cue's start and removed at its end; a track whose cues carry no end (DVD and DVB from the fixtures) stays on screen. Both servers use ffmpeg's `overlay` filter (R3). Not asserted by a scenario (it is a property of the pixels): `go test ./internal/transcode -run BitmapBurnIn` builds a PGS and a DVD track, transcodes with `Subtitle.Bitmap`, and checks by pixel difference against the plain transcode that nothing differs before the cue, the cue's pixels are there inside its window and (PGS) gone after it. The same frames were checked on both servers through `master.m3u8` / `main.m3u8` of a real transcode (cue from 0.5 s to 3.5 s: the difference with the plain transcode starts at the first sample inside the window, PGS ends with it, DVD/DVB persist).

- **bitmap.burnin.R2** [untestable: the arguments of ffmpeg] A picture burn-in passes `-canvas_size <W>x<H>`, the size of the subtitle stream (a DVB track has none: no option), before every input: the video and, for a sidecar (`.idx`, `.sup`, `.mks`), the sidecar file as a second input. The pictures are read from that input's first subtitle stream (`-map` of the sidecar's stream). Tinyfin probes the files for these sizes when it starts the job.
- **bitmap.burnin.R3** [untestable: the arguments of ffmpeg] The filter graph is `[<in>:<stream>]scale,scale=<W>:<H>:fast_bilinear[sub];[0:0]setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709[,<scale of the output>],format=yuv420p[main];[main][sub]overlay=eof_action=pass:repeatlast=0`, with `<W>x<H>` the size of the output picture. When the track's size has another shape than the output (or is unknown) the pictures are fitted to the height instead: `scale,scale=-1:<H>:fast_bilinear,crop,pad=max(<W>\,iw):max(<H>\,ih):(ow-iw)/2:(oh-ih)/2:black@0,crop=<W>:<H>`; the same shape in another size (640x480 on a 320x240 picture) is a plain scale. Checked by pixel difference on both servers for PGS, DVD, DVB, an index file, a canvas wider and one narrower than the picture.
- **bitmap.burnin.R4** [partly testable: the scenarios check that every case transcodes; the arguments are untestable] A text burn-in is `setparams=...bt709[,<scale>],format=yuv420p,subtitles=f='<file>'[:charenc=<enc>]:fontsdir='<dir>'`: the colour tags, the scale and the pixel format come first and the text is rendered on the picture the viewer gets. An embedded track is extracted first (an ASS or SSA track as ASS, any other as SubRip) and the file burned; in an ASS file the style font `Arial` is replaced by `Arial Unicode MS`, as when the track is served (`subtitles.stream.R6`).
- **bitmap.burnin.R5** [partly testable: the scenarios check that every case transcodes; the arguments are untestable] An external text file whose stream has a language gets `charenc=<encoding>` from its content, by a statistical detector: `ascii` (no byte above 0x7F), `utf-8` (a byte order mark, or valid UTF-8 with at least three multi-byte characters; fewer read as `windows-1250`), `utf-32le`/`utf-32be` for a UTF-32 byte order mark, `shift-jis`, `euc-jp`, `euc-kr`, `gb18030`, `big5`, the single-byte Cyrillic (`windows-1251`, `koi8-r`, `iso-8859-5`, `ibm866`), Greek (`iso-8859-7`), Hebrew (`windows-1255`), Arabic (`windows-1256`) and Thai (`tis-620`) pages and the Latin ones (`iso-8859-1`, `iso-8859-2`, `iso-8859-9`, `windows-1250`, `windows-1252`); a file with a UTF-16 byte order mark gets none; a file with no language none at all. The observed answers of the reference for 84 samples are the corpus of `internal/transcode/testdata/charset-corpus.json`, checked by `TestDetectCharsetCorpus` (69 agree; the others are the short or ambiguous ones listed under Known differences).
- **bitmap.burnin.R6** [untestable: the arguments of ffmpeg] When the input file carries attachments (the fonts of a Matroska file), they are written to a directory and the text burn-in passes it as `fontsdir`; a font a style names that is neither attached nor installed falls back to the host's fontconfig default (`subtitles3.burn.fonts` checks that every case transcodes, a font's glyphs are a property of the pixels). The same input without attachments has no attached fonts to offer.
- **bitmap.burnin.R7** [untestable: the arguments of ffmpeg] An external picture subtitle (a PGS `.sup`, a VobSub `.idx`/`.sub` pair, a Matroska file with pictures) selected for burn-in is read as a second input exactly like R2/R3, from the start of the video and from a seek position alike (`subtitles3.burn.bitmap-sidecar` requests the first and a later segment of each).
- **bitmap.burnin.R8** [partly testable: the scenario checks that the text is drawn; the glyphs are pixels] The fallback faces of a burned-in text are the reference image's: a style font that is neither attached nor installed is drawn in DejaVu Sans (fontconfig's `sans-serif`), CJK text in WenQuanYi Zen Hei, so the picture of a missing font is the same on both servers (checked by decoding the frames of both for Latin, CJK, Cyrillic and Greek text: identical). Tinyfin's image ships those two fonts and a `fonts.conf` (`deploy/fonts.conf`), because libass draws no text at all when fontconfig has no configuration (`Failed to load fontconfig fonts!`), whatever `fontsdir` says; a host without a fontconfig configuration gets a generated minimal one (fonts directories of the job, the configured one, the first system font directory found) through `FONTCONFIG_FILE` for the ffmpeg process. (`subtitles4.burn.fonts-cjk`; Go test `TestFontEnv_WritesConfigWithoutFontconfig`)

## Known differences

* **Fonts.** The faces match when the same fonts are installed (R8): Tinyfin's image ships DejaVu Sans and WenQuanYi Zen Hei like the reference's. A Tinyfin run on a host without them draws the host's fallback faces; positions and sizes are identical, glyph shapes can differ. Attached fonts are used by both (R6).
* **Charset detection.** The reference's detector is statistical; Tinyfin's is a set of rules (R5). They agree on text of some length in the common encodings. They differ on short or ambiguous samples, which the reference reads with surprising results (a one-word UTF-8 or Latin-1 sample as `windows-1250` or `iso-8859-2`, a short Chinese sample as `iso-8859-16` or `ibm852`, a two-character Korean sample as `windows-1252`). The detector is deterministic on the bytes (two samples with the same bytes, `hu-1250` and `hu-iso2`, got the same answer, `iso-8859-2`), but its short-sample answers come from the per-language character-pair tables of a statistical detector, which cannot be re-derived from the observations (the 15 differing samples fall in three groups: a Latin sample with few accented letters, where `windows-1250`, `iso-8859-2` or `windows-1252` depends on which letter and how many; Latin text with symbols such as `©` or a no-break space, `windows-1252`; two- to six-character CJK samples, which the reference reads as a Latin or Greek page). A file in a code page that the table of R5 lacks gets `iso-8859-1`.
* **Seeking.** None left in what the picture and sound of a burn-in carry: segments of a text and of a picture burn-in at a seek position, near the end of the file and past it are byte-identical to the reference's on the fixtures tried (the arguments differ: the reference seeks with `-ss` and `-copyts`, Tinyfin shifts the timestamps around the renderer). The reference's `-sn -map -0:0` on a picture burn-in only keeps the original picture and subtitle streams out of the output, which Tinyfin's `[v]` label does already.
* **Existing databases.** Picture tracks gain `Width`/`Height` when the file is probed again; an item scanned before only gets them with its next probe.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `bitmap.streams.R1`, `R2` | `bitmap.streams.pgs`, `bitmap.streams.dvd`, `bitmap.streams.mixed` |
| `bitmap.streams.R1`, `R3` | `bitmap.streams.dvb` |
| `bitmap.delivery.R1`, `R2` | `bitmap.delivery.encode` |
| `bitmap.delivery.R3`, `R4` | `bitmap.delivery.embed` |
| `bitmap.delivery.R5`, `R6` | `bitmap.delivery.external` |
| `bitmap.route.R1`, `R2` | `bitmap.route.pgs`, `bitmap.route.dvd` |
| `bitmap.route.R3` | `bitmap.route.dvb` |
| `bitmap.hls.R1`, `R2` | `bitmap.hls.master` |
| `bitmap.burnin.R1` | none: Go test `TestBitmapBurnIn_RealFFmpeg` |
| `bitmap.burnin.R2`, `R3` | none: Go test `TestBuildArgs_BitmapGraph` |
| `bitmap.burnin.R4`, `R5` | `subtitles3.burn.fonts`, `subtitles3.burn.charenc` (the arguments: Go tests `TestBuildArgs_AudioStreamAndBurn`, `TestDetectCharset`, `TestDetectCharsetCorpus`) |
| `bitmap.burnin.R6` | `subtitles3.burn.fonts` (the arguments: Go test `TestExtractFonts_Attachments`) |
| `bitmap.burnin.R8` | `subtitles4.burn.fonts-cjk` (the arguments: Go tests `TestFontEnv_*`) |
| `bitmap.burnin.R7` | `subtitles3.burn.bitmap-sidecar` |
