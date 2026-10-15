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

## <a id="fields"></a>Listings

- **chapters.fields.R1** A listing (`GET /Items`) carries `Chapters` only when `Fields` contains `Chapters` (empty list for an item without chapters); without it the member is absent.
- **chapters.fields.R2** `Fields` names are case-insensitive.

## <a id="segments"></a>Media segments

- **chapters.segments.R1** `GET /MediaSegments/{itemId}` answers `{Items, TotalRecordCount, StartIndex}`. With no segment provider configured the list is empty (`TotalRecordCount` 0, `StartIndex` 0); chapters are not turned into segments.
- **chapters.segments.R2** `includeSegmentTypes` (repeatable, or comma separated) takes the segment type names `Unknown`, `Commercial`, `Preview`, `Recap`, `Outro`, `Intro` in any case, or their numbers 0 to 5, and only filters.
- **chapters.segments.R3** An unknown type name, an empty value or a number outside 0 to 5 is a `400` validation problem on `includeSegmentTypes` (checked before the item is looked up). A comma list is invalid as a whole when any entry is.
- **chapters.segments.R4** An unknown well-formed id is `404`, a malformed one `400`, an anonymous caller `401`.

## Known differences

* **Chapter pictures.** The reference may extract a still per chapter on demand (`/Items/{id}/Images/Chapter/{n}`) when the library asks for chapter images, and then sets `ImageTag`. Tinyfin never extracts them: `ImageTag` is always omitted and the chapter image route answers 404 (as the reference does with extraction off).
* **Disc images and `.strm`.** Chapters are read from the file ffprobe probes at scan time. A `.strm` item (probed on play) has none.
* **Existing databases.** Migration 0026 drops the scan signatures so every item is probed again once, to pick its chapters up.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `chapters.dto.R1`, `R2`, `R3` | `chapters.item.detail` |
| `chapters.dto.R4` | `chapters.item.none` |
| `chapters.fields.R1`, `R2` | `chapters.item.listing`, `chapters.item.none` |
| `chapters.segments.R1`, `R2` | `chapters.segments.empty` |
| `chapters.segments.R3`, `R4` | `chapters.segments.errors` |
