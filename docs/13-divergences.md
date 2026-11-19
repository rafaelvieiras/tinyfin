# 13 — Divergences from the reference server

> **What this is.** The one index of every place Tinyfin deliberately behaves, reports or looks
> differently from the reference server. Two kinds live here: behavior where the reference server
> is defective and Tinyfin does the correct thing instead, and choices where the reference is not
> defective but Tinyfin goes its own way.
>
> **Why an index, when the detail is elsewhere.** Every difference already has a home — a
> `divergence` record in a contract scenario, a "deliberately not reproduced" section in a spec, a
> decision-log entry, a route note in `11`. What was missing is a single place to see them
> together, which is what makes "is this a difference or a bug?" answerable without reading every
> doc. Each row points at the detail; this file is not the detail.
>
> **The rule (`AGENTS.md`).** A change that introduces, changes or removes a divergence from the
> reference server updates this file **in the same commit**, and the code comment, spec or
> CHANGELOG entry that describes it names its ID here. A divergence that is not in this file is a
> bug or an unfinished review, not a decision.
>
> **Not in scope here:** routes and options that are simply not implemented. Those are tracked as
> gaps in [`11`](11-jellyfin-web-route-coverage.md)'s per-route table (⚠️/❌ rows) and in the
> "Known differences" sections of the contract specs. Internal architecture that never reaches a
> client (how connections are owned, how the scan holds memory) belongs to
> [`12`](12-clean-room-behavior-specs.md), not here.

## How to record one

- **IDs.** `DIV-<n>` for a difference no scenario asserts, numbered in order and never reused. A
  difference the contract *does* assert keeps the scenario's own id as its ID — the scenario is
  executable and stable, and duplicating the name would only create a second thing to keep in
  sync.
- **Which section.** A: the contract asserts it against a live reference and the scenario carries
  a `divergence` record. B: no scenario asserts it, so nothing fails if the behavior drifts —
  these need a human to read them. C: Tinyfin adds or shapes something the reference has no
  equivalent for at all.
- **Machine-checked part.** `go run ./cmd/conformance lint` (in CI) fails when a scenario's
  `divergence` record is missing from section A, so a new reference-defect divergence cannot be
  introduced without landing here. Sections B and C are reviewed by a human.

## A. Reference defects the contract asserts away (scenario-backed)

Every row is a scenario whose `divergence` record states what the reference does, run against the
reference to confirm it, and asserted here as the correct behavior instead.

| Scenario (ID) | What the reference does, and what Tinyfin does instead | Spec |
|---|---|---|
| `admin.config.section-invalid` | The reference answers 500 with a plain-text body for a member of the wrong type in the `metadata` and `xbmcmetadata` sections (a boolean sent as a string, a boolean sent as a number). Tinyfin answers 400 and leaves the section unchanged; a server error for a bad request is not reproduced. | [admin.md](contract/specs/admin.md) |
| `admin.tasks.stop-idle` | The reference answers 500 with a plain-text body when the task is not running, an unhandled error. Stopping a task that is not running is a no-op: 204. | [admin.md](contract/specs/admin.md) |
| `admin.config.hwaccel-invalid` | The reference answers 500 (plain-text 'Error processing request.') for an unknown, empty, null, numeric out-of-range or non-string HardwareAccelerationType, and accepts the numeric spellings 1 and "1" as enum ordinals. Tinyfin refuses every value that is not a known name (case-insensitive, trimmed) with 400 and leaves the section unchanged; a 500 for a bad request is not reproduced. | [admin.md](contract/specs/admin.md) |
| `browse.persons.hidden` | The reference answers 200 (zero counts) for a person that exists only in libraries the user cannot see, which tells that the person exists. | [browse.md](contract/specs/browse.md) |
| `browse.years.total` | The reference reports the number of items it scanned (not the number of years) as TotalRecordCount of GET /Years; a paging client needs the number of years. | [browse.md](contract/specs/browse.md) |
| `chapters.images.path` | The reference puts the server path of every chapter picture in ImagePath, for every caller (a path leak); Tinyfin never does. | [chapters.md](contract/specs/chapters.md) |
| `collections.create.rejected-creates-nothing` | The reference creates the (empty) collection before it rejects an unknown or malformed id; Tinyfin validates first and creates nothing. | [collections.md](contract/specs/collections.md) |
| `collections.create.invalid-input` | The reference answers these with 500 (an unhandled error) and, for the malformed id, leaves the collection behind; Tinyfin answers 400 and creates nothing. | [collections.md](contract/specs/collections.md) |
| `devices.id-suffix.collapses` | The reference stores a DeviceId that carries the account id as a second device; Tinyfin strips the caller's own account id, so one install keeps one entry (devices.R11). | [devices.md](contract/specs/devices.md) |
| `items.scope.ids-restricted-user` | The reference skips the library-access restriction when items are requested by Ids: a user with no library access receives the items, although the single-item read of the same ids is 404. Access restriction must hold on every route that returns items; a restricted user gets an empty result. | [items.md](contract/specs/items.md) |
| `playlists.update.entries` | The reference applies the Ids of an update only in some combinations of members (for a private playlist the entries are left as they were unless Name or Users is sent too). | [playlists.md](contract/specs/playlists.md) |
| `playlists.update.rename` | The reference does not keep a rename sent on its own; a later read of the playlist shows the old name. | [playlists.md](contract/specs/playlists.md) |
| `playlists.users.owner-asks` | The reference answers the owner's own record (CanEdit true) when the owner asks about a shared user. | [playlists.md](contract/specs/playlists.md) |
| `playlists.users.grant` | In the reference a share made with this route is listed but does not give the user edit access until the playlist is updated through POST /Playlists/{id}. | [playlists.md](contract/specs/playlists.md) |
| `playlists.users.unshare` | The reference answers 204 to the removal of a share and keeps the share. | [playlists.md](contract/specs/playlists.md) |
| `playlists.item.metadata-refresh` | The reference refreshes the metadata of a playlist after an entry change only sometimes (the rating and genres can stay as they were). | [playlists.md](contract/specs/playlists.md) |
| `quickconnect.exchange.disabled-account` | The reference issues a working QuickConnect login result for an account that was disabled after its secret was authorized. A disabled account must be refused (403), as on a password login (users.auth.R8). | [quickconnect.md](contract/specs/quickconnect.md) |
| `startup.wizard.no-default-login` | The reference creates an administrator named `root` with an empty password when the user page is opened and lets anyone sign in as it; Tinyfin creates no account before the page's POST (`startup.safety.R1`). | [startup.md](contract/specs/startup.md#safety) |
| `subtitles.stream.auth` | The reference serves the stream routes to anonymous callers (subtitles.stream.R9); Tinyfin requires an authenticated caller, the delivery URL carries the ApiKey. | [subtitles.md](contract/specs/subtitles.md) |
| `users.delete.last-admin-refused` | The reference revokes every token of the target user before validating, so a refused deletion of the only administrator still logs them out. Validation must come first; a refused request must have no side effects. | [users.md](contract/specs/users.md) |
| `users.forgot.pinfile-bare-name` | The reference returns the absolute server path of the PIN file in PinFile, which discloses the server's directory layout. Tinyfin returns only the bare file name. | [users.md](contract/specs/users.md) |
| `users.forgot-pin.throttle` | Tinyfin hardening: redeem attempts are rate limited per source and in total (users.forgot.R9); the reference does not throttle. [untestable as a scenario: it would leave the runner's source address throttled for 15 minutes and break every later redeem from it.] | [users.md](contract/specs/users.md) |
| `users.get.has-password-flag` | The reference reports HasPassword=true (and HasConfiguredPassword=true) for every account, including accounts created without a password or whose password was removed, so the flag carries no information and clients cannot skip the password prompt. The flag must be true exactly when the account has a password. | [users.md](contract/specs/users.md) |
| `users.prefs.null-configuration` | The reference answers 500 when Configuration is an explicit null. Tinyfin treats null like an omitted Configuration: 204 and the preferences reset to their defaults. | [users.md](contract/specs/users.md) |
| `users.quickconnect.secret-single-use` | The reference keeps an authorized secret valid for about ten minutes and returns the same token to every caller that presents it, so a leaked secret can be replayed. A secret must be exchangeable once. | [quickconnect.md](contract/specs/quickconnect.md) |

## B. Differences no scenario asserts

No scenario compares these against a live reference, so nothing fails if they drift; this is the
review list. The spec's own "Observed on the reference and deliberately not reproduced" section
stays the detailed description — a row here says what differs, not why the code that way.

| ID | Area | Difference | Detail |
|---|---|---|---|
| **DIV-1** | Extras | The reference gives every extra a `Primary` picture extracted from its video and dates a `ThemeSong` `0001-01-01`; Tinyfin's extras have no picture and no premiere date. | [extras.md](contract/specs/extras.md) |
| **DIV-2** | Extras | The order of a movie's several media sources follows something the files do not show on the reference; Tinyfin lists the item's own file first, then the others by name. | [extras.md](contract/specs/extras.md) |
| **DIV-3** | Extras | Asking for a transcode of a version starts from the item's own file here; the reference transcodes the named version. Versions are direct-play sources on both. | [extras.md](contract/specs/extras.md) |
| **DIV-4** | Extras | The wire `SortName` of an extra is computed the way the reference does it for a trailer (lower-cased) and a theme song (as it is); Tinyfin normalizes both like any other item. | [extras.md](contract/specs/extras.md) |
| **DIV-5** | Images | A `BlurHash` has the same shape and component count; the digits differ slightly, because the reference scales a picture before encoding in a way that could not be reproduced (a 10x10 picture comes out identical). | [images.md](contract/specs/images.md) |
| **DIV-6** | Images | Inside `extrafanart/`, and for every backdrop of a season or plain folder item, the reference follows the folder's entry order; Tinyfin sorts by name or uses the rule order. | [images.md](contract/specs/images.md) |
| **DIV-7** | Images | Choosing between `<episode>-thumb` and `<episode>` follows the folder's entry order on the reference; Tinyfin takes `-thumb` first, then the plain name, then the same two in `metadata/`. | [images.md](contract/specs/images.md) |
| **DIV-8** | Images | Both extract a `Primary` frame for an extra, but the frame chosen differs slightly. | [images.md](contract/specs/images.md) |
| **DIV-9** | Images | A `webp` picture has `Width`/`Height` but no `BlurHash` here; the reference computes one for every picture it decodes. | [images.md](contract/specs/images.md) |
| **DIV-10** | Items | `RecursiveItemCount` of a folder named like an episode is 0 here (its own children); the reference counts the episode found inside it, although that episode is a child of its season. | [items.md](contract/specs/items.md) |
| **DIV-11** | Items | Which of an episode's several files stands for it, and the order of the rest: the reference's choice is not derivable from the files (the same copies came out in a different order than by name); Tinyfin takes the shortest name, then the first by path, and lists the others by path. A transcode starts from the item's own file. | [items.md](contract/specs/items.md) |
| **DIV-12** | Items | Exotic numbering: `Show E5-6` is season 5 episode 6 on the reference and `Show E5-E6` loses its name; Tinyfin reads episode 5 and keeps the name. Combinations outside items.episodes.R7/R12 were not probed one by one. | [items.md](contract/specs/items.md) |
| **DIV-13** | Items | Release tokens: the list in items.names.R10 is the one probed; a token outside it that the reference also cuts at would keep its text here. | [items.md](contract/specs/items.md) |
| **DIV-14** | Items | Sort-name transliteration keeps a residue (Thai runs, rare Indic signs, supplementary planes) with no closed rule; the table was observed one code point at a time and is not extended by guessing. | [items.md](contract/specs/items.md) |
| **DIV-15** | Items | Collections (`BoxSet`): the remaining differences are listed in that spec's own section. | [collections.md](contract/specs/collections.md) |
| **DIV-16** | Playback | Offer values (`AudioBitrate`, `MaxFramerate`, `<codec>-level`, `AudioSampleRate`) come from what the scanner probed, so an item scanned by an older build reports none until the next incremental scan re-probes it. | [playback.md](contract/specs/playback.md) |
| **DIV-17** | Playback | A copy that starts in the middle of the file: the reference re-runs the key-frame rule from the start position (its segments then contradict its own playlist); Tinyfin keeps cutting at the playlist's plan. | [playback.md](contract/specs/playback.md) |
| **DIV-18** | Playback | A stream bit rate the estimate table does not cover (an audio codec the build cannot encode, `pcm` with an odd sample size) is missing here. | [playback.md](contract/specs/playback.md) |
| **DIV-19** | Playback | Rendering: burn-in is `libass` (text) and `overlay` (PGS/DVD/DVB) through the configured ffmpeg, with fontconfig fonts or `-fonts-dir`/`TINYFIN_FONTS_DIR`; the reference's server-wide fallback-font setting is not used. Verified by Go tests with a real ffmpeg, not a scenario. | [playback.md](contract/specs/playback.md) |
| **DIV-20** | Subtitles | A document generated from WebVTT puts its second cue on layer 1 on the reference, on layer 0 here. | [subtitles.md](contract/specs/subtitles.md) |
| **DIV-21** | Subtitles | ASS header details of an extracted embedded track (font names, a byte-order mark) follow whichever ffmpeg is configured, so they can differ from the reference's build. | [subtitles.md](contract/specs/subtitles.md) |
| **DIV-22** | Subtitles | The reference caches converted documents without all parameters in the key, so a request can be answered with another request's document; Tinyfin has no cache. | [subtitles.md](contract/specs/subtitles.md) |
| **DIV-23** | Subtitles | A `mediaSourceId` written with dashes answers 500 on the reference (it compares the text); Tinyfin folds every id to one spelling before routing (wire.id.R2) and serves it. | [subtitles.md](contract/specs/subtitles.md) |
| **DIV-24** | Subtitles | Which charset a burned-in external subtitle is read in: rules over the bytes here, a statistical detector there, disagreeing on short or ambiguous samples. | [subtitles.md](contract/specs/subtitles.md) |
| **DIV-30** | Configuration | `UseFileCreationTimeForDateAdded` in the `metadata` section is reported as `true` (the file's own timestamp) and that is the only source of `DateAdded` this server has; choosing `false` (the date the library scanned the item in) is stored and served back and changes nothing. The reference honors both, and its file-time option dates an item by the file's *creation* time where this server uses its *modification* time. | [admin.md](contract/specs/admin.md), route row in [`11`](11-jellyfin-web-route-coverage.md) |
| **DIV-32** | Library order | A library name whose leading character is punctuation the sort-name set leaves alone (`_zebra`) is placed differently by the reference on each of its three lists — first in `GET /Library/MediaFolders`, last in `GET /UserViews`, after the digits in the root form of `GET /Items` — so no single rule reproduces all three. Tinyfin orders every list by the sort name, which puts `_zebra` after the digits and before the letters. | Library lists are ordered by the sort name everywhere (`admin.libraries.R4`); the reference's per-route placements were measured on a throwaway server and are not derivable from the name. |
| **DIV-31** | Configuration | Every member of the `xbmcmetadata` section (`UserId`, `ReleaseDateFormat`, `SaveImagePathsInNfo`, `EnablePathSubstitution`, `EnableExtraThumbsDuplication`) is stored and served back with the reference's defaults, and none of them is honored: this server writes no `.nfo` sidecar, so there is no release date to format, no image paths to save and no thumbs to duplicate. The section exists so the dashboard's library NFO page loads and saves instead of failing. | [admin.md](contract/specs/admin.md), route row in [`11`](11-jellyfin-web-route-coverage.md) |
| **DIV-29** | Images | `format` and the `Accept: image/webp` negotiation are not implemented on item pictures: a poster asked for as `?format=png` or by a client that takes WebP is answered in the stored file's own format (JPEG stays JPEG, PNG stays PNG, and a GIF/WebP/TBN source is answered as JPEG). The size members are all implemented. Chapter pictures are the exception — they do implement both (`chapters.md`). | Smaller surface on the route a library screen hits hundreds of times: what a dashboard asks for is a size, and the stored formats are already what it can draw. Recorded so a client asking for WebP reads it as a decision, not a bug. [images.md](contract/specs/images.md), route row in [`11`](11-jellyfin-web-route-coverage.md) |

| **DIV-33** | Cast photos | A credit's photo is downloaded the first time a client asks for it, not during the scan: the scan stores the URL (an NFO credit's `<thumb>`, a provider credit's profile URL) and `BaseItemPerson.PrimaryImageTag` derives from it, so the picture appears on the first `GET /Items/{personId}/Images/Primary`. Measured on the reference: it fetches during the scan, keeps the file under its own metadata directory, and publishes the tag only once the file is there; deleting that file behind its back makes the tag and the picture disappear until the next scan. A client cannot tell the two apart — same tag, same 200, same bytes — but the timing differs, and so do two visible consequences: a URL that fails is advertised here (the tag exists, the image 404s) where the reference advertises nothing, and `ImageBlurHashes.Primary` appears here only once the picture has been downloaded, where the reference has it from the scan. | The scan must work with no network, and a photo nobody looks at is a request the server should not make. `Person.ImageURL` -> `person_images` -> download on the image route; [08](08-library-scanning-and-metadata.md) "Cast photos", [images.md](contract/specs/images.md). |
| **DIV-34** | Cast photos, address guard | Both downloads the server makes on its own go through `internal/imagefetch`: `http`/`https` only, no non-public address unless the operator allowlisted the host, an 8 MiB ceiling, and a body that must sniff as an image. Measured on the reference: an NFO `<thumb>` pointing at a private address (`http://<private-address>:<port>/...`) is fetched without objection, with no scheme check and no size bound. Here it is refused unless `TINYFIN_IMAGE_HOSTS` names that host, and the picture is simply missing. | An `<thumb>` is untrusted input from inside the library — the shape of a server-side request forgery. Treating it as safe because the reference does would be reproducing a defect; [08](08-library-scanning-and-metadata.md) "Cast photos". |

## C. Things the reference has no equivalent for

| ID | Area | What Tinyfin does | Why / where |
|---|---|---|---|
| **DIV-25** | Library card image | A library's picture is composed here — a 16:9 PNG of up to four of the library's own pictures as slanted bands with the library's name over a dark band that fades out above and below, built once after the scan that cataloged them — where the reference generates a single still. The bands take backdrops where an item has one, and the name's face, size and anchor match the reference's images, and a uniform overlay dims the whole card by 38% — that overlay is this project's, because four pictures are busier than one and a bright patch anywhere on the surface hurts the white name. The contract around it matches: `PrimaryImageItemId`, `ImageTags.Primary` / `ImageBlurHashes` / `PrimaryImageAspectRatio` on the library item, a 960x540 PNG, stable across scans, nothing for a library with no artwork. | The maintainer's chosen look; the field and the routes are the reference's. Decision **D16** in [`00`](00-decision-log.md), route notes in [`11`](11-jellyfin-web-route-coverage.md). |
| **DIV-26** | Scan progress | A single-library scan reports on the `RefreshLibrary` task as well as on the library itself (`RefreshStatus`/`RefreshProgress`), so a client watching only the task list sees it running. The reference reports only the library and leaves the task `Idle`, which means the dashboard's "Scan all libraries" button does not spin there for a single-library scan; here it does. | A dashboard with no sign a scan is running is worse than a button that spins. Decision **D15** in [`00`](00-decision-log.md), the "Task progress" note in [`contract/specs/admin.md`](contract/specs/admin.md). |
| **DIV-27** | Realtime activity log | The `ActivityLogEntry` websocket push carries the most recent entries; the reference sends an empty array on every push, apparently using the message only as "something changed, go re-fetch". | No black-box observation depends on the empty payload, and the entries are strictly more useful. [`12`](12-clean-room-behavior-specs.md)'s socket section. |
| **DIV-28** | Backups | `POST /Backup/Restore` validates its input and answers `501`: replacing the database of a running server is not implemented. `POST /Backup/Create` also succeeds while a scan runs, where the reference refuses with a 500. | Restore is documented as "stop the server, copy the file"; the snapshot is consistent at any time. The "Known differences" list in [`contract/specs/admin.md`](contract/specs/admin.md). |
