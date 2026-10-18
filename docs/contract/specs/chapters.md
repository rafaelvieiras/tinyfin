# Chapters and media segments

The chapter marks a media file carries (ffprobe chapters, the MKV/MP4 chapter atoms) and what a client sees of them: `Chapters` on the item and `GET /MediaSegments/{itemId}`. Probed on the 12.2 reference with online metadata off.

Scenarios: [`../scenarios/chapters.json`](../scenarios/chapters.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy library `Chapters`).

## Fixture facts this spec relies on

* `Chaptered Movie (2500)` is a 6 s MKV with three chapters: `Opening` at 0 s, an untitled one at 2 s and `Finale` at 4 s.
* `Plain Chapterless (2501)` is an MP4 with no chapters.

## <a id="dto"></a>Chapters on the item

- **chapters.dto.R1** `Chapters` is a list of `{StartPositionTicks, Name, ImageDateModified}` in start order, `StartPositionTicks` being the chapter's start in 100 ns ticks (2 s is `20000000`).
- **chapters.dto.R2** A chapter's `Name` is its title tag; a chapter without one is `Chapter N` with N its 1-based position in the list (`Chapter 2`, `Chapter 10`, no zero padding).
- **chapters.dto.R3** `ImageTag` is a member only when a chapter picture exists; `ImageDateModified` is then the picture's date and otherwise `0001-01-01T00:00:00.0000000Z`. A single read (`GET /Items/{itemId}`, `GET /Users/{userId}/Items/{itemId}`) carries the list without `Fields`.
- **chapters.dto.R4** An item without chapters (any item without a media file included) has `Chapters: []` on a single read.

## <a id="images"></a>Chapter pictures

A still taken at each chapter mark, opt-in per library. Scenarios: [`../scenarios/chapters2.json`](../scenarios/chapters2.json) (lazy libraries `ChapterImg`, `ChapterImgTask` and `ChapterImgOff`; the videos are 640x360 ramps whose brightness grows with time, so the moment a still was taken at can be read off its pixels).

- **chapters.images.R1** Pictures are made only for videos of a library with `EnableChapterImageExtraction` true (the default is off). A chapter that has one carries `ImageTag` (32 hex digits) and `ImageDateModified` (the picture's date) in `Chapters` (single read; a listing with `Fields=Chapters`); one without has neither member's value (`ImageTag` absent, the zero date, `chapters.dto.R3`).
- **chapters.images.R2** `GET /Items/{itemId}/Images/Chapter/{index}` (`Chapter` in any case, `index` 0-based into `Chapters`, 0 when it is left out) answers `200` `image/jpeg`, the still, with `Cache-Control: public`. It is public like the other picture routes (no token needed). A `tag` query value (not checked against the picture) makes the answer `Cache-Control: public, max-age=31536000, immutable` with `ETag` the tag.
- **chapters.images.R3** `404` for an index past the last chapter, a negative index, a chapter without a picture and an item that does not exist; a `400` validation problem on `imageIndex` for an index that is not an integer.
- **chapters.images.R4** A picture is taken at the chapter's start, except that a chapter starting at 0 is taken at 15 s. A video shorter than that therefore gets no picture for any of its chapters (the extraction of the item fails as a whole): a 10 s file with chapters at 0 s and 5 s has none, a 16 s one has them.
- **chapters.images.R5** With `ExtractChapterImagesDuringLibraryScan` true the pictures are made right after the library scan; with only `EnableChapterImageExtraction` they are made by the `Extract Chapter Images` task (`RefreshChapterImages`, id `4e6637c832ed644d1af3370a2506e80a`, category `Library`, daily at 02:00), which skips libraries that have extraction off. A refresh of the item does not make them. A scan of unchanged media does not make them again.

## <a id="fields"></a>Listings

- **chapters.fields.R1** A listing (`GET /Items`) carries `Chapters` only when `Fields` contains `Chapters` (empty list for an item without chapters); without it the member is absent.
- **chapters.fields.R2** `Fields` names are case-insensitive.

## <a id="segments"></a>Media segments

- **chapters.segments.R1** `GET /MediaSegments/{itemId}` answers `{Items, TotalRecordCount, StartIndex}`. With no segment provider configured the list is empty (`TotalRecordCount` 0, `StartIndex` 0); chapters are not turned into segments.
- **chapters.segments.R2** `includeSegmentTypes` (repeatable, or comma separated) takes the segment type names `Unknown`, `Commercial`, `Preview`, `Recap`, `Outro`, `Intro` in any case, or their numbers 0 to 5, and only filters.
- **chapters.segments.R3** An unknown type name, an empty value or a number outside 0 to 5 is a `400` validation problem on `includeSegmentTypes` (checked before the item is looked up). A comma list is invalid as a whole when any entry is.
- **chapters.segments.R4** An unknown well-formed id is `404`, a malformed one `400`, an anonymous caller `401`.

## Known differences

* **Chapter pictures.** Tinyfin writes each still, at the video's own size, to `<data dir>/chapters/<item id>/<start ticks>.jpg` (with a `.stamp` of the video's size and date; a changed file is extracted again) and serves it as it is: the `maxWidth`, `maxHeight`, `width`, `height`, `quality`, `format` and fill parameters of the reference's picture route are ignored (as for every picture, see `images.md`). `ImageTag` is a digest of the file's path and date, so it differs by value. The reference also puts the picture's server path in `ImagePath` of every `Chapters` entry, for every caller; Tinyfin never does (a path leak, security-driven divergence, not asserted). Extraction is one ffmpeg at a time and concurrent with the scan's tail, so a scenario waits for the tags (retries).
* **Disc images and `.strm`.** Chapters are read from the file ffprobe probes at scan time. A `.strm` item (probed on play) has none.
* **Existing databases.** Migration 0026 drops the scan signatures so every item is probed again once, to pick its chapters up.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `chapters.dto.R1`, `R2`, `R3` | `chapters.item.detail` |
| `chapters.dto.R4` | `chapters.item.none` |
| `chapters.images.R1`, `R2`, `R3` | `chapters.images.generate` |
| `chapters.images.R4` | `chapters.images.short` |
| `chapters.images.R5` | `chapters.images.task`, `chapters.images.off` |
| `chapters.fields.R1`, `R2` | `chapters.item.listing`, `chapters.item.none` |
| `chapters.segments.R1`, `R2` | `chapters.segments.empty` |
| `chapters.segments.R3`, `R4` | `chapters.segments.errors` |
