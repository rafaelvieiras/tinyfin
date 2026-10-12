# Items: listing, querying and single-item read

Contract for the item surface of the Jellyfin 12+ API: the generic item listing (`GET /Items`),
the "continue watching" list (`GET /UserItems/Resume`), per-user item data
(`GET`/`POST /UserItems/{itemId}/UserData`) and the single-item read (`GET /Items/{itemId}`),
plus the two legacy aliases the official web client still calls.

Scenarios: [`../scenarios/items.json`](../scenarios/items.json). Fixture state: [`../fixtures.json`](../fixtures.json).
Rule IDs are stable (`items.<topic>.R<n>`); scenarios reference them through `covers`.

## Fixture facts this spec relies on

* Libraries `Movies` (movies) and `Shows` (tvshows). Items: movies *Alpha Movie* (2015), *Bravo Movie* (2020),
  *Charlie Movie* (2018); series *Test Show* (2019) with seasons 1 and 2 and three episodes (S01E01, S01E02, S02E01).
* No metadata beyond names/years/numbering: no genres, ratings, overviews, people, external ids, subtitles. (Images: the reference extracts a `Primary` image from each movie and episode video, so `ImageTypes=Primary` matches those; series and seasons have none. Image-type scenarios therefore use types nothing has.)
  Videos are 320x240, about 4-6 seconds long.
* Users: `admin`, `alice` (default policy), `nofolders` (`EnableAllFolders=false`, no enabled folders),
  `nodownload`, `disabled`. The `disabled` user cannot authenticate, so no scenario uses it.
* Library ids used as `ParentId` are captured from `GET /UserViews` (libraries sort alphabetically: Movies, Shows).
* Writing scenarios touch only `alice`'s user data and restore flags, position and play count in `cleanup`.
  A last-played date, and a rating or like value, cannot be cleared through the API and may remain.

## Conventions

* JSON member names are PascalCase (`Items`, `TotalRecordCount`, `StartIndex`); query parameter names are
  case-insensitive. Ids are returned as strings.
* Comma-separated lists carry enum values, ids and numbers; repeated keys are also accepted by the server.
* Authentication is `Authorization: MediaBrowser ...` (see the users spec). Anonymous is always **401** in this area.
* "Context user" below is the user whose data and library access shape a response: the token user,
  or `UserId` when an administrator supplies another one.

---

## <a id="list"></a>GET /Items

**Purpose.** Query items the user may see: by parent, type, text, year, played/favorite state, with sorting and paging.

**Auth.** Valid user token. Administrators may act for another user via `UserId`; others may not.

**Request.** Everything is query-string. Parameters (all optional) grouped by effect:

| Group | Parameters | Default | Effect |
|---|---|---|---|
| Scope | `ParentId`, `Recursive`, `UserId` | none / false (true when types given with a library parent) / token user | Where to look and as whom; see list, scope and user rules |
| Types | `IncludeItemTypes`, `ExcludeItemTypes`, `MediaTypes` | empty (no restriction) | Kind filters |
| Text | `SearchTerm`, `NameStartsWith`, `NameStartsWithOrGreater`, `NameLessThan` | none | Name filters / relevance search |
| Identity | `Ids`, `ExcludeItemIds`, `AdjacentTo` | empty | Select by id / neighbours |
| State | `Filters`, `IsPlayed`, `IsFavorite` | empty | Per-user flags |
| Numbers | `Years`, `ParentIndexNumber`, `IndexNumber`, `MinWidth`, `MinHeight`, `MaxWidth`, `MaxHeight`, `IsHd`, `Is4K` | none | Numeric filters |
| Metadata | `Genres`, `Tags`, `OfficialRatings`, `Person`, `HasSubtitles`, `HasOverview`, `HasImdbId`, `HasTmdbId`, `HasParentalRating`, `MinCommunityRating`, `MinPremiereDate`, `ImageTypes`, `IsLocked`, `VideoTypes`, `Is3D`, `MaxOfficialRating`, `MinOfficialRating`, `CollapseBoxSetItems` | none | Neutral on fixtures; see filter rules |
| Order | `SortBy`, `SortOrder` | empty: sort name ascending | See sort rules |
| Paging | `StartIndex`, `Limit`, `EnableTotalRecordCount` | none / none / true | See paging rules |
| Shaping | `Fields`, `EnableUserData`, `EnableImages`, `ImageTypeLimit`, `EnableImageTypes` | none / true / true / none / all | See fields rules |

**Responses.**

| Status | Condition |
|---|---|
| 200 | Normal; body `{Items, TotalRecordCount, StartIndex}` |
| 400 | Malformed or all-zero/unknown `ParentId`; malformed `UserId`; conflicting `Filters`; non-numeric number/bool parameters |
| 401 | Anonymous; `ParentId` library not accessible to the context user |
| 403 | `UserId` of another user by a non-administrator |
| 404 | `UserId` unknown (administrator) |

### Rules: listing and hierarchy

- **items.list.R1** Authentication is required. A request without a valid user token gets **401**.
- **items.list.R2** On success the status is **200** and the body is an object with `Items` (array of item objects), `TotalRecordCount` (integer) and `StartIndex` (integer: the `StartIndex` query value, `0` when absent).
- **items.list.R3** Query parameter names are case-insensitive; unknown parameters are ignored without error.
- **items.list.R4** Library-views listing: when there is no `ParentId` and the request carries no filtering, searching, recursion or id parameters at all, `Items` are the top-level libraries visible to the user, followed by one extra item: the user's `Playlists` folder (`Type` `ManualPlaylistsFolder`, `CollectionType` `playlists`, `IsFolder` true), present for every user whether or not any playlist or library exists. `GET /UserViews` does not include it, and neither does any recursive or filtered query. The listing is thus 3 items for a user with the two fixture libraries (Movies, Shows, Playlists). This form is not sorted and not paged: `Limit`, `SortBy` and `SortOrder` have no effect, `TotalRecordCount` equals the number of items returned, and `StartIndex` is only echoed.
- **items.list.R5** Without `ParentId`, any filtering or searching parameter (`IncludeItemTypes`, `NameStartsWith`, `SearchTerm`, `Years`, ...) turns the request into a recursive query over every library the user may access; `Recursive` need not be sent.
- **items.list.R6** With `ParentId` naming a library: if `IncludeItemTypes` is non-empty and `Recursive` was not supplied, the query is recursive; with no type list and `Recursive` absent or false, only the direct children of the library are returned (for the fixture libraries: the three movies, and the one series).
- **items.list.R7** `Recursive=true` returns every descendant of `ParentId` (library or folder), subject to the other filters; without a type filter this includes items of all types (series, seasons and episodes for a TV library) and the plain `Folder` item that represents the library's media root directory (type `Folder`, named after the directory). The Movies library recursive therefore returns 4 items: the three movies plus that folder; `IncludeItemTypes=Movie` or `Filters=IsNotFolder` narrows it to the three movies.
- **items.list.R8** `ParentId` = a series: non-recursive returns its seasons (an `IncludeItemTypes` value is ignored in this form); recursive returns its episodes and seasons by default, narrowed by `IncludeItemTypes`.
- **items.list.R9** `ParentId` = a season: returns that season's episodes.
- **items.list.R10** `ParentId` that is the all-zero id, malformed, or does not exist is rejected: **400** (malformed id included, via parameter binding).
- **items.list.R11** `ParentId` that names a non-folder item (for example a movie) is not an error (**200**) and the parent is effectively ignored: the request is handled as if `ParentId` were absent but without the Playlists extra. Non-recursive with no filters returns the user's library views (the two libraries); `Recursive=true` returns everything the user may access (movies, series, seasons, episodes and folders); `IncludeItemTypes` filters that global set. A user with no library access gets an empty result.

### Rules: visibility

- **items.scope.R1** A user with access to no libraries gets empty results from every listing form: the recursive queries and searches all return `Items: []` and `TotalRecordCount: 0` with status 200. The one exception is the root listing form without `ParentId` (items.list.R4), which still returns the single `Playlists` folder (`TotalRecordCount` 1); `GET /UserViews` returns none.
- **items.scope.R2** `ParentId` naming a library the user may not access is **401** (not 403, not an empty list), even when `Recursive` is true. A user with access gets the normal listing.
- **items.scope.R3** Library restriction must also hold when items are requested by `Ids`: a restricted user receives no items, consistent with the 404 of the single-item read (items.get.R6). **Divergence:** the reference does not apply the restriction on this path (confirmed at runtime: a user with no library access gets the requested items back with 200, and an administrator using that user's `UserId` gets them too). That is an access-control defect; the scenario asserts the safe result and is skipped on the reference.

### Rules: user resolution

- **items.user.R1** `UserId` absent: the context (accessible libraries, user data) is the authenticated token user.
- **items.user.R2** `UserId` naming another user: allowed only for administrators, who then get results computed in the target user's context (libraries, user data, hidden items). Non-administrators get **403**.
- **items.user.R3** `UserId` equal to the caller's own id, or the all-zero id, behaves exactly as if it were absent.
- **items.user.R4** `UserId` well-formed but unknown: **404** for an administrator; **403** for a non-administrator (the permission check precedes the lookup).
- **items.user.R5** `UserId` malformed: **400**.

### Rules: filtering

- **items.filter.R1** `IncludeItemTypes` (comma-separated item kinds) restricts results to those kinds; `Movie`, `Series`, `Season`, `Episode` are observable with the fixtures; multiple kinds are OR-ed.
- **items.filter.R2** Tokens of a comma-separated enum or id list that cannot be parsed are silently dropped. If every token is dropped the filter disappears and unfiltered results come back (no error).
- **items.filter.R3** `ExcludeItemTypes` removes the listed kinds from the result when `IncludeItemTypes` is empty.
- **items.filter.R4** `ExcludeItemTypes` has no effect when `IncludeItemTypes` is non-empty (including when both list the same kind).
- **items.filter.R5** `MediaTypes` (comma-separated) keeps only items whose media type is in the list: movies and episodes are `Video`; series, seasons and libraries have none; with the fixtures `Audio` matches nothing.
- **items.filter.R6** `Years` (comma-separated integers) keeps items whose production year is in the list; the year comes from the folder name (`Name (Year)`); items without a year never match.
- **items.filter.R7** `NameStartsWith` keeps items whose sort name starts with the value (case-insensitive); `NameStartsWithOrGreater` keeps sort names greater than or equal to the value; `NameLessThan` keeps sort names strictly less than the value; comparison is case-insensitive (the scenarios pin lower-case values only); several name filters combine with AND.
- **items.filter.R8** `ExcludeItemIds` (comma-separated ids) removes those ids from the result.
- **items.filter.R9** `ParentIndexNumber` keeps items whose parent index (season number) equals the value; `IndexNumber` keeps items whose index (episode number) equals the value; both apply on the main route.
- **items.filter.R10** `Filters` accepts `IsFolder`, `IsNotFolder`, `IsPlayed`, `IsUnplayed`, `IsFavorite`, `IsResumable`, `Likes`, `Dislikes`, `IsFavoriteOrLikes`; `IsFolder` keeps folder-like items (series, seasons, libraries, collections); `IsNotFolder` keeps playable leaves.
- **items.filter.R11** A `Filters` list containing a conflicting pair (`IsFolder`+`IsNotFolder`, `IsPlayed`+`IsUnplayed`, `Likes`+`Dislikes`) is rejected with **400**.
- **items.filter.R12** `IsPlayed` / `Filters=IsPlayed` and `Filters=IsUnplayed` select by the requesting (or `UserId`) user's own played flag; the boolean `IsPlayed=true|false` parameter is equivalent. Other users' flags are not visible.
- **items.filter.R13** `IsFavorite=true|false` and `Filters=IsFavorite` select by the user's own favorite flag; `IsFavorite=false` returns the items that are not favorites.
- **items.filter.R14** `Filters=IsResumable` keeps items with a saved playback position for the user (position greater than zero).
- **items.filter.R15** For a folder (a series, a season), the played state is aggregate: it is played only when every leaf below it is played; it is unplayed otherwise.
- **items.filter.R16** `AdjacentTo` returns the given item and its immediate neighbours (previous and next) in the requested order, at most three items; `TotalRecordCount` counts these; an anchor that is not in the candidate set yields an empty result.
- **items.filter.R17** Dimension filters work on the video stream: `IsHd=false` matches width below 1200 (SD), `IsHd=true` width of 1200 or more, `Is4K=true` width of 3800 or more or height of 2100 or more; `Is4K=false` without `IsHd` matches nothing (empty result); `MinWidth`/`MinHeight` keep items at least that large; `MaxWidth`/`MaxHeight` keep items at most that large.
- **items.filter.R18** Metadata filters that nothing in the fixtures satisfies return an empty result with status 200: `Genres`, `Tags`, `OfficialRatings`, `Person`, `HasSubtitles=true`, `HasOverview=true`, `HasImdbId=true`, `HasTmdbId=true`, `MinCommunityRating`, `MinPremiereDate`, `ImageTypes` for types nothing has (`Backdrop`, `Logo`, `Banner`), `IsLocked=true`, `VideoTypes`, `Is3D=true`; `HasOverview=false` keeps items without an overview.
- **items.filter.R19** Parental parameters on unrated items: `MaxOfficialRating` (known or unknown value), `HasParentalRating=false`, `MinOfficialRating` and `CollapseBoxSetItems=true` (no box sets exist) all leave unrated movies in the result.

### Rules: selecting by id

- **items.ids.R1** `Ids` (comma-separated) returns exactly those items that exist (and are visible to the user); unknown ids are skipped; the query is global (no `Recursive` or `ParentId` needed) and can mix types.
- **items.ids.R2** When `Ids` has more than one id and no `SortBy` is given, items come back in the order the ids were requested; an explicit `SortBy` wins.
- **items.ids.R3** Unparseable tokens in `Ids` are dropped while the valid ones are used.
- **items.ids.R4** Other filters still apply to the `Ids` set (for example `IncludeItemTypes`, `Years`).

### Rules: search

- **items.search.R1** `SearchTerm` matches case-insensitively as a substring of the item's name (exact, prefix and inner matches all qualify); the query is recursive over accessible libraries; no match gives an empty list with status 200.
- **items.search.R2** When the search finds results, the order is by relevance (best match first, then sort name ascending) and `SortBy`/`SortOrder` are ignored.
- **items.search.R3** `SearchTerm` combines with `IncludeItemTypes`, `ParentId` and `Recursive`; a `ParentId` limits matches to that subtree.

### Rules: sorting

- **items.sort.R1** Without `SortBy` results are ordered by sort name ascending.
- **items.sort.R2** `SortBy=SortName` (or `Name`) orders by sort name; `SortOrder=Ascending` (default) / `Descending` sets the direction. `SortOrder` omitted means ascending.
- **items.sort.R3** `SortBy=ProductionYear` orders by production year; `SortBy=PremiereDate` orders by premiere date, falling back to the production year when no premiere date is known.
- **items.sort.R4** `SortBy` and `SortOrder` are comma-separated lists aligned by position: `SortOrder[i]` applies to `SortBy[i]`; a key without a counterpart takes `SortOrder[0]` (ascending when `SortOrder` is empty); a `SortOrder` list with more values than there are `SortBy` keys is rejected with **400** (including one key with two order values); if `SortBy` is absent or yields no usable key the order list is not validated (**200**, default order). `ParentIndexNumber` and `IndexNumber` order episodes by season then episode.
- **items.sort.R5** `SortOrder` without a usable `SortBy` (absent, or only unknown tokens) has no effect; the default order applies.
- **items.sort.R6** Ties on the sort key are broken by sort name ascending.
- **items.sort.R7** `SortBy=Random` returns the same set (count and `TotalRecordCount` unchanged) in an unspecified order.

### Rules: paging

- **items.paging.R1** `Limit=n` returns at most n items; `TotalRecordCount` is still the full count of matching items (before paging); `StartIndex` is 0 when not supplied.
- **items.paging.R2** `StartIndex=n` skips n items; the response echoes it in `StartIndex`; a value past the end gives `Items: []` with the full `TotalRecordCount`.
- **items.paging.R3** `Limit=0` returns no items but still reports the full `TotalRecordCount` (on queries that go through paging).
- **items.paging.R4** `EnableTotalRecordCount` defaults to true. With `false`, `TotalRecordCount` equals the number of items in the returned page (not the full count).
- **items.paging.R5** Non-numeric `Limit` or `StartIndex`, and non-boolean `Recursive`, are rejected with **400** (parameter binding).

### Rules: item shape and shaping options

- **items.fields.R1** Item objects carry at least `Id`, `Name`, `ServerId`, `Type`, `IsFolder`; playable items add `MediaType` (`Video` for the fixtures), `RunTimeTicks`; `ProductionYear` comes from the folder name; episodes carry `IndexNumber` and `ParentIndexNumber`. (The full per-item field mapping is outside this area; only the fields exercised here are pinned.)
- **items.fields.R2** Optional fields are returned only when requested through `Fields` (comma-separated); `Path` is absent by default and present with `Fields=Path`; unknown field names are dropped silently.
- **items.fields.R3** `UserData` is included by default for a user context; `EnableUserData=false` omits it. It carries `Played`, `IsFavorite`, `PlayCount`, `PlaybackPositionTicks` for the context user.
- **items.fields.R4** `EnableImages`, `ImageTypeLimit` and `EnableImageTypes` shape image information only; they are accepted in any combination and never change which items match.

---

## <a id="resume"></a>GET /UserItems/Resume

**Purpose.** Items the user started and did not finish ("continue watching"), most recent first.

**Auth.** Valid user token (administrators may use `UserId`).

**Request.** `UserId`, `StartIndex`, `Limit`, `SearchTerm`, `ParentId`, `Fields`, `MediaTypes`, `EnableUserData`,
`ImageTypeLimit`, `EnableImageTypes`, `ExcludeItemTypes`, `IncludeItemTypes`, `EnableTotalRecordCount` (default true),
`EnableImages` (default true), `ExcludeActiveSessions` (default false). There is no `SortBy`, `Filters` or `Recursive`.

**Response.** **200** `{Items, TotalRecordCount, StartIndex}`; **400/403/404** as in the user rules.

- **items.resume.R1** `GET /UserItems/Resume` requires authentication; anonymous gets **401**.
- **items.resume.R2** With nothing in progress the response is **200** with `Items: []`, `TotalRecordCount: 0`, `StartIndex: 0`.
- **items.resume.R3** Items with a saved playback position greater than zero for the user are returned (unplayed/in-progress), restricted to libraries the user may access.
- **items.resume.R4** Order is most recently played first (by last played date, descending); no `SortBy` is honored.
- **items.resume.R5** `StartIndex`/`Limit` page the list; `TotalRecordCount` is the full count unless `EnableTotalRecordCount=false`, when it equals the page size.
- **items.resume.R6** Filters: `MediaTypes`, `IncludeItemTypes`, `ExcludeItemTypes` (only when `IncludeItemTypes` is empty), `ParentId` (subtree), `SearchTerm` (name substring).
- **items.resume.R7** A `ParentId` that does not exist is not an error: **200** with an empty list.
- **items.resume.R8** Results are per user: another user's progress is invisible; a user without library access gets an empty list; an administrator with `UserId` of another user sees that user's progress.
- **items.resume.R9** `UserId`: another user's id by a non-administrator is **403**; unknown id is **404** (administrator); malformed id is **400**; own id behaves as absent.
- **items.resume.R10** `EnableUserData=false` omits `UserData` from the item objects; by default it is present.
- **items.resume.R11** `ExcludeActiveSessions=true` removes items the user is currently playing in an active session; with no active playback it changes nothing.

---

## <a id="userdata-read"></a><a id="userdata-write"></a>GET and POST /UserItems/{itemId}/UserData

**Purpose.** Read or partially update the context user's data for one item (played, favorite, position, rating).

**Auth.** Valid user token. Acting on another user's record requires an administrator
(the reference additionally requires the preference-access permission for non-administrators on their own record, which is on by default).

**Request.** Path `itemId`; query `UserId` (optional). `POST` body (JSON object, all fields optional):
`Rating`, `PlaybackPositionTicks`, `PlayCount`, `IsFavorite`, `Likes`, `LastPlayedDate`, `Played`
(plus ignored echo fields `PlayedPercentage`, `UnplayedItemCount`, `Key`, `ItemId`).

**Response.** **200** user data object `{Played, IsFavorite, PlayCount, PlaybackPositionTicks, Key, ItemId, ...}`
(optional members `Rating`, `Likes`, `LastPlayedDate`, `PlayedPercentage`, `UnplayedItemCount` appear when they have values).
Errors: **400** all-zero item id / malformed body, **401** anonymous, **403** other user, **404** unknown or hidden item / unknown user.

- **items.userdata.R1** Both `GET` and `POST /UserItems/{itemId}/UserData` require authentication; anonymous gets **401**.
- **items.userdata.R2** `GET` for an item the user never touched returns **200** with defaults: `Played` false, `IsFavorite` false, `PlayCount` 0, `PlaybackPositionTicks` 0, and a non-empty `Key`.
- **items.userdata.R3** Item errors (same for `GET` and `POST`): unknown item **404**; item the user cannot see **404**; all-zero item id **400**.
- **items.userdata.R4** `UserId`: omitted or own id acts on the token user; another user's id is **403** for non-administrators and allowed for administrators (reading or writing that user's record); unknown user id is **404** (administrator).
- **items.userdata.R5** User data is stored per user and item: a write by one user is not visible to another.
- **items.userdata.R6** `POST` body is a partial update; these fields are applied when present and non-null: `Played`, `IsFavorite`, `PlaybackPositionTicks`, `PlayCount`, `LastPlayedDate`, `Rating`, `Likes`. The response is **200** with the resulting user data (same shape as `GET`).
- **items.userdata.R7** Played, favorite and resume state written here is reflected by item listings (`UserData` on item objects and the `IsPlayed`, `IsFavorite`, `IsResumable` filters) for that user only.
- **items.userdata.R8** For folders the user data in a listing carries `UnplayedItemCount`, the number of unplayed leaf items below it (a series with three unplayed episodes reports 3, and 2 after one is played).
- **items.userdata.R9** `Played=true` through this endpoint only sets the flag: it does not increment `PlayCount` and does not touch the playback position.
- **items.userdata.R10** Fields not present in the body keep their stored values; an empty JSON object changes nothing.
- **items.userdata.R11** `Key`, `ItemId`, `PlayedPercentage` and `UnplayedItemCount` may appear in the body (they are part of the response shape) but are ignored; the response `Key`/`ItemId` are those of the item.
- **items.userdata.R12** Malformed JSON body is **400**.
- **items.userdata.R13** Writing user data on a folder (series, season) does not propagate to its children.

---

## <a id="get"></a>GET /Items/{itemId}

**Purpose.** Read one item as a complete item object.

**Auth.** Valid user token; `UserId` query as in the user rules.

**Response.** **200** item object; **401** anonymous; **403** `UserId` of another user (non-administrator);
**404** unknown or invisible item, or unknown user (administrator).

- **items.get.R1** `GET /Items/{itemId}` requires authentication; anonymous gets **401**.
- **items.get.R2** Success is **200** with a single item object (not wrapped in `Items`/`TotalRecordCount`) and the full field set: `UserData` for the context user and `Path` included.
- **items.get.R3** The same item fields as in listings: `Id`, `Name`, `Type`, `IsFolder`, `ProductionYear`, `MediaType` for movies; series have `IsFolder` true; episodes have `IndexNumber`/`ParentIndexNumber`; for a series `UserData.UnplayedItemCount` is the number of unplayed episodes.
- **items.get.R4** Unknown item id is **404**; the body is a problem document (JSON object with `type`, `title`, `status` = 404 and a trace id), not empty.
- **items.get.R5** The all-zero item id is not an error: it returns the user's root folder (`IsFolder` true), also for users without library access.
- **items.get.R6** An item that exists but is not visible to the context user (library not accessible) is **404** with the same problem document body, not 403.
- **items.get.R7** `UserId` query: omitted, own or all-zero id acts as the token user; another user's id is **403** for non-administrators and gives the target user's visibility and user data for administrators; unknown id is **404** for an administrator and **403** for a non-administrator.
- **items.get.R8** `UserData` reflects the context user's own data: another user's favorites/played state are not shown.
- **items.get.R9** List-shaping parameters (`Fields`, `EnableUserData`, ...) do not apply to a single read; the item is always returned in full.

---

## <a id="legacy"></a>Legacy aliases (scope `legacy-web-required`)

The official web client's embedded API client still calls these two routes. They are specified only as aliases
of the modern ones; drop them when the client stops using them.

| Route | Modern equivalent | What differs |
|---|---|---|
| `GET /Users/{userId}/Items` | `GET /Items?UserId={userId}` | user id in the path (not optional); no `IndexNumber`, `AudioLanguages`, `SubtitleLanguages` |
| `GET /Users/{userId}/Items/{itemId}` | `GET /Items/{itemId}?UserId={userId}` | user id in the path; no query parameters |

- **items.legacy.R1** `GET /Users/{userId}/Items` is a thin alias of `GET /Items` with `UserId` taken from the path: same filters, sorting, paging, result shape and `StartIndex` echo. (`legacy-web-required`.)
- **items.legacy.R2** Same user rules as `GET /Items`: anonymous **401**; path user other than the caller is **403** for non-administrators; administrators get the target user's context; unknown user **404**.
- **items.legacy.R3** The legacy route does not take `IndexNumber`: it is ignored there (all episodes match) while `GET /Items` honors it. `AudioLanguages` and `SubtitleLanguages` are likewise not accepted.
- **items.legacy.R4** `GET /Users/{userId}/Items/{itemId}` is an alias of `GET /Items/{itemId}` with `UserId` from the path: same body, 404 for unknown/hidden items, root folder for the all-zero item id.
- **items.legacy.R5** Same user rules on the single-item alias: anonymous **401**; non-administrator with another user in the path **403**; administrator allowed; unknown user in the path **404**.

---

## Excluded legacy routes (not specified, see `scope-excluded.md`)

`GET /Users/{userId}/Items/Resume` (use `GET /UserItems/Resume`),
`GET`/`POST /Users/{userId}/Items/{itemId}/UserData` (use `/UserItems/{itemId}/UserData`).

## Out of scope parameters (legacy behaviors, per the scope rules)

`IsMissing`, `MinCriticRating`, `LocationTypes`, `ExcludeLocationTypes` and the pipe-separated form of the name lists
(`Genres`, `OfficialRatings`, `Tags`, `Studios`, `Artists`, `Albums`) are legacy and are not part of the contract.
Name-list parameters are used here with a single value only.

## Not exercisable with the fixtures

These parameters are accepted but nothing observable can be asserted with the available data
(no such metadata, plugins, Live TV, music or box sets exist):

`HasThemeSong`, `HasThemeVideo`, `HasSpecialFeature`, `HasTrailer`, `HasTvdbId`, `IsUnaired`,
`MinDateLastSaved`, `MinDateLastSavedForUser`, `MaxPremiereDate`, `IsMovie`, `IsSeries`, `IsNews`, `IsKids`, `IsSports`,
`PersonIds`, `PersonTypes`, `Studios`, `StudioIds`, `GenreIds`, `Artists`, `ArtistIds`, `ExcludeArtistIds`,
`AlbumArtistIds`, `ContributingArtistIds`, `Albums`, `AlbumIds`, `IsPlaceHolder`, `SeriesStatus`,
`AudioLanguages`, `SubtitleLanguages`, `Filters=Likes|Dislikes|IsFavoriteOrLikes` (only the conflict check is covered),
`SortBy=DatePlayed|PlayCount|...` user-ordering keys, BoxSet/Playlist/Collection behaviors, channel and Live TV parents,
special library views (`UserView` types such as latest, next-up, genres), remote-access and parental-schedule denial,
API-key (no user) access. Naming a studio or an artist in a query also creates the by-name entity as a side effect in the
reference; that is deliberately left unexercised.

## Open questions

* **Library restriction on `Ids` (items.scope.R3).** Confirmed at runtime on the reference: the restriction is
  not applied (security defect). Recorded as a divergence in the scenario; it belongs in the security findings.
* **Dimension filters (items.filter.R17)** assume the media files in the fixtures are 320x240; the values come from the
  media generation script, not from the reference.
* **`PremiereDate` ordering fallback (items.sort.R3)** is taken from the audit's description of the order mapping;
  verify against the reference.
* **`UserId` all-zero on the legacy single-item alias.** The audit notes both "falls back to the token user"
  (shared helper) and "400" (legacy note); no scenario asserts it.
* **Non-GUID item id in a route** (`/Items/not-a-guid`): the framework's binding result (400 vs 404) is not pinned
  by the audit; not asserted.
* **Sort name of items with leading articles** and non-ASCII names: not observable with the fixtures.

---

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `items.list.R1` | `items.list.anonymous` |
| `items.list.R2` | `items.list.shape-recursive-movies` |
| `items.list.R3` | `items.list.unknown-param-ignored`, `items.list.param-names-case-insensitive` |
| `items.list.R4` | `items.list.root-views`, `items.list.root-views-ignore-paging` |
| `items.list.R5` | `items.list.root-filter-is-recursive` |
| `items.list.R6` | `items.list.parent-library-direct-children`, `items.list.parent-library-types-default-recursive` |
| `items.list.R7` | `items.list.parent-library-recursive` |
| `items.list.R8` | `items.list.series-children` |
| `items.list.R9` | `items.list.season-children` |
| `items.list.R10` | `items.list.parent-invalid` |
| `items.list.R11` | `items.list.parent-not-folder` |
| `items.scope.R1` | `items.scope.no-libraries-user` |
| `items.scope.R2` | `items.scope.parent-library-forbidden` |
| `items.scope.R3` | `items.scope.ids-restricted-user` |
| `items.user.R1` | `items.user.own-and-empty-user-id` |
| `items.user.R2` | `items.user.other-user-forbidden`, `items.user.admin-other-user-context` |
| `items.user.R3` | `items.user.own-and-empty-user-id` |
| `items.user.R4` | `items.user.unknown-user` |
| `items.user.R5` | `items.user.malformed-user-id` |
| `items.filter.R1` | `items.filter.include-item-types` |
| `items.filter.R2` | `items.filter.include-types-invalid-token` |
| `items.filter.R3` | `items.filter.exclude-item-types` |
| `items.filter.R4` | `items.filter.exclude-ignored-when-include-set` |
| `items.filter.R5` | `items.filter.media-types` |
| `items.filter.R6` | `items.filter.years` |
| `items.filter.R7` | `items.filter.name-ranges` |
| `items.filter.R8` | `items.filter.exclude-item-ids` |
| `items.filter.R9` | `items.filter.index-numbers` |
| `items.filter.R10` | `items.filter.filters-folder` |
| `items.filter.R11` | `items.filter.filters-conflict` |
| `items.filter.R12` | `items.filter.played-and-unplayed` |
| `items.filter.R13` | `items.filter.favorites` |
| `items.filter.R14` | `items.filter.resumable` |
| `items.filter.R15` | `items.filter.folder-played-aggregate` |
| `items.filter.R16` | `items.filter.adjacent-to` |
| `items.filter.R17` | `items.filter.dimensions` |
| `items.filter.R18` | `items.filter.metadata-absent` |
| `items.filter.R19` | `items.filter.parental-neutral` |
| `items.ids.R1` | `items.ids.by-id` |
| `items.ids.R2` | `items.ids.order-preserved` |
| `items.ids.R3` | `items.ids.invalid-token-dropped` |
| `items.ids.R4` | `items.ids.with-filters` |
| `items.search.R1` | `items.search.term` |
| `items.search.R2` | `items.search.ignores-sort` |
| `items.search.R3` | `items.search.scoped` |
| `items.sort.R1` | `items.sort.default-order` |
| `items.sort.R2` | `items.sort.sort-name` |
| `items.sort.R3` | `items.sort.production-year` |
| `items.sort.R4` | `items.sort.multi-key` |
| `items.sort.R5` | `items.sort.order-without-key` |
| `items.sort.R6` | `items.sort.tie-break` |
| `items.sort.R7` | `items.sort.random` |
| `items.paging.R1` | `items.paging.limit` |
| `items.paging.R2` | `items.paging.start-index` |
| `items.paging.R3` | `items.paging.limit-zero` |
| `items.paging.R4` | `items.paging.total-record-count-flag` |
| `items.paging.R5` | `items.paging.malformed` |
| `items.fields.R1` | `items.fields.movie-basics`, `items.fields.series-and-episode` |
| `items.fields.R2` | `items.fields.path-on-request` |
| `items.fields.R3` | `items.fields.enable-user-data`, `items.userdata.reflected-in-listing` |
| `items.fields.R4` | `items.fields.image-options-accepted` |
| `items.resume.R1` | `items.resume.anonymous` |
| `items.resume.R2` | `items.resume.empty` |
| `items.resume.R3` | `items.resume.lists-in-progress`, `items.resume.played-and-completed-excluded` |
| `items.resume.R4` | `items.resume.lists-in-progress` |
| `items.resume.R5` | `items.resume.paging` |
| `items.resume.R6` | `items.resume.filters` |
| `items.resume.R7` | `items.resume.unknown-parent-is-empty` |
| `items.resume.R8` | `items.resume.per-user` |
| `items.resume.R9` | `items.resume.user-id` |
| `items.resume.R10` | `items.resume.user-data-option` |
| `items.resume.R11` | `items.resume.exclude-active-sessions` |
| `items.userdata.R1` | `items.userdata.get-anonymous`, `items.userdata.post-anonymous` |
| `items.userdata.R2` | `items.userdata.get-defaults` |
| `items.userdata.R3` | `items.userdata.get-item-errors`, `items.userdata.post-errors` |
| `items.userdata.R4` | `items.userdata.get-user-id`, `items.userdata.post-errors`, `items.userdata.post-admin-for-other-user` |
| `items.userdata.R5` | `items.userdata.get-reflects-writes` |
| `items.userdata.R6` | `items.userdata.post-played`, `items.userdata.post-fields` |
| `items.userdata.R7` | `items.filter.played-and-unplayed`, `items.userdata.reflected-in-listing` |
| `items.userdata.R8` | `items.userdata.folder-unplayed-count` |
| `items.userdata.R9` | `items.userdata.post-played` |
| `items.userdata.R10` | `items.userdata.post-partial-merge` |
| `items.userdata.R11` | `items.userdata.post-ignored-fields` |
| `items.userdata.R12` | `items.userdata.post-errors` |
| `items.userdata.R13` | `items.userdata.post-folder-no-propagation` |
| `items.get.R1` | `items.get.anonymous` |
| `items.get.R2` | `items.get.movie`, `items.get.series-and-episode` |
| `items.get.R3` | `items.get.movie`, `items.get.series-and-episode` |
| `items.get.R4` | `items.get.not-found` |
| `items.get.R5` | `items.get.root` |
| `items.get.R6` | `items.get.hidden-from-user` |
| `items.get.R7` | `items.get.user-id` |
| `items.get.R8` | `items.get.user-data-per-user` |
| `items.get.R9` | `items.get.full-dto-ignores-list-options` |
| `items.legacy.R1` | `items.legacy.items-alias` |
| `items.legacy.R2` | `items.legacy.items-user-rules` |
| `items.legacy.R3` | `items.legacy.items-no-indexnumber` |
| `items.legacy.R4` | `items.legacy.item-alias` |
| `items.legacy.R5` | `items.legacy.item-user-rules` |
