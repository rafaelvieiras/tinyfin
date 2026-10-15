# Trickplay: seek-thumbnail sprite sheets

The thumbnails a player shows while scrubbing: JPEG sheets of a grid of small pictures taken at a fixed interval, the `Trickplay` member of an item that lists them, and the two routes that serve a playlist of the sheets and each sheet. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/trickplay.json`](../scenarios/trickplay.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy libraries `Trickplay`, which has the sheets switched on, and `TrickplayOff`). Rule IDs are stable (`trickplay.<topic>.R<n>`).

## Fixture facts this spec relies on

* `Tp Short (2501)` is 320x240 and 25.5 s, `Tp Big (2502)` 320x180 and 1500 s, `Tp Narrow (2503)` 160x90 and 12 s, all in `Trickplay`, whose options are `EnableTrickplayImageExtraction` and `ExtractTrickplayImagesDuringLibraryScan` true. `Tp Off (2504)` is in `TrickplayOff`, which has neither.
* Generation runs in the background after the scan: a scenario waits for the sheets to appear (retries) rather than assuming them at once.

## <a id="generation"></a>When sheets exist

- **trickplay.generate.R1** Sheets are made only for the videos of a library with `EnableTrickplayImageExtraction` true; a library with it off (the default) has none, and its items carry an empty `Trickplay` object.
- **trickplay.generate.R2** With `ExtractTrickplayImagesDuringLibraryScan` true the sheets are made as part of the library scan, and are there shortly after it. A scan of unchanged media does not make them again.
- **trickplay.generate.R3** One resolution is made per item: width 320, or the video's own width when it is narrower (never upscaled, rule trickplay.item.R3). The height follows the picture's aspect ratio, both rounded down to an even number. One thumbnail is taken every 10 s starting at 0; a grid is 10 columns by 10 rows, so one sheet holds 100 thumbnails and a partial last sheet is padded to the full grid size.

## <a id="item"></a>The item

- **trickplay.item.R1** `Trickplay` is an object keyed by the media source id (the item's id), whose value is an object keyed by the width (as text) of each resolution: `{Width, Height, TileWidth, TileHeight, ThumbnailCount, Interval, Bandwidth}`. `Width`/`Height` are one thumbnail's size, `TileWidth`/`TileHeight` the grid (10x10), `Interval` the spacing in milliseconds (10000), `ThumbnailCount` the number of thumbnails the extraction yields (the duration over the interval, rounded to the nearest: 12 s is 1, 25.5 s is 3, 330 s is 33; at least 1), and `Bandwidth` the bits per second of the largest sheet over the time its grid covers (a positive integer).
- **trickplay.item.R2** The member is present on a single-item read, and on a listing only when `Fields` names `Trickplay`. A video item with no sheets has `{}`; folders (series, seasons, boxsets, libraries) have no member.
- **trickplay.item.R3** A video narrower than 320 px is keyed by its own width (`160`), with no `320` entry.

## <a id="playlist"></a>The tiles playlist

- **trickplay.playlist.R1** `GET /Videos/{itemId}/Trickplay/{width}/tiles.m3u8` answers `200` `application/vnd.apple.mpegurl`: `#EXTM3U`, `#EXT-X-TARGETDURATION`, `#EXT-X-VERSION:7`, `#EXT-X-MEDIA-SEQUENCE:1`, `#EXT-X-PLAYLIST-TYPE:VOD`, `#EXT-X-IMAGES-ONLY`, then for each sheet `#EXTINF:<seconds>,`, `#EXT-X-TILES:RESOLUTION=<w>x<h>,LAYOUT=<c>x<r>,DURATION=<interval seconds>` and the sheet's relative URI `<n>.jpg?MediaSourceId=<id>&ApiKey=<token>` (the caller's token), and `#EXT-X-ENDLIST`.
- **trickplay.playlist.R2** `#EXTINF` is the number of thumbnails of the sheet times the interval in seconds (a 3-thumbnail sheet is `30`, a full one `1000`, so a partial last sheet is shorter than the video's tail); `#EXT-X-TARGETDURATION` is the number of sheets.
- **trickplay.playlist.R3** `MediaSourceId` is optional; a value that is not a GUID is a `400` validation problem on `mediaSourceId`.

## <a id="sheet"></a>The sheets

- **trickplay.sheet.R1** `GET /Videos/{itemId}/Trickplay/{width}/{index}.jpg` answers `200` `image/jpeg` (a JPEG of the whole grid, `Width*TileWidth` by `Height*TileHeight`), for `index` from 0 to the number of sheets minus one.
- **trickplay.sheet.R2** `404` problem document for an item that does not exist, a width the item has no sheets at, and an index past the last sheet (or negative). `400` validation problem for a `width` or `index` that is not an integer and a `mediaSourceId` that is not a GUID. `401` without credentials.

## Known differences

* **Options.** The reference reads width, interval, grid, JPEG quality, thread count and priority from server configuration (`TrickplayOptions`); Tinyfin always uses its defaults (320 px, 10 s, 10x10, quality 4) and runs one single-threaded ffmpeg at a time (`-trickplay-concurrency` / `TINYFIN_TRICKPLAY_CONCURRENCY`).
* **When.** The reference also generates through a daily scheduled task for libraries that switched generation on but not the during-scan option; Tinyfin does that only on a refresh with `RegenerateTrickplay=true` (item or library), which also forces the sheets to be made again.
* **Disabling.** Turning the option off and scanning again removes the files in both servers; the reference keeps the `Trickplay` member (pointing at nothing), Tinyfin removes it too. Not asserted.
* **Storage.** Sheets are kept in `<data dir>/trickplay/<item id>/<width>/<n>.jpg`, or beside the video in `<name>.trickplay/<width> - <c>x<r>/` when `SaveTrickplayWithMedia` is set. JPEG bytes and sizes differ, so `Bandwidth` differs by value (asserted only to be positive).
* **Index of one thumbnail.** The thumbnail at time t is sheet `floor(t / (interval * cols * rows))`, cell `floor(t / interval) mod (cols * rows)` read row by row.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `trickplay.generate.R1` | `trickplay.item.off` |
| `trickplay.generate.R2`, `R3`, `trickplay.item.R1`, `R2` | `trickplay.item.short`, `trickplay.item.big`, `trickplay.item.list` |
| `trickplay.item.R3` | `trickplay.item.narrow` |
| `trickplay.playlist.R1`, `R2` | `trickplay.playlist.short`, `trickplay.playlist.big` |
| `trickplay.playlist.R3` | `trickplay.playlist.errors` |
| `trickplay.sheet.R1`, `R2` | `trickplay.sheet.serve`, `trickplay.sheet.errors` |
