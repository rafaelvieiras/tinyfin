# Images: local artwork

Which picture files that sit beside an item are its artwork, which kind each one is, in which order they are tried, and how the item and the image routes expose them. The behaviors were probed one by one on the 12.2 reference with online metadata off (every picture is a file next to the media).

Scenarios: [`../scenarios/images.json`](../scenarios/images.json). Fixture state: [`../fixtures.json`](../fixtures.json), media from `scripts/conformance-media.sh` (lazy libraries `Art` and `ArtShows`). Rule IDs are stable (`images.<topic>.R<n>`).

## Fixture facts this spec relies on

* The reference extracts a `Primary` picture from the video of every movie and episode that has none, so `ImageTags.Primary` is present on those items whatever the folder holds. Scenarios therefore check the file an image comes from (`GET /Items/{itemId}/Images`, `Path`) instead of the presence of a `Primary` tag, and use other kinds to test absence.
* Pictures are copies of one tiny image; the file name is what decides what they are.

## <a id="names"></a>Names and extensions

- **images.names.R1** A picture file has one of the extensions `png`, `jpg`, `jpeg`, `webp`, `tbn`, `gif`, in any letter case (`POSTER.TBN`, `Fanart.webp`). For one name the extensions are tried in that order, so `poster.png` is chosen over `poster.jpg`, which is chosen over `poster.webp`; the name is matched ignoring case. `avif`, `bmp`, `heic`, `jpe` and every other extension are not artwork.
- **images.names.R2** `Primary` of a movie that is alone in its folder is the first that exists of: `<video>`, `<video>-poster`, `<video>-folder`, `<video>-cover`, `poster`, `folder`, `cover`, `default`, `movie` (each with the extensions of R1; `<video>` is the video file's name without its extension). The name decides before the extension: `poster.gif` wins over `folder.png`.
- **images.names.R3** The other single-picture kinds take the first that exists of: `Logo` (`<video>-logo`, `logo`, `<video>-clearlogo`, `clearlogo`), `Banner` (`<video>-banner`, `banner`), `Art` (`<video>-clearart`, `clearart`), `Thumb` (`<video>-landscape`, `landscape`, `<video>-thumb`, `thumb`), `Disc` (`<video>-disc`, `disc`, `<video>-cdart`, `cdart`, `<video>-discart`, `discart`). A series reads the same names with its folder's name for `<video>`, plus `show` after `default`, and has no `Disc`; a plain folder item reads them with its own name, also `<name>-default` after `<name>-cover`.
- **images.names.R4** Names that no kind owns are ignored: `box`, `boxfront`, `boxrear`, `back`, `front`, `screenshot`, `menu`, `primary`, `thumbnail`, `poster1`, `cover1`, `clearlogo1`, `landscape1`, `banner1`, `thumb1`, `disc1`, `art1`, `logo-black`, and a picture whose prefix is not the video's (`other-poster.jpg`). An item has no `ImageTags` entry for them.

## <a id="backdrops"></a>Backdrops

- **images.backdrop.R1** `Backdrop` collects every match, in this order: `<name>-fanart`, `<name>-fanart-1`, `<name>-fanart-2`, ..., `fanart`, `fanart-1`, ...; `<name>-background`, `<name>-background-N`, `background`, `background-N`; `<name>-art`, `<name>-art-N`, `art`, `art-N`; every picture of a folder `extrafanart/` (by file name); `<name>-backdrop`, `<name>-backdrop1`, ..., `backdrop`, `backdrop1`, `backdrop2`, ... A name is one backdrop whichever extension it has (R1). A numbered form is read from 1 up and stops at the second missing number in a row (`backdrop1` and `backdrop3` are read, `backdrop50` is not; `fanart-2` without `fanart-1` is read); `fanart1`, `backdrop-1`, `backdrop0` and `fanart01` are not backdrops. Index N of `BackdropImageTags` is `GET /Items/{itemId}/Images/Backdrop/N` and `ImageIndex` N of the image list.
- **images.backdrop.R2** The single `<name>-fanart` picture of a movie that is alone in its folder, a series or a folder is listed at two consecutive indexes (the same file twice); a movie that shares its folder reads it once.

## <a id="shared"></a>Movies sharing a folder, folder items

- **images.shared.R1** A movie that is not alone in its folder takes only the pictures that carry its own name (`<video>`, `<video>-poster`, `<video>-landscape`, `<video>-fanart`, ...), never `poster.jpg` and the other plain names.
- **images.shared.R2** The folder item of such a folder (and every other folder item, and the library's root folder item) takes the plain names: `Primary` `poster` (before `folder`, `cover`, `default`), `Backdrop` `fanart`, `Logo`, `Banner`, `Thumb`, `Art`.

## <a id="shape"></a>A movie in a folder item

- **images.shape.R1** A movie that is the only movie of a folder that is itself a `Folder` item (the folder holds a sub folder, items.names.R12) has the folder's pictures as artwork, and the folder item has them too: `poster.jpg` and `logo.png` are `Primary` and `Logo` of both.

## <a id="series"></a>Series, seasons, episodes

- **images.series.R1** A series' pictures are read in its folder with the names of R2 and R3 and the backdrops of R1 (`poster.jpg` before `folder.png`; `fanart.jpg`, `fanart-1.jpg`, `extrafanart/a.jpg` are three backdrops).
- **images.season.R1** A season reads, in the series folder, `season<N>-poster`, `season<NN>-poster` (`season1-poster` first), `season<N>-banner`, `season<NN>-banner`, `season<N>-landscape`, `season<NN>-landscape`, `season<N>-fanart`, `season<NN>-fanart` (N its number, NN with two digits; `season-specials-<kind>` and `specials-<kind>` for season 0, which `season00-` and `season0-` do not match), and in its own folder `<folder name>`, `poster`, `folder`, `cover`, `default` (Primary), `banner`, `landscape`, `thumb`, `logo`, `clearlogo`, `clearart`, `fanart`, `backdrop` and the other backdrop names. `season01.jpg`, `season01-thumb.jpg`, `season01-backdrop.jpg`, `season01-logo.jpg` are not artwork. A series-folder picture wins over a season-folder one of the same kind.
- **images.episode.R1** An episode's `Primary` is `<episode>-thumb` or `<episode>` (the file's name without its extension, any extension of R1) in the folder of the file, or in a `metadata` folder beside it; `<episode>-poster`, `-landscape`, `-fanart` and the other suffixes are not artwork. When several of these exist the one the reference chooses depends on the order of the folder's entries.

## <a id="api"></a>Item fields and image routes

- **images.api.R1** An item carries `ImageTags` (kind to tag, 32 hex digits), `BackdropImageTags` (one tag per backdrop) and `ImageBlurHashes` (kind to a map of tag to BlurHash) as objects/arrays that are empty when it has no picture; `GET /Items/{itemId}/Images` (administrator) lists `ImageType`, `ImageIndex` (backdrops only), `ImageTag`, `Path`, `BlurHash`, `Height`, `Width`, `Size`. The tag of a picture changes when its file changes.
- **images.api.R2** `GET /Items/{itemId}/Images/{imageType}/{imageIndex}`: an index that is not a number is **400**, an index that names no picture (`Backdrop/8` of an item with eight, `Primary/1`, a negative index) is **404**, and so is a kind the item has not; without an index the route is index 0.
- **images.api.R3** A request that names a `tag` is answered with `Cache-Control: public, max-age=31536000, immutable`; the tag is not checked against the picture (a wrong tag still answers 200).

## <a id="frames"></a>Pictures taken from the video

- **images.frame.R1** A movie, episode, home video or music video that has no Primary picture of its own gets one taken from its video once its library has been scanned: `ImageTags.Primary` (32 hex digits), an entry in `ImageBlurHashes.Primary` and `PrimaryImageAspectRatio` (width over height of the video) are set, `GET /Items/{itemId}/Images/Primary` answers a JPEG at the video's own pixel size and `GET /Items/{itemId}/Images` lists it with that `Width` and `Height`.
- **images.frame.R3** The same holds for the video extras of an item: local trailers, special features (featurettes, behind the scenes, deleted scenes, interviews, scenes, shorts, samples, clips, unnamed extras), theme videos and the additional parts of a stacked movie, whether they hang off a movie, a series or a season. Each gets a `Primary` picture taken from its video (`ImageTags.Primary`, `ImageBlurHashes.Primary`, `PrimaryImageAspectRatio`, `GET /Items/{id}/Images/Primary` a JPEG at the video's own size, and the entry of `/Images`). Theme songs (audio) get none.
- **images.frame.R2** [untestable: the frame position is approximate and the fixture videos are too uniform to tell frames apart] The frame is taken a little into the video (about a tenth of the way in plus a fraction of a second, never past the end); the exact frame is not part of the contract. A picture file beside the media, or one a provider supplies, takes the place of the frame.

## <a id="cache"></a>Serving a picture more than once

- **images.cache.R1** [untestable: the copy is invisible on the wire — same bytes, same `ETag`, same `Last-Modified` — and the behavior it changes (a source that stops being readable) cannot be expressed as a scenario step] A picture that has been served once is answered from a copy kept next to the database, so a client that scrolls a library does not pull the same picture off the media path again. The copy is keyed by what identifies the picture (item, kind, index, tag) and by the file's size and modification time, so a replaced picture is never served from an older copy; the response still carries the file's own `Last-Modified`. The copies are bounded by a size budget (2 GiB unless configured otherwise, `0` disables) and the least recently used ones are dropped past it. Nothing about the bytes, the status codes or the headers changes: a picture that is not in the library is still `404` (images.api.R2).

## Known differences

Observed on the reference and deliberately not reproduced (not asserted by a scenario that runs against both):

* **BlurHash values.** The number of components follows the picture's aspect ratio like the reference's (about 20, up to 9 along the long side) and the string has the same form; the digits differ slightly because the reference scales the picture before encoding in a way that could not be reproduced (a 10x10 picture comes out identical).
* **Order inside a folder of pictures.** The pictures of `extrafanart/`, and every backdrop of a season or of a plain folder item, come in the order of the folder's entries on the reference; Tinyfin lists them by name (`extrafanart/`) or in the order of R1.
* **Several episode pictures.** For `<episode>-thumb` and `<episode>` of one episode the reference's choice follows the folder's entry order; Tinyfin takes `-thumb` first, then the plain name, then the same two in `metadata/`.
* **Person pictures.** The reference reads none from `.actors/` or `metadata/people/` (an actor has no picture); Tinyfin reads none either, and does not catalog `<actor>` entries of an `.nfo` as people.
* **Extracted frames.** The reference also extracts a `Primary` picture for extras, and so does Tinyfin (`images.frame.R3`). The frame chosen differs slightly from the reference's.
* **Picture decoding.** Tinyfin reads the size of `jpeg`, `png`, `gif` and `webp` content (`tbn` is read as whichever of them it holds) and the BlurHash of the first three; a `webp` picture has `Width`/`Height` but no BlurHash.

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `images.names.R1` | `images.local.extension-order`, `images.local.extensions-recognized`, `images.local.extensions-ignored` |
| `images.names.R2` | `images.local.primary-priority` |
| `images.names.R3`, `R4` | `images.local.kinds` |
| `images.backdrop.R1` | `images.local.backdrops` |
| `images.backdrop.R2` | `images.local.backdrop-duplicate` |
| `images.shared.R1`, `R2` | `images.local.shared-folder` |
| `images.shape.R1` | `images.local.folder-shaped` |
| `images.series.R1` | `images.series.names` |
| `images.season.R1` | `images.season.names` |
| `images.episode.R1` | `images.episode.names` |
| `images.api.R1` | `images.local.kinds`, `images.local.backdrops` |
| `images.api.R2`, `R3` | `images.api.index-lookup` |
| `images.cache.R1` | — (untestable, see the rule) |
| `images.frame.R1` | `images.frame.movie`, `images.frame.episode` |
| `images.frame.R3` | `images.frame.extras-movie`, `images.frame.extras-parts`, `images.frame.extras-series` |
