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
* No metadata beyond names/years/numbering: no genres, ratings, overviews, people, external ids, subtitles.
  The only exceptions are lazy libraries that exist just around the scenarios requiring them: `Rated` (ten
  movies whose sidecar `.nfo` carries `G`, `PG`, `PG-13`, `R`, `NC-17`, `TV-MA`, `TV-14`, `12`, `ZZ-99` and no
  rating), `RatedShows` (a `TV-MA` series, one episode without a rating, one with `TV-Y`) and `Many` (150 tiny
  movies named `Bulk NNN`). Policy users `rating10` (`MaxParentalRating` 10), `rating17`, `rating17s1`
  (`MaxParentalSubRating` 1) and `blockunrated` (`BlockUnratedItems=[Movie]`) are fixtures. (Images: the reference extracts a `Primary` image from each movie and episode video, so `ImageTypes=Primary` matches those; series and seasons have none. Image-type scenarios therefore use types nothing has.)
  Videos are 320x240, about 4-6 seconds long.
* Further lazy libraries, each created only around the scenarios that require it: `Trim` (movies whose `.nfo`
  titles carry surrounding white space), `TrimShows` (series with white-space titles, named season, `<runtime>`
  values), `Loose` (series with specials, loose files in season folders and files in the series folder) and
  `Home` (a `homevideos` library: nested folders of videos with no naming scheme, a few ignored entries). Their
  `.nfo` files start with a UTF-8 byte order mark, as the reference's own writer produces them.
* Lazy libraries of the artwork and extras specs (`images.md`, `extras.md`): `Art`, `ArtShows`, `Extras`,
  `ExtraShows`; and `Resid` (series with season folders that share a number, a folder named like an episode,
  several files of one episode and episode numbering without a pattern) and `PhotoTypes` (one picture per
  extension of a home-videos album).
* Users: `admin`, `alice` (default policy), `nofolders` (`EnableAllFolders=false`, no enabled folders),
  `nodownload`, `disabled`. The `disabled` user cannot authenticate, so no scenario uses it.
* Library ids used as `ParentId` are captured from `GET /UserViews` (libraries are ordered by their sort name, so
  Movies comes before Shows: admin.libraries.R4).
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

### <a id="views"></a>Rules: user views and who may ask for them

- **items.views.R1** `GET /Users/{userId}/Views` is the legacy form of `GET /UserViews`: the same items in the same order (admin.libraries.R4) for the user in the path. A caller may ask for their own views (**200**) and an administrator for any user's (**200**); another user's views are **403**, a user identifier that names nobody is **404**, and an anonymous request is **401**.
- **items.views.R2** `GET /UserViews?userId=` follows the same authorization, with one difference: no `userId` and the caller's own are **200**, another user's is **403** for a non-administrator (**200** for an administrator), and a `userId` that names nobody is **403** rather than `404` — the route does not tell an unknown identifier from a foreign one.

- **items.views.R6** The account's `Configuration.OrderedViews` (the order the preferences page saves with its up/down buttons and `POST /Users/{userId}/Configuration`) orders **both** view routes: the views it names come first, in its own order, and every view it does not name keeps the default order (admin.libraries.R4) behind them. An identifier that names no view is ignored, and so is a repeat. It changes nothing else: `GET /UserViews/GroupingOptions` keeps its name order (items.grouping.R3), and `GET /Library/MediaFolders`, `GET /Library/VirtualFolders` and the `GET /Items` root listing keep theirs. Measured against the reference.
- **items.views.R7** The account's `Configuration.MyMediaExcludes` (the "Include in My Media" checkboxes) takes those libraries out of both view routes. `includeHidden=true` lists them again — on both routes, in the `OrderedViews` order — and `includeHidden=false` behaves like leaving it out. The parameter binds like a boolean and nothing else: `true`/`false` in any capitalization are accepted, and any other value (`1`, `yes`, an empty value) is **400** with a validation problem document. It changes only the two view routes: `GET /UserViews/GroupingOptions` keeps offering the library and so does `GET /Library/MediaFolders` (the latter is an administrator route, so it cannot answer for another user's preference anyway), and the "recently added" row is unaffected (measured on the reference; `TestGroupingOptionsIgnoreMyMediaExcludes` in `internal/api/library_prefs_test.go`).

### <a id="grouping"></a>Rules: grouping options

- **items.grouping.R1** `GET /UserViews/GroupingOptions` lists the library folders the caller may group into one view: an array of `{Name, Id}`, with `Id` a 32-hex identifier and no dashes. A server with no groupable library answers `[]` — an empty array, never `null` — and an anonymous request is **401**.
- **items.grouping.R2** Only the groupable collection types are listed: `movies`, `tvshows`, and a library with no collection type at all (created without one, which is also how the reference stores a photos library). `music`, `musicvideos`, `homevideos` and `books` are not listed — measured against the reference one library of each type at a time. `boxsets` and `playlists` are not library folders and never appear.
- **items.grouping.R3** [untestable: it needs two libraries whose names order differently under the two comparators, and a scenario cannot create them without leaving them behind for every other scenario on the shared server; covered by a Go test] The order is by `Name`, compared case-insensitively and after transliteration. That is **not** the sort-name order `GET /UserViews` and `GET /Library/VirtualFolders` use: on the reference, two libraries named `10 Coisas` and `9 Vidas` come out `10 Coisas` first here, where the sort-name order (`0000000009 vidas` before `0000000010 coisas`) puts `9 Vidas` first, and `zebra` comes before `ZZZ`. The Go test is `TestGroupingOptionsFilterAndOrder` in `internal/api/library_grouping_test.go`.
- **items.grouping.R4** `GET /Users/{userId}/GroupingOptions` is the legacy form and answers the same body. Authorization follows `GET /Users/{userId}/Views` (items.views.R1) with one measured difference: an identifier that names nobody is **200** with the caller's own list, not **404**. A value that is not an identifier at all is **400** (a validation problem document), another user's options are **403** unless the caller is an administrator, and an anonymous request is **401**.

### Rules: visibility

- **items.scope.R1** A user with access to no libraries gets empty results from every listing form: the recursive queries and searches all return `Items: []` and `TotalRecordCount: 0` with status 200. The one exception is the root listing form without `ParentId` (items.list.R4), which still returns the single `Playlists` folder (`TotalRecordCount` 1); `GET /UserViews` returns none.
- **items.scope.R2** `ParentId` naming a library the user may not access is **401** (not 403, not an empty list), even when `Recursive` is true. A user with access gets the normal listing.
- **items.scope.R3** Library restriction must also hold when items are requested by `Ids`: a restricted user receives no items, consistent with the 404 of the single-item read (items.get.R6). **Divergence:** the reference does not apply the restriction on this path (confirmed at runtime: a user with no library access gets the requested items back with 200, and an administrator using that user's `UserId` gets them too). That is an access-control defect; the scenario asserts the safe result and is skipped on the reference.

### Rules: parental rating restrictions

An item's *rating* is its `OfficialRating` text. A user is restricted by the policy fields `MaxParentalRating`
(integer, with optional `MaxParentalSubRating`) and `BlockUnratedItems` (list of item groups), and a listing
can be bounded with the `MaxOfficialRating` / `MinOfficialRating` query parameters. Verified black-box against
the 12.2 reference with fixture items whose rating comes from a sidecar `.nfo` (`<mpaa>`).

- **items.rating.R1** Rating text maps to a *score* `(value, sub)`, compared by value and then sub. The table
  (the same one `GET /Localization/ParentalRatings` publishes, case-insensitive) is: `G`, `TV-G`, `TV-Y`,
  `Approved` = 0/0; `TV-Y7` = 7/0, `TV-Y7-FV` = 7/1; `PG`, `TV-PG` = 10/0 (`TV-PG-D`, `-L`, `-S`, `-V` and
  combinations = 10/1); `PG-13` = 13/0; `TV-14` = 14/0 (with `-D/-L/-S/-V` combinations 14/1); `R` = 17/0;
  `NC-17` = 17/1; `TV-MA` = 17/1 (with `-L/-S/-V` combinations 17/1); `TV-X`, `TV-AO` = 18/0; `21` = 21/0;
  `XXX` = 1000/0; `Banned` = 1001/0. Text not in the table is resolved by, in order: removing a leading
  `Rated `; removing a country prefix up to the first `:` or `-` and looking the rest up in the table or reading
  it as a number (`US:PG` = 10, `DE-12` = 12, `ZZ-99` = 99); reading the whole text as a number, ignoring a
  trailing `+` (`12` = 12, `16+` = 16). Anything else, the empty text and `Unrated` have **no value**: the item
  is *unrated*.
- **items.rating.R2** With `MaxParentalRating` set, listings, searches and every other item list hide a rated
  item whose value exceeds it; unrated items stay visible (an unrecognized rating text is unrated).
- **items.rating.R3** At the same value the sub rating decides: the maximum's sub rating defaults to 0
  (`MaxParentalSubRating`), so with `MaxParentalRating=17` an `R` item (17/0) is visible and `NC-17`/`TV-MA` (17/1)
  are not until `MaxParentalSubRating` is 1.
- **items.rating.R4** `BlockUnratedItems` hides unrated items of the listed groups: `Movie` covers movies;
  `Series` covers series, seasons and episodes; `Trailer`; `Music` (artists, albums, songs); `Other` (everything
  else, library folders included in listings). An item whose rating is inherited (R6) is rated. Without
  `MaxParentalRating` the block still applies.
- **items.rating.R5** `MaxOfficialRating` keeps rated items with a score at most the parameter's (value, then sub:
  `R` excludes `NC-17` and `TV-MA`; a number resolves as in R1: `12` keeps `PG` and the `12` item, `10` equals `PG`);
  `MinOfficialRating` keeps rated items with a score at least the parameter's. Unrated items always pass both, and
  a value that has no score (`Unrated`, unrecognized) filters nothing. The two combine with each other and with
  the policy. (Leaves `items.filter.R19` intact: unrated items stay.)
- **items.rating.R6** An item with no rating of its own (an episode or season) uses its series' rating. An item
  with its own rating keeps it for listing purposes.
- **items.rating.R7** Reading one item (`GET /Items/{itemId}`, `GET /Users/{userId}/Items/{itemId}`) that the
  restrictions hide answers the same `404` problem document as a missing item. Differences from the listing
  filter, kept for compatibility: only the rating *value* is compared (so at maximum 17 an `NC-17`/`TV-MA`
  item is readable although the listing hides it), an item inside a series is also refused when the series' own
  rating value exceeds the maximum (an episode with its own `TV-Y` in a `TV-MA` series is `404` at maximum 10 while
  the listing still shows it), and `BlockUnratedItems` applies to movies, series, seasons, episodes, trailers and
  music but not to library folders (a library is readable although `Other` is blocked).
- **items.rating.R8** The same scope (library, tags, ratings) applies to the other item read paths: item lists
  of `GET /Items/Latest`, `GET /UserItems/Resume` and the counts of `GET /Items/Counts`; the single-item
  reads `GET /Items/{id}/Similar`, `/Ancestors`, `/PlaybackInfo`, the stream routes and
  `GET /UserItems/{id}/UserData` answer `404` for a hidden item; `GET /Shows/{seriesId}/Seasons` and `/Episodes`
  answer `404` for a hidden series.

> `GET /Shows/NextUp` is scoped the same way in Tinyfin; the reference's answer could not be observed with the
> fixtures (nothing is in progress), so it is not part of R8.
>
> Tinyfin's own notes: the rating is evaluated at query time, so a changed `OfficialRating` or policy applies
> immediately. The table is the US one the reference serves by default; the reference ignores a `countryCode`
> query on `GET /Localization/ParentalRatings`, so no country-specific table is specified.

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
- **items.filter.R17** Dimension filters work on the video stream. `IsHd` and `Is4K` pick resolution buckets and the picked buckets are united: `IsHd=false` is the SD bucket (width below 1200), `IsHd=true` the HD bucket (width of 1200 or more that is **not** 4K, so a 3840-wide item is not matched by `IsHd=true`), `Is4K=true` the 4K bucket (width of 3800 or more, or height of 2100 or more). `Is4K=false` picks no bucket: alone it matches nothing (empty result), together with `IsHd` it changes nothing. A narrow, tall item (for example 100x2100) is in both the SD and the 4K bucket. `MinWidth`/`MinHeight` keep items at least that large; `MaxWidth`/`MaxHeight` keep items at most that large (inclusive bounds). Observed on the reference with 320x240, 1280x720, 3840x2160 and edge-size clips (1198x100, 1200x100, 3800x100, 100x2100, 1300x2200); `scripts/conformance-media.sh` generates them.
- **items.filter.R18** Metadata filters that nothing in the fixtures satisfies return an empty result with status 200: `Genres`, `Tags`, `OfficialRatings`, `Person`, `HasSubtitles=true`, `HasOverview=true`, `HasImdbId=true`, `HasTmdbId=true`, `MinCommunityRating`, `MinPremiereDate`, `ImageTypes` for types nothing has (`Backdrop`, `Logo`, `Banner`), `IsLocked=true`, `VideoTypes`, `Is3D=true`; `HasOverview=false` keeps items without an overview.
- **items.filter.R19** Parental parameters on unrated items: `MaxOfficialRating` (known or unknown value), `HasParentalRating=false`, `MinOfficialRating` and `CollapseBoxSetItems=true` (no box sets exist) all leave unrated movies in the result.
- **items.filter.R20** The boolean `IsFolder` query parameter has no effect: `IsFolder=true`, `IsFolder=false` and an unparsable value (`IsFolder=maybe`) all return the same set as the request without it (status 200), and on its own it does not turn the library-views listing into a query. Folders are selected through `Filters=IsFolder` / `Filters=IsNotFolder` (items.filter.R10).

### Rules: selecting by id

- **items.ids.R1** `Ids` (comma-separated) returns exactly those items that exist (and are visible to the user); unknown ids are skipped; the query is global (no `Recursive` or `ParentId` needed) and can mix types.
- **items.ids.R2** When `Ids` has more than one id and no `SortBy` is given, items come back in the order the ids were requested; an explicit `SortBy` wins.
- **items.ids.R3** Unparseable tokens in `Ids` are dropped while the valid ones are used.
- **items.ids.R4** Other filters still apply to the `Ids` set (for example `IncludeItemTypes`, `Years`).

### Rules: search

- **items.search.R1** `SearchTerm` matches as a substring of the item's name (exact, prefix and inner matches all qualify) without regard to case, accents or punctuation, and by the rules of search.hints.R3 (search.md owns them); the query is recursive over accessible libraries; no match gives an empty list with status 200. Without a type list it also finds genre, studio and person names (search.items.R1).
- **items.search.R2** When the search finds results, the order is by relevance (search.hints.R5: starts with, ends a word, seasons and episodes, the rest; then sort name ascending) and `SortBy`/`SortOrder` are ignored.
- **items.search.R3** `SearchTerm` combines with `IncludeItemTypes`, `ParentId` and `Recursive`; a `ParentId` limits matches to that subtree.

### Rules: sorting

- **items.sort.R1** Without `SortBy` results are ordered by sort name ascending.
- **items.sort.R2** `SortBy=SortName` (or `Name`) orders by sort name; `SortOrder=Ascending` (default) / `Descending` sets the direction. `SortOrder` omitted means ascending.
- **items.sort.R3** `SortBy=ProductionYear` orders by production year (the premiere date plays no part: an item with year 2005 and premiere date 2019 sorts at 2005; ties follow R6). `SortBy=PremiereDate` orders by premiere date; an item with no premiere date is ordered as if it premiered on January 1st of its production year (a year-only 2012 item precedes a 2012-06-15 premiere and follows a 2011 one).
- **items.sort.R4** `SortBy` and `SortOrder` are comma-separated lists aligned by position: `SortOrder[i]` applies to `SortBy[i]`; a key without a counterpart takes `SortOrder[0]` (ascending when `SortOrder` is empty); a `SortOrder` list with more values than there are `SortBy` keys is rejected with **400** (including one key with two order values); if `SortBy` is absent or yields no usable key the order list is not validated (**200**, default order). `ParentIndexNumber` and `IndexNumber` order episodes by season then episode.
- **items.sort.R5** `SortOrder` without a usable `SortBy` (absent, or only unknown tokens) has no effect; the default order applies.
- **items.sort.R6** Ties on the sort key are broken by sort name ascending.
- **items.sort.R7** `SortBy=Random` returns the same set (count and `TotalRecordCount` unchanged) in an unspecified order.
- **items.sort.R8** The sort name of an item (what `SortName` carries when requested, what `SortBy=SortName`/`Name`, the default order and the tie-break use, and what `NameStartsWith`, `NameStartsWithOrGreater` and `NameLessThan` compare against) is derived from its title, or from an explicit sort title in its metadata, in this order: (1) the articles `the`, `a` and `an` (in that order, any case) are each removed as whole words wherever they stand between spaces: at the start when a space follows, between two spaces (the two spaces collapse into one, so `Hello - A World` gives `hello  world`), and at the end when a space precedes (`The Matrix` gives `matrix`, `Return of the King` gives `return of king`, `2001 A Space Odyssey` gives `0000002001 space odyssey`, `A Tale of an Apple` gives `tale of apple`, `Out of The` gives `out of`); a title that is only the article is kept (`The` gives `the`, `The The The` gives `the`), and the removal happens before the next step, so `The-Thing`, `A-Team`, `Hello.The.World` and `'The'` between quotes do not lose an article; (2) the characters `,` `&` `-` `{` `}` `'` are deleted and `.` `+` `%` become a space (`Dr. Who` gives `dr  who`, `Rock-n-Roll` gives `rocknroll`); (3) every run of decimal digits shorter than ten digits is left-padded with ASCII zeros to ten (`Movie 2` gives `movie 0000000002`, so 2 orders before 10; `Movie 1.5` gives `movie 0000000001 0000000005`); longer runs are unchanged; a character deleted in step 2 does not interrupt a run (`7&4` gives `0000000074`), one that became a space does; a decimal digit is any digit of the Basic Multilingual Plane in any script (see R11); (4) the text is lower-cased and transliterated to Latin letters, character by character, as R11 to R15 describe (`Éclair` gives `eclair`, `Ångström` gives `angstrom`, `ß` `ss`, `Æ` `ae`, `Œ` `oe`, `Ø` `o`, `Ł` `l`, `Þ` `th`, `Привет` gives `privet`). Every other character (`!`, `#`, `(`, `_`, `~`, quotes, ...) is kept, except as R13 says for titles that contain letters outside the Latin set. Sort names compare as plain code points, ascending, with no locale collation.
- **items.sort.R9** Seasons and episodes have a structural sort name: a season's is its number padded to four digits (`0001`); an episode's is `<season, 3 digits> - <episode, 4 digits> - <title>` with the title exactly as written (`001 - 0002 - Second Chapter`; no article removal, case kept). Episodes therefore order by season, then episode number, then title, and `NameStartsWith` on episodes is compared with this composite. A series follows R8 (`The Sort Show` gives `sort show`).
- **items.sort.R10** An explicit sort title in the item's metadata (`<sorttitle>` of a Kodi `.nfo`) replaces the title as the input of R8 without renaming the item: title `Zebra Sort` with sort title `An Aardvark Sort` has `Name` `Zebra Sort` and sort name `aardvark sort` (the article rule and the other steps apply to the sort title too).
- **items.sort.R11** Transliteration is a per-character table (observed one code point at a time on the 12.2 reference, for every Basic Multilingual Plane code point outside surrogates and private use plus the mathematical and enclosed alphanumeric blocks). The output is lower case, ASCII except where noted, and uses no tone or accent marks. Classes: **Latin** letters with diacritics, ligatures and special letters fold (`ĳ` `ij`, `ĸ` `q`, `ŉ` `n`, `ſ` `s`, `ƕ` `hv`, `ǆ` `dz`, `ß` `ss`, `ẞ` `ss`, `ŋ` `n`, `ƒ` `f`, `ɇ` `e`, Vietnamese letters to their base); fullwidth Latin letters and digits fold to ASCII; `ﬁ` gives `fi`, `ﬅ` `st`; roman-numeral characters spell out (`Ⅷ` `viii`); `©` gives `c`, `®` `r`, `«` and `《` `<<`, `»` and `》` `>>`, `№` `no`, `℡` `tel`; vulgar fractions give a space and digits (`¼` ` 14`, `½` ` 12`; they are not padded). **Greek**: `α` `a`, `β` `b`, `γ` `g`, `δ` `d`, `ε` `e`, `ζ` `z`, `η` `e`, `θ` `th`, `ι` `i`, `κ` `k`, `λ` `l`, `μ` `m`, `ν` `n`, `ξ` `x`, `ο` `o`, `π` `p`, `ρ` `r`, `σ` and `ς` `s`, `τ` `t`, `υ` `y`, `φ` `ph`, `χ` `ch`, `ψ` `ps`, `ω` `o`, accented and breathing forms fold to the base (`ὁ` `ho`, `ῥ` `rh`, `ᾳ` `ai`). **Cyrillic** uses one Latin letter per letter with the caron dropped: `ж` `z`, `ц` `c`, `ч` `c`, `ш` `s`, `щ` `s`, `ю` `u`, `я` `a`, `ы` `y`, `э` `e`, `х` `h`, `ъ` and `ь` nothing, `ё` `e`, `й` `i`; Ukrainian `ґ` `g`, `є` `e`, `і` and `ї` `i`; Serbian `ђ` `d`, `ћ` `c`, `ј` `j`, `љ` `l`, `њ` `n`, `џ` `d`; Cyrillic letters outside these (for example `Ҝ`, `Ө`) are kept (lower-cased). **Armenian, Georgian, Hebrew** (consonants only; `א` and `ע` give nothing), **Arabic** (letters only, no vowel marks; `ش` `sh`, `غ` `gh`, `ء` `ʾ`, `ع` `ʿ`) and **Thai** (consonants and vowels, `ก` `k`, `ข` `kh`, `ศ` `s`, `อ` `x`, `ั` `a`) are tabulated the same way. **Devanagari, Bengali, Tamil**: a consonant carries its inherent `a` (`क` `ka`, `ম` `ma`, `த` `ta`); signs and the virama give nothing or their vowel (`ि` `i`, `ै` `ai`, `्` nothing). **Kana** reads in Hepburn style (`し` `shi`, `ち` `chi`, `つ` `tsu`, `ふ` `fu`, `ぢ` `dji`, `づ` `dzu`, `を` `wo`, `ん` `n`, `ヴ` `vu`), small kana carry a `~` (`ぁ` `~a`, `っ` `~tsu`, `ゃ` `~ya`), the prolonged sound mark `ー` (or its halfwidth form) next to a kana letter only lengthens the vowel and gives nothing (`コーヒー` gives `kohi`, `ラーメン` `ramen`), elsewhere it stays; the iteration marks `ゝ` `ゞ` give nothing; halfwidth katakana read like their fullwidth forms and fold a following voiced or semi-voiced mark into the letter (`ｶﾞ` gives `ga`, `ﾊﾟ` `pa`). Many punctuation marks, bullets, dashes, curly quotes, `×`, `÷`, `¿`, `¡`, `§`, `¶` and the CJK brackets and full stops give nothing. A character with no entry (`€`, `™`, `°`, `±`, arrows, circled numbers, rare letters, characters outside the Basic Multilingual Plane such as mathematical alphanumerics) is kept, lower-cased when it has a lower case. The case mapping happens before the table, so `İ` (dotted capital I) gives `I` in a Latin-set title and `i` in any other (R13).
- **items.sort.R12** **Han** ideographs (U+3400-4DBF, U+4E00-9FFF and the compatibility block U+F900-FAFF) read as their Mandarin reading without tone marks, lower case (`中文` `zhong wen`, `東京` `dong jing`, `漢` `han`, `語` `yu`); about a thousand rare ideographs have no reading and are kept. A space separates a reading from the previous character when that is a letter of any script (including another ideograph), or when it is separated from it by a dropped separator such as `!` `)` `;` `、` (R13): `abc北京` gives `abc bei jing`, `ひ中` `hi zhong`, `x!中` `x zhong`. There is no space after a digit, a symbol, a fullwidth or ideographic full stop `。`, a middle dot `·` `・`, an em dash or an ellipsis (`1北京` gives `0000000001bei jing`, `x。中` `xzhong`), and none between a reading and a following letter (`北京abc` gives `bei jingabc`, `中ひ` `zhonghi`, `中한` `zhonghan`); a following opening bracket or quotation mark (`(` `[` `「` `『` `【` `（` `“` `‘`) is replaced by a space (`中(x` gives `zhong x`). `<<` is set off by a space on its Han side (`x《中` `x <<zhong`, `中《x` `zhong <<x`) and `>>` by one before a following reading (`》中` `>> zhong`); in a title with no ideograph next to them they take none (`y«` `y<<`). **Hangul** syllables are composed from the reading of their parts and joined without spaces: initial (`ㄱ` `g`, `ㄲ` `kk`, `ㄴ` `n`, `ㄷ` `d`, `ㄸ` `tt`, `ㄹ` `l`, `ㅁ` `m`, `ㅂ` `b`, `ㅃ` `pp`, `ㅅ` `s`, `ㅆ` `ss`, `ㅇ` nothing, `ㅈ` `j`, `ㅉ` `jj`, `ㅊ` `ch`, `ㅋ` `k`, `ㅌ` `t`, `ㅍ` `p`, `ㅎ` `h`), vowel (`ㅏ` `a`, `ㅐ` `ae`, `ㅑ` `ya`, `ㅒ` `yae`, `ㅓ` `eo`, `ㅔ` `e`, `ㅕ` `yeo`, `ㅖ` `ye`, `ㅗ` `o`, `ㅘ` `wa`, `ㅙ` `wae`, `ㅚ` `oe`, `ㅛ` `yo`, `ㅜ` `u`, `ㅝ` `wo`, `ㅞ` `we`, `ㅟ` `wi`, `ㅠ` `yu`, `ㅡ` `eu`, `ㅢ` `ui`, `ㅣ` `i`) and final (none, `g` `kk` `gs` `n` `nj` `nh` `d` `l` `lg` `lm` `lb` `ls` `lt` `lp` `lh` `m` `b` `bs` `s` `ss` `ng` `j` `ch` `k` `t` `p` `h`); `한국어 제목` gives `hangugeo jemog`, `한` `han`, `힣` `hih`. Kana and Hangul readings take no spaces around them.
- **items.sort.R13** A title is *plain* when every character outside ASCII belongs to this set: `¡ ª º ¿`, the Latin-1 letters `À-Ö Ø-ý ÿ` except `Ð ß Þ ð þ ×` and `÷`, and from Latin Extended-A, Latin Extended-B and Latin Extended Additional: `ĀāĂăĄąĆćČčĎďĐđĒēĖėĘęĚěĞğĢģĨĩĪīĮįİıĶķĹĺĻļĽľŁłŃńŅņŇňŌōŐőŒœŔŕŘřŚśŞşŠšŤťŨũŪūŮůŰűŸŹźŻżŽžƏƠơƯưȘșȚțə`, the Vietnamese letters `Ạ-ỹ` (U+1EA0-1EF9), the combining cedilla U+0327, `K` (U+212A) and `Å` (U+212B) and `⸘`. In a plain title ASCII punctuation is kept as R8 says (`Café! (2)` gives `cafe! (0000000002)`). A title with any other non-ASCII character (a letter of another script, `ß`, `Þ`, `Ð`, `ĉ`, `ǎ`, a symbol, a non-breaking or ideographic space) is *non-Latin*: from then on every character of the title is processed the same way, with two additions: (a) the ASCII characters `!` `"` `#` `(` `)` `*` `/` `:` `;` `?` `@` `[` `\` `]` `_` are dropped (`Привет, мир! (1)` gives `privet mir 0000000001`, `Straße!` gives `strasse`; `$` `<` `=` `>` `^` `` ` `` `|` `~` and `+`/`.`/`%` (spaces, step 2) behave as before), and (b) every Unicode space character (non-breaking, ideographic, en/em, ...) becomes an ASCII space. Two non-ASCII spacing details of non-Latin titles: a fullwidth `，` `！` `？` `：` `；` `）` `、` `」` `』` `】` `”` `’` is dropped like its ASCII counterpart and counts as a separator for R12; the dropped marks `。` `·` `・` `—` `–` `…` are dropped and block the space R12 would add.
- **items.sort.R14** Script-specific context rules observed with the table: Greek `γ` before `γ` `κ` `ξ` `χ` reads `n` (`ἄγγελος` `angelos`, `γκ` `nk`, `γχ` `nch`); Greek `υ` after `α` `ε` `η` `ο` `ω` (or their accented forms) reads `u` (`αυ` `au`, `ευρώ` `euro`, `ὈΔΥΣΣΕΎΣ` `odysseus`), elsewhere `y`. In Devanagari a vowel sign or the virama directly after a consonant of the same script removes the consonant's inherent `a` (`से` `se`, `नमस्ते` `namaste`); in Tamil the same holds for the vowel signs and the virama; in Bengali the virama removes it but vowel signs keep it (`লি` `lai`) and the aa sign adds nothing (`বা` `ba`). A virama that does not follow a consonant of its own script, directly after a letter, leaves the private-use character U+E04D in the sort name (`z्` gives `z` followed by U+E04D); after a space, symbol or ASCII digit it gives nothing. A run of Thai text that follows a letter of another script is set off by one space (`京คด` gives `jing khd`; `A 국ั` `gug a`); a Thai consonant directly after an ASCII digit or after a dropped character takes none. The extended Arabic-Indic digits U+06F0-06F9 count as letters for these two rules, the ASCII and other digits do not.

> **Transliteration: observed, and not reproduced.** The table (R11, R12) was probed for every Basic Multilingual Plane code point, one character per item, and the context rules (R12 to R14) were fitted to about 6,600 random mixed-script titles on the reference; the committed implementation reproduces all single characters and agrees on all but roughly 0.3% of those titles (`items.sort.transliteration` pins the representative cases). What stays divergent, in decreasing order of likelihood to matter:
> * Thai vowel marks that are not preceded by a Thai consonant, and the spacing a Thai run gets around dropped marks, follow rules this specification only approximates (`A 국ัส` gives `gug a s` on the reference, `gug as` on Tinyfin).
> * An opening bracket or quotation mark between an ASCII letter and a digit sometimes becomes a space (`Y『5` `y 0000000005`) and sometimes not (`b「9`); Tinyfin spaces it only for `(`, `[` and the fullwidth/ideographic openers listed in R12 when the preceding character is an ASCII letter.
> * Indic scripts other than Devanagari, Bengali and Tamil (Gurmukhi, Gujarati, Odia, Telugu, Kannada, Malayalam, Sinhala) were probed one character at a time only; combinations with signs follow the Bengali/Devanagari rules above where Tinyfin applies them and may differ. The same holds for Greek digraphs other than the ones R14 lists.
> * Han ideographs outside the three blocks of R12 (extensions B and later, in the supplementary planes) and every other supplementary-plane script except the mathematical and enclosed alphanumerics were not probed; Tinyfin keeps those characters lower-cased.

### Rules: paging

- **items.paging.R1** `Limit=n` returns at most n items; `TotalRecordCount` is still the full count of matching items (before paging); `StartIndex` is 0 when not supplied.
- **items.paging.R2** `StartIndex=n` skips n items; the response echoes it in `StartIndex`; a value past the end gives `Items: []` with the full `TotalRecordCount`.
- **items.paging.R3** `Limit=0` returns no items but still reports the full `TotalRecordCount` (on queries that go through paging).
- **items.paging.R4** `EnableTotalRecordCount` defaults to true. With `false`, `TotalRecordCount` equals the number of items in the returned page (not the full count).
- **items.paging.R5** Non-numeric `Limit` or `StartIndex`, and non-boolean `Recursive`, are rejected with **400** (parameter binding).
- **items.paging.R6** There is no default page size: `GET /Items` without `Limit` returns every match (verified with 150 movies in one library: 150 items, `TotalRecordCount` 150), and so does `GET /UserItems/Resume`. `Limit` and `StartIndex` still page. (`GET /Items/Latest` is different: its default is 20 and it honors `Limit` up to the number of matches.)

### Rules: item shape and shaping options

- **items.fields.R1** Item objects carry at least `Id`, `Name`, `ServerId`, `Type`, `IsFolder`; playable items add `MediaType` (`Video` for the fixtures), `RunTimeTicks`; `ProductionYear` comes from the folder name; episodes carry `IndexNumber` and `ParentIndexNumber`. (The full per-item field mapping is outside this area; only the fields exercised here are pinned.)
- **items.fields.R2** Optional fields are returned only when requested through `Fields` (comma-separated); `Path` is absent by default and present with `Fields=Path`; unknown field names are dropped silently.
- **items.fields.R3** `UserData` is included by default for a user context; `EnableUserData=false` omits it. It carries `Played`, `IsFavorite`, `PlayCount`, `PlaybackPositionTicks` for the context user.
- **items.fields.R4** `EnableImages`, `ImageTypeLimit` and `EnableImageTypes` shape image information only; they are accepted in any combination and never change which items match.
- **items.fields.R5** `SortName` is an optional field like `Path`: listing items carry it only when `Fields` contains `SortName`; a single read (`GET /Items/{itemId}`) always carries it.

### Rules: names taken from metadata files and file names

- **items.names.R1** Text read from a sidecar `.nfo` is stored without surrounding white space: the title (`Name`), sort title, original title and plot (`Overview`) of movies, series, episodes and home videos lose every leading and trailing Unicode space character (space, tab, line breaks, no-break space U+00A0, ideographic space U+3000, ...). White space inside the text is kept (an `Overview` of two lines keeps its line break; `Trim   Inner  Run` stays). A character that is not white space is not trimmed: a zero width space (U+200B) around a title stays.
- **items.names.R2** A value that is only white space is no value: a `<title>` of three spaces leaves the name taken from the folder or file, and a blank `<plot>` (or `<plot />`) leaves `Overview` absent.
- **items.names.R3** The sort title is trimmed like the title and then feeds items.sort.R8 (`  The Trim Sort  ` gives `trim sort`); the sort name of an item whose title came from a file or folder name is derived without that name's surrounding white space (`Name` ` The Lead` sorts as `lead`), while `Name` itself is as written (items.names.R4).
- **items.names.R4** A movie's name taken from its folder name, an episode's name taken from its file name and a home video's name taken from its file name (without the extension) are kept as written, white space included (a movie folder ` Trim Lead File` gives the name ` Trim Lead File`).
- **items.names.R5** A series name taken from its folder is trimmed and the white space before a trailing `(Year)` is dropped: the folder ` Trim Lead Show  (2111) ` gives `Name` `Trim Lead Show` and `ProductionYear` 2111.
- **items.names.R6** A season folder name matches `Season N` ignoring case, surrounding white space and repeated white space between the words (`Season 1 ` is season 1 with `Name` `Season 1`).
- **items.names.R7** A season's name from a `<namedseason number="N">` entry of the series `.nfo` is used as written (surrounding white space kept); an entry that is only white space is no name.
- **items.names.R8** An episode's name is its `.nfo` title, trimmed, otherwise the whole file name without the extension as written (`Trim Show S01E02 - File Title  `, not just the part after the numbering). An explicit sort title replaces the structural sort name of items.sort.R9 and is normalized on its own (episode sort title `The Trim Ep Sort` gives `trim ep sort`); a blank sort title is ignored.

- **items.names.R9** A movie that is the only movie of its folder (one file, or several files that are parts or versions of it, items.names.R11) is named after the folder exactly as written, whatever the file is called, and the `(Year)` stays in the name (`Foo One (2015)` is named `Foo One (2015)`, not `Foo One`; a file `Other Name.mp4` in the folder `Foo Two (2016)` gives `Foo Two (2016)`; `Foo Three.2017.1080p.BluRay` stays as written). Its `ProductionYear` is the last four digit number from 1900 to 2099 that follows a space, `.`, `_`, `-`, `(` or `[` and is not directly followed by another digit, anywhere after the first two characters of the name: `Foo One (2015)` 2015, `Two Years 1999 2005` 2005, `Blade Runner 2049 (2017)` 2017, `Hyphen-2011-Name` 2011; `(2013) Paren Start` (year at the start), `Foo Twelve (1899)`, `Far (2100)`, `Big (20144)` and `Name2001` have none. A movie that no `.nfo` titles is therefore listed with the folder's text; a `<title>` still replaces it (items.names.R1).
- **items.names.R10** A video file that is not alone in a folder, or that sits directly in the library folder, is a movie named after the file without its extension, cut at the last year (and at the white space and hyphens before it) and then at the first release token; a trailing `[group]` is removed. Text that nothing ends is kept exactly as written, white space included. The year gives `ProductionYear` as in R9: `Foo Seven (2021).mp4` is `Foo Seven` 2021, `Foo.Eight.2022.mp4` is `Foo.Eight` 2022 (dots are kept), `Loose F (2005) [1080p].mp4` is `Loose F` 2005, `Loose Z - 2014 -.mp4` is `Loose Z` 2014, `Loose Q 2009 1080p 2010.mp4` is `Loose Q 2009` 2010, `2012 (2009).mp4` is `2012` 2009, `Name [Group].mp4` is `Name` while `Brk [Group] Name` is kept; `Loose H 1080p.mp4` is `Loose H` without year and `Loose N2001` is kept whole. A release token ends the title when it stands between delimiters (space `_` `,` `.` `(` `)` `[` `]` `-`), in any case: `3d sbs hsbs htab tab mvc hdr hdc uhd ultrahd 4k ac3 dts custom dc divx divx5 dsr dsrip dutch dvd dvdrip dvdscr dvdscreener screener dvdivx cam fragment fs hdtv hdtvrip hdrip internal limited ntsc ogg ogm pal pdtv proper repack rerip retail cd1`-`cd9 r5 bd5 bd bdrip brrip se svcd swedish german read.nfo nfofix unrated ws telesync ts telecine tc 480p 480i 576p 576i 720p 720i 1080p 1080i 2160p hrhd hrhdtv hddvd bluray x264 h264 x265 h265 xvid xvidvd xxx web-dl blu-ray aac multi`. Words such as `hevc`, `web`, `webrip`, `remux`, `extended`, `imax`, `hd`, `720`, `1080`, `r3`, `french` or `hdcam` are not tokens.
- **items.names.R11** Folders of a movie library: a folder that holds exactly one movie and no sub folder is that movie (no `Folder` item, R9; a folder that also holds a sub folder is a `Folder` item, R12); any other folder is an item of `Type` `Folder` named after the directory as written (empty folders and folders with no video included) that holds what is inside it, at any depth, so `Collection/Inner One (2012)/Inner One (2012).mp4` is a movie whose `ParentId` is the folder `Collection`; a movie directly under the library path is a direct child of the library. Files that differ only by a part number (`Name - part1.mkv`, `Name - part2.mkv`; `cd`, `dvd`, `part`, `pt`, `disc`, `disk` followed by 1-2 digits) are one movie (the lowest part is its file); `Name (Year).mkv` and `Name (Year) - 4K.mkv` in the folder `Name (Year)` are one movie (the folder's name followed by ` - `, `-`, `.` or `_` and more is a version of it, `Name (Year).3D.mkv`, `Name (Year)-extras.mkv`; the name followed by a space and a word is another movie). A folder with two unrelated videos holds two movies named after their files (`part1`, `part2`). Trailer, feature and sample videos (extras.md), hidden folders and the folders `extras`, `extra`, `clips`, `trailers`, `backdrops`, `theme-music`, `sample`, `samples`, `featurettes`, `other`, `subs`, `behind the scenes`, `deleted scenes`, `interviews`, `scenes`, `shorts`, `extrafanart`, `extrathumbs`, `metadata`, `@eaDir`, `#recycle` and `lost+found` (any case) are left out.
- **items.names.R12** A folder of a movie library that holds one movie and also a sub folder (other than the left out ones of R11: `soundtrack/`, `actors/`, an empty folder) is an item of `Type` `Folder` named after the directory that holds the movie and the sub folders (`ParentId` of the movie is the folder). The movie is named after its file without the extension when that is not the folder's name (`Other Name.mp4` in `G1 (2001)` is `Other Name`) and after the folder as written when it is (`Ex Shape Plain (2227)`). Both the folder item and the movie have the folder's pictures (images.shape.R1).
- **items.fields.R6** `MediaType` is `Video` for movies, episodes, trailers and videos, `Photo` for photos, `Audio` for music and `Unknown` for everything that groups items or is a library (series, seasons, folders, photo albums, collections, library views): the member is present with that value.
- **items.fields.R7** `ChildCount` (direct children) and `RecursiveItemCount` (descendants that are not folders themselves: the episodes of a series, the videos below a folder) are answered for folders, seasons and series. A single read (`GET /Items/{itemId}`) always carries them; a listing carries each only when `Fields` names it.

### Rules: what a scan reads (metadata files, no online provider)

- **items.scan.R1** The scan reads Kodi-style `.nfo` files beside the media: `<movie>` documents (`movie.nfo` in a movie's folder, or `<video name>.nfo` beside the file; for videos of a home-videos library or several movies sharing a folder only `<video name>.nfo`), `<tvshow>` (`tvshow.nfo` in the series folder, also giving `<runtime>` and `<namedseason>` names) and `<episodedetails>` (`<episode file name>.nfo`, one or several documents, items.episodes.R10). The fields taken are `<title>`, `<originaltitle>`, `<sorttitle>`, `<plot>`, `<year>`, `<premiered>` (episodes also `<aired>`, `<season>`, `<episode>`), `<genre>`, `<tag>`, `<mpaa>`, `<rating>`/`<ratings>`, `<uniqueid type="tmdb">` and the credits `<studio>`, `<actor>` (`<name>`, `<role>`, `<type>`, `<order>`), `<director>`, `<credits>` and `<writer>` (browse.persons.R4); a value that is only white space is no value (items.names.R1, R2). A file that is not valid XML is ignored.
- **items.scan.R2** With no `.nfo` and no online provider (the reference with its network cut, Tinyfin with no provider key) an item is still cataloged and playable: its names, numbers and years come from folder and file names alone (items.names.R9 to R11, items.episodes.R6 to R9, items.seasons.R1 to R3); it has no `Overview`, genres, ratings, people or artwork other than local files next to it (`poster`, `folder`, `fanart`, `backdrop`, `logo`, `banner`, season posters); a series' `ProductionYear` is the year in its folder name.

### Rules: runtime of series

- **items.runtime.R1** A series carries `RunTimeTicks` only when its `tvshow.nfo` has a `<runtime>` (minutes): the value times 600,000,000. `<runtime>0</runtime>` gives `RunTimeTicks` 0, present (the member is not omitted), `<runtime>20</runtime>` gives 12,000,000,000; without `<runtime>` the member is absent. The value is the same in listings with and without `Fields=RunTimeTicks`, on `GET /Items/{itemId}` and on `GET /Users/{userId}/Items/{itemId}`.
- **items.runtime.R2** The `<runtime>` text is read up to its first space as a base-10 integer with an optional sign that fits 32 bits: `20 min` is 20, `+5` is 5, `020` is 20, `-5` gives -3,000,000,000; `12.5`, `20.0`, `abc`, an empty tag, a value with a leading space and an out-of-range number give no runtime (absent).
- **items.runtime.R3** Only series take a runtime from their `.nfo`. Seasons, folders and library views have no `RunTimeTicks`; movies, episodes and videos carry the duration of their media file (a `<runtime>` in their `.nfo` does not replace it).

### Rules: episodes without a season/episode pattern

- **items.episodes.R1** Every video file inside a season folder, and every video file directly inside a series folder, is an `Episode` of that series whether or not its name carries `SxxExx`/`NxM` numbering. Its `Name` follows items.names.R8 (`Short One` from the `.nfo`, `Zed NoNfo` from the file). Files that claim the same numbers are one episode when their names also agree (R11); otherwise each is listed.
- **items.episodes.R2** `ParentIndexNumber` (season) and `IndexNumber` (episode) come from the file name when it has numbering, and are replaced by `<season>` and `<episode>` of the file's `.nfo` when those tags are present (each independently, so `<season>0</season>` alone gives season 0 and no episode number; `<episode>8</episode>` replaces the 7 of `S01E07`). A number nothing provides is absent: a loose file does not inherit its folder's number (`Loose In Season` in `Season 01` has neither `IndexNumber` nor `ParentIndexNumber`). A numbering of the form `S02E05` in a file inside `Season 01` gives season 2.
- **items.episodes.R3** The season an episode belongs to (its `ParentId`, `SeasonId`, `SeasonName`) is the season folder it sits in, whatever numbers it carries (see items.seasons.R6 for the season whose number it carries). A file directly in the series folder belongs to the season folder of its season number when there is one, otherwise to a season with no folder named `Season N` (`IndexNumber` N, no `Path`), created for it; a file with no season number belongs to the first season folder that has no number (items.seasons.R2), otherwise to a season named `Season Unknown` (no `IndexNumber`, no `Path`, sort name `Season Unknown`). A series whose files are all loose therefore has exactly one season, `Season Unknown`.
- **items.episodes.R4** Season 0 is named `Specials` (a `Season 00` folder, a folder named `Specials` in any case, or a created season 0); other seasons are `Season N`. A `Specials` folder is season 0.
- **items.episodes.R5** The structural sort name of an episode (items.sort.R9) uses only the numbers it has: season and episode `SSS - EEEE - title` (`000 - 0001 - S00E01 - Promo`), season only `SSS - title` (`000 - Short One`), episode only `EEEE - title` (`0004 - Root Nfo NoSeason Title`), neither the title as written, with its case and article (`Zed NoNfo`). `GET /Shows/{seriesId}/Episodes` lists every such episode.
- **items.episodes.R6** Numbering is read from the file name inside a season folder, a folder of a season or the series folder; the forms and the order they are tried in are in items.episodes.R7. A name with only an episode number (`E04`, `Ep 03`, `Episode 8`, `01 - Title`, `12`) gives that episode number and, inside a season folder, that folder's season.
- **items.episodes.R7** Numbering forms, tried in this order, the first that matches decides (case-insensitive, preceded by the start of the name or by a space, `.`, `_`, `-`, `[` or `(`): (a) `SxxExx` with up to four digits each and any of ` `, `.`, `_`, `-` between `S` and `E` (`S01E02`, `s01.e02`, `S1 - E2`, `S2005E10`, `[S02E15]`); episodes directly chained (`S01E02E03E04` ends at 4) or joined by `-` (`S01E05-06`, `S01E05-E06`) set `IndexNumberEnd`, a `.E03` or ` E06` after the first does not; (b) `NxM` (`1x02`, `03x09`, `2x1080p` is season 2 episode 1080), with `x3` chains and `-06` or `-2x06` ends; (c) `Season N Episode M` with separators between the words (`Season1 Episode2` is not this form and gives episode 2 only); (d) a date (R9); (e) `Ep`/`Episode` immediately followed by digits (`Ep05`, `EP5`, `Episode05`; `Ep.5` and `Ep. 5` are not numbers); (f) `E` followed by digits and then `.`, `_`, `-` or the end (`E05`, `e05.Title`; `E5 Title` is not); (g) `Part`/`Pt` followed by one of `.`, `_`, space, `-` and digits; (h) `[01]`; (i) ` - 05`, up to three digits, when no digit precedes it in the name (`Show - 05`, `Show - 105 - Title`; `Show - 1080p` gives 108; `Show 05 - 06` is 5); (j) the first run of digits, at most four of them, that starts the name or follows a space that follows a letter, a digit or `-` (`Show 05` 5, `Show 209` 209, `Show 2200` 2200, `Show 12345` 1234, `Show 1080p` 1080, `Show 720p 05` 720, `01 - Title` 1; `Show (2019) 05`, `Show Ep. 5`, `Show_05`, `Show.05`, `Show -05`, `Show #05`, `[Group] 05` and `Show S01` give none). A bare three or four digit number is the episode number, not season and episode. Names with no number at all (`Special`) give neither.
- **items.episodes.R8** The name of an episode file with no `.nfo` title is the file name without the extension, cut like a loose movie file (items.names.R10: the text before the last year, then before the first release token, without trailing `[group]`) and, when it ends with ` - 05` (hyphen, digits), without that tail: `Yr Show (2019) S01E01` is `Yr Show` (ProductionYear 2019), `Show - S01E03 - Title 1080p` is `Show - S01E03 - Title`, `Show [S02E15]` is `Show`, `Show - 05` is `Show`, `Show 05 [Group]` is `Show 05`, `Show.S01E04.720p.WEB-DL.x264` is `Show.S01E04`; a name nothing cuts stays whole (`Show 209`, `Show S01E02 Title Here`, `S00E01 - Promo`, `Trim Show S01E02 - File Title  `). The year read this way is the episode's `ProductionYear`; the number `2020` of `Show 2020` is also its `IndexNumber`.
- **items.episodes.R9** A name with a date in it is an episode with no season or episode number from the name: `2020-01-31`, `2020.01.31` or `2020 01 31` (year first) and `31-01-2020` (day first, hyphen, dot or space). `PremiereDate` is the date and `ProductionYear` its year; the year first form keeps the name whole (`Show 2020-01-31 Title`), the day first form ends the name before `-2020` (`Show 31-01-2020` is `Show 31-01`). A group such as `01-31-2020` whose month is out of range is still a date group: `ProductionYear` 2020, no air date, no number. Inside a season folder such an episode has that folder's season; its `ParentIndexNumber` is absent otherwise.
- **items.episodes.R10** A `.nfo` with several `<episodedetails>` documents describes one episode that covers several: the documents are ordered by `<episode>` (those without one last), the first supplies every field, `Name` and `Overview` are the non-blank titles and plots of all of them joined with ` / `, `IndexNumber` is the first document's episode and `IndexNumberEnd` the last document's (absent when it has none or there is one document): documents `Alpha`(1) and `Beta`(3) give `Alpha / Beta`, 1, 3; `Rev One`(5) and `Rev Two`(4) give `Rev Two / Rev One`, 4, 5.
- **items.episodes.R11** Files of one series that carry the same season and episode numbers (the season of the folder when the name gives none) and the same name once release tokens and a trailing ` -` are cut (`Show S01E01.mkv`, `Show S01E01 1080p.mkv`, `Show S01E01 - 4K.mkv`) are one `Episode` with one media source per file: the listing and the counts carry the episode once (`ChildCount` of the season 2 for such a season with a second episode), a single read of it has `MediaSources` for every file (`Name` of the first is its file name, of the others what follows the shared name: `1080p`, `4K`), `PlaybackInfo` lists them and the stream route serves the file of a `MediaSourceId`. Files with numbers but different names (`Show E5E6`, `Show E5-E6`) are separate episodes. Files without both numbers are never merged.
- **items.episodes.R12** Numbering that the other forms do not give: a chain of `E` numbers (`E5E6`, `E05E06E07`, `E5-E6`, `E5_E6`) is the last number of the chain (`E5.E6` is 5); a name that ends with a word that carries the number glued to it (`Show Title3`, `Title12`, `Show 2020 Title3` is 3, not 2020) is that number, unless the word is `part`, `pt`, `cd`, `dvd`, `disc`, `disk`, `ep`, `episode`, `season`, `vol`, `volume` or `chapter`, unless the name holds two such words (`Show Title7 Extra5`), and, for a one-word name, unless it has a single digit (`Show3`). The season is that of the folder.

### Rules: season folders

- **items.seasons.R1** A folder directly inside a series folder is a season number N when its whole name, ignoring case and surrounding white space, is a season word (`Season`, `Series`, `Staffel`, `Temporada`, `Saison`, `Stagione`, `Seizoen`) and N with any or no separator between them (`Season 1`, `Season06`, `Season_7`, `Season.8`, `Season-24`, `Season  23`, `Series 11`, `Temporada 9`), `S` and N (`S4`, `S05`), or N alone (`12`, `013`), optionally followed by a non-alphanumeric and more text (`Season 15 - Title`, `Season 16 Redux`, `Season 21 (2010)`, `S17 Complete`); `Specials` (any case) is season 0. The season is named `Season N` (`Specials` for 0) and sorts by number, whatever the folder says. `Seasons 25`, `Season Finale`, `Season X`, `Series`, `Special`, `Disc 1`, `Vol 4`, `Part 3`, `S26E01` carry no number.
- **items.seasons.R2** Any other folder directly inside a series folder (that R4 does not leave out) that holds a video file, directly or below it, is a season named after the folder as written, with no `IndexNumber` (`Part 2`, `Arc 1`, `Season X`); its sort name is the folder name, and the episodes inside it are its children. One that holds no video (an empty folder, a folder of pictures or of songs) is an item of `Type` `Folder` of the series. A file of the series folder that has no season number belongs to the first such season (by name), otherwise to `Season Unknown` (items.episodes.R3).
- **items.seasons.R3** Several folders may carry one number (`Season 01` and `Season 1 Bonus`; `Season 0`, `Season 00`, `Specials`): one season stands for them (the last of them whose whole name is the season, else the first), and the episodes of all of them belong to it.
- **items.seasons.R4** Not scanned, in any case: hidden folders and the folders `extras`, `extra`, `clips`, `trailers`, `backdrops`, `theme-music`, `sample`, `samples`, `featurettes`, `other`, `subs`, `behind the scenes`, `deleted scenes`, `interviews`, `scenes`, `shorts`, `metadata`, `@eaDir`, `#recycle` and `lost+found`, at any depth of a series. Singular or longer names (`Featurette`, `Interview`, `short`, `trailer`, `Deleted`, `Extras 2`, `subtitles`, `bonus`, `artwork`) are ordinary folders (seasons when directly in the series).
- **items.seasons.R5** A folder inside a season folder (`Season 01/Disc A`) is an item of `Type` `Folder` whose `ParentId` is the season (or the folder above it), holding its episodes; the episodes' `SeasonId` is the season.
- **items.seasons.R6** An episode that says it belongs to season N (by its name or `.nfo`) from a folder that is not season N (`S05E01` in `Season 03`, `S02E01` in `Part 2`) keeps its folder as parent; the series also has a season N for it when no folder season has that number (named `Season N`, `IndexNumber` N, no `Path`, `ChildCount` 0). Listing a season (`ParentId`) returns the episodes it holds and the episodes of the series whose `ParentIndexNumber` is its number (`Season 5` lists `S05E01` from `Season 03`; `Season 1` lists `Bonus S01E05` from `Season 1 Bonus`), and `GET /Shows/{seriesId}/Episodes?seasonId=` likewise.
- **items.seasons.R8** The series' `ChildCount` counts every season folder, also the ones that share a number with another (`Season 01`, `Season 1 Bonus`, `Specials`, `Season 00` is 4), while its listing and `GET /Shows/{seriesId}/Seasons` have one season per number (2). `RecursiveItemCount` is the number of episodes.
- **items.seasons.R9** A folder directly inside a series folder whose name carries season and episode numbers (`S26E01`, `S26E02 Title`, `1x05`, `Show S30E01`, `S29E03E04`) is not a season: it is an item of `Type` `Folder` of the series, and the files in it are episodes of the season their numbers name (the season is created when no folder is that season: `Season 26`), taking the folder's numbers for a file name that has none. A folder with only an episode number (`E05`, `Ep 12`, `Episode 5`) or a season word (`Season 26 Episode 1`) stays a season.
- **items.seasons.R7** Season folders carry `RecursiveItemCount` (the episodes below them), `MediaType` `Unknown` and a `ChildCount` of what they directly hold (episodes and folders).

### Rules: home-videos libraries

- **items.homevideos.R1** A library of collection type `homevideos` is a view (`Type` `CollectionFolder`, `CollectionType` `homevideos`, `IsFolder` true) like the other libraries.
- **items.homevideos.R2** Its content is cataloged without any naming scheme: every folder below the library path (at any depth, empty ones included) is an item of `Type` `Folder` (`IsFolder` true, `MediaType` not `Video`, `Path` the directory, no media sources) named after the directory as written, and every video file is an item of `Type` `Video` (`IsFolder` false, `MediaType` `Video`) named after the file without its extension as written (`Channel One - Bumper - 2002 - Game`, `Dotted.Name.2002.1080p`, `  lead file `). Nothing is parsed out of the name: no `ProductionYear`, no series or season. Sort names follow items.sort.R8 on the name (`dotted name 0000002002 0000001080p`; `2 Two` sorts before `10 Ten`). Video extensions are matched case-insensitively and include `mkv`, `mp4`, `avi`, `webm`, `m4v`, `ts`, `mov`, `wmv`, `flv`, `mpg`, `mpeg`, `ogv`, `3gp`, `m2ts`, `vob` and `divx`; files of other kinds (`.txt`, `.mp3`, `.nfo`, subtitles) are not items.
- **items.homevideos.R3** A `<name>.nfo` beside a video (`<movie>` document) supplies the video's `Name`, sort title, original title, plot, year and premiere date under items.names.R1; a `movie.nfo` in the folder is not read for the videos next to it.
- **items.homevideos.R4** Left out of the catalog: entries whose name starts with a dot; the folders `@eaDir`, `#recycle` and `metadata`; video files that are trailers or samples by name (`Movie-trailer.mp4`, `Movie.trailer.mp4`, `Movie.sample.mkv`, `sample.mp4`); the content of folders named `extras`, `sample` and `backdrops` (the folder itself is listed).
- **items.homevideos.R5** Hierarchy: `ParentId` = the library lists the first level (folders and videos directly under the library path), not the library's own root folder item; `Recursive=true` also returns every descendant and that root folder item (`Type` `Folder`, named after the library directory). `ParentId` = a folder lists its direct children, and with `Recursive=true` everything below it; a folder item carries `ChildCount` (its direct children, 0 when empty).
- **items.homevideos.R6** Type filters work as for other libraries: `IncludeItemTypes=Video` (recursive when `Recursive` is absent, items.list.R6), `IncludeItemTypes=Folder`, `MediaTypes=Video` (videos only, folders have no media type), `Filters=IsNotFolder` and `Filters=IsFolder`; `IsFolder` as a parameter is ignored (items.filter.R20).
- **items.homevideos.R7** A `Video` item has the shape of a movie: `RunTimeTicks`, `MediaSources` (one source whose `Id` is the item id), `MediaStreams`, `VideoType` `VideoFile`, `Width` and `Height` from the probe; `POST /Items/{itemId}/PlaybackInfo` answers like it does for a movie; for a folder (or any item without a file) it answers **400**, as does the stream route.
- **items.homevideos.R8** A picture file (the extensions of items.homevideos.R10, any case) is an item of `Type` `Photo` (`MediaType` `Photo`, `IsFolder` false), named after the file without the extension as written, with `Width` and `Height` of the picture, `Album` the name of its folder and `AlbumId` that folder's id; the picture is its own `Primary` image. Not photos: hidden files, folder artwork (`folder`, `poster`, `cover`, `default`, `thumb`, `thumbnail`, `landscape`, `logo`, `fanart`, `backdrop`, each optionally followed by digits), and the images of a video (a file named like the video, or like it followed by `-` and more, such as `Clip.jpg`, `Clip-poster.jpg`). Other names (`banner`, `art`, `season01-poster`, `primary`) are photos.
- **items.homevideos.R10** The picture extensions are `jpg`, `jpeg`, `png`, `gif`, `bmp`, `webp`, `tif`, `tiff`, `avif`, `ico`, `wbmp` and the raw formats `cr2`, `crw`, `dng`, `arw`, `erf`, `nef`, `nrw`, `orf`, `pef`, `raf`, `rw2`. `heic`, `heif`, `jpe`, `jfif`, `svg`, `psd`, `jxl`, `tga` and the other raw formats (`raw`, `3fr`, `cr3`, `srw`, ...) are not pictures: such a file is not cataloged.
- **items.homevideos.R9** A folder that directly holds at least one photo is an item of `Type` `PhotoAlbum` (`IsFolder` true, `MediaType` `Unknown`) instead of `Folder`; a folder with only videos or nothing, and the library root, stay `Folder`. Albums, folders and photos are filtered by `IncludeItemTypes` `PhotoAlbum`, `Folder`, `Photo` and by `MediaTypes=Photo`.

---

## <a id="latest"></a>GET /Items/Latest and GET /Users/{userId}/Items/Latest

- **items.latest.R1** `GET /Users/{userId}/Items/Latest` is the per-user spelling of `GET /Items/Latest` and answers the same array for the user in the path. Authorization follows `GET /Users/{userId}/Views` (items.views.R1): own and administrator **200**, another user's **403**, an identifier that names nobody **404**, a value that is not an identifier **400**, anonymous **401**.
- **items.latest.R2** `Configuration.HidePlayedInLatest` (default `true`) keeps what the account already played out of the row, on both routes and with or without `ParentId`. An `isPlayed` the request asks for wins over the preference (`isPlayed=true` lists only the played media). The `filters` parameter has no effect on this route: `filters=IsPlayed` and `filters=IsUnplayed` are ignored, and the preference still applies. Measured against the reference.
- **items.latest.R3** `Configuration.LatestItemsExcludes` (the "Include in latest media" checkboxes) takes those libraries' media out of the row a request without `ParentId` asks for. A request scoped with `ParentId` still lists the library it names — asking for it by name is not an accident. Measured against the reference, which additionally truncates the row when the excluded library is not the last one it reads (see `DIV-35` in [`../13-divergences.md`](../13-divergences.md)); this server filters only the excluded library.

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
- **items.userdata.R14** `GET` and `POST /Users/{userId}/Items/{itemId}/UserData` are aliases of the `/UserItems/{itemId}/UserData` routes with the user taken from the path (own id or administrator, else `403`); everything else is as R1-R13. The web client posts to the `POST` form to change the favorite mark. (`items.userdata.legacy-path`)

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
- **items.get.R10** An item id in the path that is not a GUID (`/Items/not-a-guid`, `/Items/12345`) answers **400** with a problem document (`wire.problem.R2`, `errors` names `itemId`), not 404; the same holds on every route that takes an item id in its path (`GET`/`POST /UserItems/{itemId}/UserData`, the legacy alias). Authentication is checked first: an anonymous caller gets 401.

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
- **items.legacy.R6** The all-zero `userId` in the path (plain or dashed), on either alias, is not an error: it acts as the caller (the token user), as an omitted `UserId` does on the modern route (`items.user.R3`); the item's `UserData` is the caller's own.
- **items.legacy.R7** A path user id or item id that is not a GUID answers **400** with a problem document (`wire.problem.R2`) naming `userId` or `itemId`.

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
(no such metadata, plugins, Live TV or music exist):

`HasThemeSong`, `HasThemeVideo`, `HasSpecialFeature`, `HasTrailer`, `HasTvdbId`, `IsUnaired`,
`MinDateLastSaved`, `MinDateLastSavedForUser`, `MaxPremiereDate`, `IsMovie`, `IsSeries`, `IsNews`, `IsKids`, `IsSports`,
`PersonIds`, `PersonTypes`, `Studios`, `StudioIds`, `GenreIds`, `Artists`, `ArtistIds`, `ExcludeArtistIds`,
`AlbumArtistIds`, `ContributingArtistIds`, `Albums`, `AlbumIds`, `IsPlaceHolder`, `SeriesStatus`,
`AudioLanguages`, `SubtitleLanguages`, `Filters=Likes|Dislikes|IsFavoriteOrLikes` (only the conflict check is covered),
`SortBy=DatePlayed|PlayCount|...` user-ordering keys, Playlist behaviors (collections are in [`collections.md`](collections.md)), channel and Live TV parents,
special library views (`UserView` types such as latest, next-up, genres), remote-access and parental-schedule denial,
API-key (no user) access. Naming a studio or an artist in a query also creates the by-name entity as a side effect in the
reference; that is deliberately left unexercised.

## Known differences

Observed on the reference and deliberately not reproduced (they are not asserted by a scenario that runs against both):

* **`RecursiveItemCount` of an episode-named folder.** The reference counts the episodes found in a folder named like an episode (`S26E01`, items.seasons.R9) in that folder's `RecursiveItemCount` (1) although the episode is a child of its season; Tinyfin counts the folder's children (0).
* **Which file stands for an episode, and the order of its sources.** For several files of one episode (items.episodes.R11) the reference picks an episode file and orders the other sources by something the files do not show (the same copies came out in a different order than the one they have by name); Tinyfin takes the file with the shortest name (then the first by path) and lists the others by path. Versions are direct-play only: a transcode starts from the episode's own file.
* **Exotic numbering.** `Show E5-6` is season 5 episode 6 in the reference (the dash form is read as `NxM`) and `Show E5-E6` is cut to the name `Show`; Tinyfin reads episode 5 and keeps the name. Other combinations of letters and digits that are not listed in items.episodes.R7 and R12 are not probed one by one.
* **Release token list.** The tokens of items.names.R10 are the ones probed one by one; a token outside that list that the reference also cuts at would keep its text in Tinyfin.
* **Sort name transliteration.** The residue listed under items.sort.R14 stays: characters outside the observed table (Thai runs after other scripts, rare Indic signs, supplementary planes) have no closed rule; the table was observed one code point at a time and is not extended by guessing.
* **Network `EnableRemoteAccess=false`.** Implemented (wire.network.R1) and covered by a unit test only: with the setting off the reference answers every remote request 503 (administrator included), so a scenario could not restore the setting from the runner; the runner would lock itself out.
* **Collections (`BoxSet`).** Specified in [`collections.md`](collections.md); the differences that remain are listed there.

## Resolved questions

Every question that was open in earlier revisions has been settled by probing the 12.2 reference:

* **Library restriction on `Ids` (items.scope.R3).** The reference does not apply the restriction (security defect); recorded as a divergence in the scenario.
* **Dimension filters (items.filter.R17).** The fixture media is 320x240 (read back from the reference's stream info); the bucket semantics, including the union of `IsHd` and `Is4K`, are observed in `items.filter.dimension-buckets`.
* **`PremiereDate` ordering fallback (items.sort.R3).** January 1st of the production year; see `items.sort.premiere-date-fallback`.
* **`UserId` all-zero on the legacy single-item alias.** Falls back to the caller (items.legacy.R6).
* **Non-GUID item id in a route.** 400 with a problem document, never 404 (items.get.R10, items.legacy.R7).
* **Sort name of items with leading articles and non-ASCII names.** items.sort.R8 to R14 and R9. Transliteration of non-Latin scripts is implemented from the observed table; the remaining differences are listed under items.sort.R14.
* **`SortName` in listings.** Only with `Fields=SortName` (items.fields.R5).

---

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `items.list.R1` | `items.list.anonymous` |
| `items.list.R2` | `items.list.shape-recursive-movies` |
| `items.list.R3` | `items.list.unknown-param-ignored`, `items.list.param-names-case-insensitive` |
| `items.list.R4` | `items.list.root-views`, `items.list.root-views-ignore-paging` |
| `items.views.R1`, `R2` | `items.views.alias` |
| `items.views.R6`, `R7` | `users.preferences.save` |
| `items.grouping.R1`, `R2`, `R4` | `items.grouping.options` |
| `items.latest.R1`–`R3` | `users.preferences.save` |
| `items.grouping.R3` | none — asserted by `TestGroupingOptionsFilterAndOrder` |
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
| `items.filter.R17` | `items.filter.dimensions` |, `items.filter.dimension-buckets`
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
| `items.sort.R3` | `items.sort.production-year` |, `items.sort.premiere-date-fallback`
| `items.sort.R4` | `items.sort.multi-key` |
| `items.sort.R5` | `items.sort.order-without-key` |
| `items.sort.R6` | `items.sort.tie-break` |
| `items.sort.R7` | `items.sort.random` |
| `items.sort.R8` | `items.sort.sort-name-rules`, `items.sort.transliteration`, `items.sort.series-and-episode-sort-names` |
| `items.sort.R9` | `items.sort.series-and-episode-sort-names` |
| `items.sort.R10` | `items.sort.sort-name-rules` |
| `items.sort.R11` to `items.sort.R14` | `items.sort.transliteration` |
| `items.paging.R1` | `items.paging.limit` |
| `items.paging.R2` | `items.paging.start-index` |
| `items.paging.R3` | `items.paging.limit-zero` |
| `items.paging.R4` | `items.paging.total-record-count-flag` |
| `items.paging.R5` | `items.paging.malformed` |
| `items.paging.R6` | `items.paging.no-default-cap` |
| `items.rating.R1` | `items.rating.policy-max-value` |
| `items.rating.R2` | `items.rating.policy-max-value` |
| `items.rating.R3` | `items.rating.policy-max-sub-rating` |
| `items.rating.R4` | `items.rating.block-unrated` |
| `items.rating.R5` | `items.rating.query-bounds` |
| `items.rating.R6` | `items.rating.series-inheritance` |
| `items.rating.R7` | `items.rating.single-item-read` |
| `items.rating.R8` | `items.rating.other-read-paths` |
| `items.fields.R1` | `items.fields.movie-basics`, `items.fields.series-and-episode` |
| `items.fields.R2` | `items.fields.path-on-request` |
| `items.fields.R3` | `items.fields.enable-user-data`, `items.userdata.reflected-in-listing` |
| `items.fields.R4` | `items.fields.image-options-accepted` |
| `items.fields.R5` | `items.fields.sortname-optional` |
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
| `items.userdata.R14` | `items.userdata.legacy-path` |
| `items.get.R1` | `items.get.anonymous` |
| `items.get.R2` | `items.get.movie`, `items.get.series-and-episode` |
| `items.get.R3` | `items.get.movie`, `items.get.series-and-episode` |
| `items.get.R4` | `items.get.not-found` |
| `items.get.R5` | `items.get.root` |
| `items.get.R6` | `items.get.hidden-from-user` |
| `items.get.R7` | `items.get.user-id` |
| `items.get.R8` | `items.get.user-data-per-user` |
| `items.get.R9` | `items.get.full-dto-ignores-list-options` |
| `items.get.R10` | `items.get.non-guid-item-id` |
| `items.legacy.R1` | `items.legacy.items-alias` |
| `items.legacy.R2` | `items.legacy.items-user-rules` |
| `items.legacy.R3` | `items.legacy.items-no-indexnumber` |
| `items.legacy.R4` | `items.legacy.item-alias` |
| `items.legacy.R5` | `items.legacy.item-user-rules` |
| `items.legacy.R6` | `items.legacy.item-alias-path-ids` |
| `items.legacy.R7` | `items.legacy.item-alias-path-ids` |
| `items.names.R1` | `items.names.nfo-trims-surrounding-whitespace`, `items.names.series-nfo-and-named-season`, `items.names.episodes` |
| `items.names.R2` | `items.names.nfo-trims-surrounding-whitespace`, `items.names.episodes` |
| `items.names.R3` | `items.names.nfo-trims-surrounding-whitespace` |
| `items.names.R4` | `items.names.file-derived-name-kept` |
| `items.names.R5` | `items.names.series-folder-and-season-folder` |
| `items.names.R6` | `items.names.series-folder-and-season-folder` |
| `items.names.R7` | `items.names.series-nfo-and-named-season` |
| `items.names.R8` | `items.names.episodes` |
| `items.runtime.R1` | `items.runtime.series-from-nfo` |
| `items.runtime.R2` | `items.runtime.series-from-nfo` |
| `items.runtime.R3` | `items.runtime.other-types` |
| `items.episodes.R1` | `items.episodes.loose-files-names-and-numbers` |
| `items.episodes.R2` | `items.episodes.loose-files-names-and-numbers` |
| `items.episodes.R3` | `items.episodes.loose-files-seasons`, `items.episodes.specials-folder-and-series-without-seasons` |
| `items.episodes.R4` | `items.episodes.loose-files-seasons`, `items.episodes.specials-folder-and-series-without-seasons` |
| `items.episodes.R5` | `items.episodes.loose-files-names-and-numbers` |
| `items.episodes.R6` | `items.episodes.loose-files-names-and-numbers` |
| `items.homevideos.R1` | `items.homevideos.library-view` |
| `items.homevideos.R2` | `items.homevideos.library-first-level`, `items.homevideos.video-names-and-nfo` |
| `items.homevideos.R3` | `items.homevideos.video-names-and-nfo` |
| `items.homevideos.R4` | `items.homevideos.library-first-level`, `items.homevideos.recursive-listing-and-filters` |
| `items.homevideos.R5` | `items.homevideos.library-first-level`, `items.homevideos.recursive-listing-and-filters`, `items.homevideos.folder-children` |
| `items.homevideos.R6` | `items.homevideos.recursive-listing-and-filters` |
| `items.homevideos.R7` | `items.homevideos.video-names-and-nfo`, `items.homevideos.video-media-and-playback` |
| `items.filter.R20` | `items.filter.is-folder-param-ignored`, `items.homevideos.recursive-listing-and-filters` |
| `items.scan.R1` | `items.names.nfo-trims-surrounding-whitespace`, `items.episode-nfo-multi-document` |
| `items.scan.R2` | `items.names-movie-in-folder`, `items.episode-numbering-1` |
| `items.names.R9` | `items.names-movie-in-folder` |
| `items.names.R10` | `items.names-loose-movie-file` |
| `items.names.R11` | `items.names-movie-in-folder`, `items.names-folders-and-stacks` |
| `items.fields.R6` | `items.names-folders-and-stacks`, `items.homevideos-photos` |
| `items.fields.R7` | `items.names-folders-and-stacks`, `items.season-folders` |
| `items.episodes.R7` | `items.episode-numbering-1`, `items.episode-numbering-2`, `items.episode-numbering-3`, `items.episode-numbering-season-folder` |
| `items.episodes.R8` | `items.episode-numbering-1`, `items.episode-numbering-2`, `items.episode-numbering-3` |
| `items.episodes.R9` | `items.episode-numbering-1`, `items.episode-numbering-2`, `items.episode-numbering-3` |
| `items.episodes.R10` | `items.episode-nfo-multi-document` |
| `items.seasons.R1` to `R7` | `items.season-folders` |
| `items.homevideos.R8`, `R9` | `items.homevideos-photos` |
| `items.sort.R8` (articles) | `items.sort-articles-anywhere` |
| `items.names.R12` | `items.names.movie-folder-with-subfolder` |
| `items.seasons.R8` | `items.seasons.merged-folders` |
| `items.seasons.R9` | `items.seasons.episode-folder` |
| `items.episodes.R11` | `items.episodes.versions` |
| `items.episodes.R12` | `items.episodes.numbering-forms` |
| `items.homevideos.R10` | `items.homevideos.photo-types` |
| `items.seasons.R2` | `items.seasons.folder-without-videos` |
