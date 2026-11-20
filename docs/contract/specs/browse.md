# Browse: genres, studios, people, years, facets and discovery

Contract for the by-name browse routes (`/Genres`, `/Studios`, `/Persons`, `/Years`), the filter facets
(`/Items/Filters`, `/Items/Filters2`), the filters of `GET /Items` that name them, and the discovery routes
(`/Items/{itemId}/Similar`, `/Movies/Recommendations`, `/Items/Suggestions`, `/Items/{itemId}/Intros`,
`/Items/{itemId}/Collections`).

Scenarios: [`../scenarios/browse.json`](../scenarios/browse.json). Fixture state: [`../fixtures.json`](../fixtures.json).
Rule IDs are stable (`browse.<topic>.R<n>`); scenarios reference them through `covers`.

## Fixture facts this spec relies on

The same as search.md: the always-present libraries `Movies` and `Shows`, and the lazy `Search` and `SearchShows`
libraries with their genres, studios, people, years and tags. Counts below are for `Search` and `SearchShows` loaded
together with the always-present libraries (which carry no metadata of this kind):

| Name | Carried by |
|---|---|
| genre *Action* | movies *Zeta Quest*, *Xray Vision*, *Writers Cut* |
| genre *Drama* | movie *Yankee Dawn*, series *Search Show* (and, through the series, its episode) |
| genre *Comedy* | movie *Zeta Quest*, series *Search Show* (and its episode) |
| genre *Animation*, *Family* | *Whiskey Alpha* |
| genre *Horror* | *Alpha Wolf* |
| studio *Acme Pictures* | *Zeta Quest*, *Yankee Dawn*, *Writers Cut* |
| studio *Bolt Films* | *Yankee Dawn*, *Xray Vision*, series *Search Show* (and its episode) |
| studio *Cartoon Co* | *Whiskey Alpha* |
| person *Ann Actor* | *Zeta Quest*, *Yankee Dawn*, *Whiskey Alpha*, *Writers Cut* (actor), series *Search Show* (actor) |
| person *Dana Director* | director of *Zeta Quest*, *Yankee Dawn*, *Writers Cut* and of the episode; actor in *Writers Cut* |

## Conventions

* *Named items* (genre, studio, person, year) are not stored items: one exists while an item the user can see carries
  the name. Its `Id` is stable for the name (names compare without regard to case) and `GET /Items/{id}` opens it.
  Their `Type` is `Genre`, `Studio`, `Person` or `Year`; they are not folders, `MediaType` is `Unknown`,
  `LocationType` `FileSystem`.
* *Carriers*: the items the user may see (library access, tag rules, parental limits) and the playlists the user may
  read, narrowed by `ParentId` and the item type lists. A name nothing visible carries is not listed.
* A genre or studio of a series is also carried by that series' episodes for the counts (below).

---

## <a id="genres"></a>GET /Genres and GET /Genres/{genreName}

**Purpose.** List the genres, and read one.

**Auth.** Any authenticated user.

**Request.** `/Genres`: `StartIndex`, `Limit`, `SearchTerm`, `ParentId`, `Fields`, `ExcludeItemTypes`,
`IncludeItemTypes`, `IsFavorite`, `ImageTypeLimit`, `EnableImageTypes`, `UserId`, `NameStartsWithOrGreater`,
`NameStartsWith`, `NameLessThan`, `SortBy`, `SortOrder`, `EnableImages`, `EnableTotalRecordCount`.

### Rules

- **browse.genres.R1** `GET /Genres` answers a query result (`Items`, `TotalRecordCount`, `StartIndex`) of the distinct genres carried by what the user may see, ordered by name (case-insensitive) ascending. A genre carried in two spellings is one entry (the first spelling met). An item has `Id`, `Name`, `Type` `Genre`, `MediaType`, `LocationType`, empty image members, and no `UserData`, `ChildCount` or counts unless asked for. Anonymous is `401`.
- **browse.genres.R2** `Fields=ItemCounts` (or `ChildCount`) adds `ChildCount` and `MovieCount`, `SeriesCount`, `EpisodeCount` (and `TrailerCount`, `ProgramCount`, `SongCount`, `AlbumCount`, `ArtistCount`, `MusicVideoCount`, always 0) of the items carrying the genre: the genre of a series counts its episodes too, `ChildCount` is the sum. The counts are present without `Fields` when `IncludeItemTypes` is given. They are not narrowed by `IncludeItemTypes` but are by `ExcludeItemTypes`. `Fields=SortName,Path` adds the sort name (the lower-cased name) and a path.
- **browse.genres.R3** `StartIndex` and `Limit` page the list (`TotalRecordCount` is the number of genres before paging); `SortBy=SortName` with `SortOrder` Ascending/Descending orders by name; `NameStartsWith`, `NameStartsWithOrGreater` and `NameLessThan` compare names without regard to case; `SearchTerm` keeps the names that contain it (search key compare). `EnableTotalRecordCount=false` gives a `TotalRecordCount` of 0.
- **browse.genres.R4** `IncludeItemTypes` lists only genres of items of those types (`Series`: the genres of series; `Episode`: none, an episode's genre belongs to its series), `ExcludeItemTypes` leaves items of those types out; `ParentId` (a library or a folder, recursive) narrows to its items; a `ParentId` that is not a GUID is `400`; `IsFavorite=true` lists nothing (named items are not favorited).
- **browse.genres.R5** `GET /Genres/{genreName}` answers the genre for any name, also one nothing carries (`ChildCount` 0); the name is matched without regard to case and the answer carries the spelling the catalog has. The item has `Name`, `Id`, `Type`, `ChildCount` and the type counts, `UserData` (`Key` `Genre-<name>`, not played, not favorite), `SortName`, `DateCreated` and the empty metadata members (`Genres`, `People`, `Studios`, ...); counts are of what the caller may see (a user with no library reads 0).
- **browse.genres.R6** What the user may not see does not contribute: a user with no library lists no genres; a user with a parental limit lists only the genres of the items they may see (with a limit of `PG`, *Horror* and *Drama* are carried only by an `R` movie and a `TV-14` series).

## <a id="studios"></a>GET /Studios and GET /Studios/{name}

### Rules

- **browse.studios.R1** `GET /Studios` lists the distinct studio names like genres do (browse.genres.R1 to R4, same parameters plus `EnableUserData`), but each item carries `UserData` (`Key` `Studio-<name>`) unless `EnableUserData=false`; counts follow browse.genres.R2 (also present when `IncludeItemTypes` is given). `EnableTotalRecordCount=false` gives a `TotalRecordCount` of 0.
- **browse.studios.R2** `GET /Studios/{name}` answers the studio for any name (zero counts for one nothing carries), with the counts of browse.genres.R5.

## <a id="persons"></a>GET /Persons and GET /Persons/{name}

### Rules

- **browse.persons.R1** `GET /Persons` lists the distinct people credited on what the user may see, ordered by name; each item carries `UserData` (`Key` `Person-<name>`) unless `EnableUserData=false`, no counts (`Fields` does not add them). A person credited twice (actor and director) is one entry. A user with no library lists nobody.
- **browse.persons.R2** `PersonTypes` keeps people with a credit of one of those types (`Actor`, `Director`, `Writer`, `GuestStar`, ...), `ExcludePersonTypes` drops people whose only credits are of those types; `AppearsInItemId` keeps the people credited on that item; `SearchTerm`, `NameStartsWith`, `NameStartsWithOrGreater`, `NameLessThan`, `StartIndex`, `Limit` and `IsFavorite` behave as for genres; `ParentId` narrows to the items under it.
- **browse.persons.R3** `GET /Persons/{name}` answers the person with `ChildCount` and the type counts of the items crediting them (episodes count only for credits on the episode itself); a name nobody credits is `404` (problem document). **Divergence:** a name only content the user may not see credits is also `404` (the reference answers `200` with zero counts and so reveals that the person exists); that scenario is skipped on the reference.
- **browse.persons.R5** `BaseItemPerson` carries `PrimaryImageTag` when the person has a picture that can be served, and omits the field when they have none, with `ImageBlurHashes.Primary` mapping that tag to a placeholder for the picture. The tag is what a client builds the picture's URL from: `GET /Items/{personId}/Images/Primary` answers the person's photo through the ordinary item image route. A credit's photo comes from the item's `.nfo` `<thumb>` when it is an `http(s)` URL, and otherwise from the `.actors` folder beside the media (`.actors/<Name>.jpg`, `.actors/<Name>/folder.jpg`), the convention the media tools use to keep a credit photo next to the video (followed here; not measured on the reference, whose NFO reader was not enabled in the throwaway container): a `<thumb>` that names a path this server cannot read — a Jellyfin install writes its own config path there, `/config/data/metadata/People/B/Brad Pitt/folder.jpg` — is dropped at scan time instead of being stored, because measured on a migrated library 20,657 credits carried such a path and exactly one had a photo that could be read, so every cast card answered `404`. A source that fails to download once is not advertised again until the retry window passes. **Divergence:** the reference server downloads the picture during the scan and publishes the tag only once its own copy exists; Tinyfin records the source at scan time and downloads on the first request (`DIV-33`), and a private address is refused outright where the reference fetches it (`DIV-34`). The happy path — tag present, picture served — is the same on both.
- **browse.persons.R4** The credits of an item come from its `.nfo`: `<actor>` entries ordered by their `<order>` (entries without one after, in document order) with `Role` from `<role>` and `Type` `Actor` unless `<type>` says otherwise; then each `<director>` (`Type` `Director`); then `<credits>` and `<writer>` entries (`Type` `Writer`). A name repeated with the same type is one credit. `<studio>` and `<genre>` entries repeated in another case are one (the first spelling is kept).

## <a id="years"></a>GET /Years and GET /Years/{year}

### Rules

- **browse.years.R1** `GET /Years` lists the distinct production years of what the user may see (`Name` the year, `Type` `Year`, `UserData` `Key` `Year-<year>`). Without a sort the order is the order the years were first met; `SortBy=SortName` orders by year (ascending or `SortOrder` Descending). `IncludeItemTypes`, `ParentId`, `StartIndex` and `Limit` behave as for genres.
- **browse.years.R2** `GET /Years/{year}` answers the year for any number (`ChildCount` 0 for a year nothing has), with the counts of browse.genres.R5 and `SortName` the year zero-padded to ten digits; a year that is not a number is `400`. `GET /Items/{id}` opens a year by its identifier.
- **browse.years.R3** `TotalRecordCount` of `GET /Years` is the number of years before paging. **Divergence:** the reference reports a larger number (the items it scanned); the scenario is skipped on the reference.

## <a id="facets"></a>GET /Items/Filters and GET /Items/Filters2

### Rules

- **browse.facets.R1** `GET /Items/Filters` (the older facets) answers `{Genres, Tags, OfficialRatings, Years}`: names sorted, years ascending, of the items under `ParentId` (recursive; `IncludeItemTypes` does not narrow them). Without a `ParentId` every list is empty.
- **browse.facets.R2** `GET /Items/Filters2` answers `{Genres, Tags, AudioLanguages, SubtitleLanguages}` where `Genres` are `{Name, Id}` pairs (the ids of the genre items), over the items under `ParentId` (default: everything the user may see), narrowed by `IncludeItemTypes`; `IsSeries`, `IsKids`, `IsNews`, `IsSports`, `IsAiring` with `true` give empty lists. `AudioLanguages` is `[{Name, Value}]` and is filled only when item types are given.

## <a id="item-filters"></a>Filters of GET /Items that name a name

### Rules

- **browse.item.R1** A named item's identifier opens it through `GET /Items/{id}` and `GET /Users/{userId}/Items/{id}` with the shape of browse.genres.R5; an identifier that names no item, library or name is `404`.
- **browse.item.R2** `Genres` and `Studios` take values separated by `|`; an item matches when it carries any of them, without regard to case. A comma is part of a name (`Drama,Horror` is one name that nothing carries). `Tags` match without regard to case.
- **browse.item.R3** `GenreIds`, `StudioIds` and `PersonIds` (comma-separated named-item identifiers) keep items carrying any of those names; when none of the identifiers names anything the result is empty.
- **browse.item.R4** `Person` keeps items crediting that exact name (case matters here); with `PersonTypes` only credits of those types count; `PersonTypes` without `Person` changes nothing.

## <a id="similar"></a>GET /Items/{itemId}/Similar

**Auth.** Any authenticated user. **Request.** `Limit`, `Fields`, `UserId`. Aliases: `/Movies/{itemId}/Similar`, `/Shows/{itemId}/Similar`, `/Trailers/{itemId}/Similar`.

### Rules

- **browse.similar.R1** For a movie or a series the answer is the items of the same type, not the item itself, that share at least one genre, studio, person or tag with it, the most shared values first and ties by sort name; `TotalRecordCount` is the number returned. `Limit` keeps the first ones. Other types (episodes, seasons) and items that share nothing get an empty list.
- **browse.similar.R2** Only what the caller may see is listed; `Fields` shapes the items like any list.
- **browse.similar.R3** A missing or unreadable item is `404`, an id that is not a GUID `400`, anonymous `401`.

## <a id="recommendations"></a>GET /Movies/Recommendations

**Request.** `UserId`, `ParentId`, `Fields`, `CategoryLimit`, `ItemLimit`.

### Rules

- **browse.recommendations.R1** The answer is an array of categories `{RecommendationType, BaselineItemName, CategoryId, Items}`. For a user who played nothing it is empty.
- **browse.recommendations.R2** From the movies the user played last (newest first): `SimilarToRecentlyPlayed` (items similar to the movie, `BaselineItemName` its name, `CategoryId` its id), then `HasDirectorFromRecentlyPlayed` and `HasActorFromRecentlyPlayed` (movies with a director / actor of it, `BaselineItemName` the person, `CategoryId` the person's id), each only with unplayed movies the caller may see and not the baseline. `CategoryLimit` (default 5) bounds the categories and `ItemLimit` (default 8) the items of each.

## <a id="suggestions"></a>GET /Items/Suggestions

### Rules

- **browse.suggestions.R1** Answers a query result of a random sample of the items the user may see: the types named by `Type` (default movies and series), `MediaType` narrows them, `Limit` (default 12) and `StartIndex` page, and `TotalRecordCount` is the number of candidates without a `Limit` and the number of items returned with one. Anonymous is `401`.

## <a id="item-extras"></a>GET /Items/{itemId}/Intros and GET /Items/{itemId}/Collections

### Rules

- **browse.intros.R1** `GET /Items/{itemId}/Intros` (and the per-user form) answers an empty query result for a readable item and `404` for a missing one.
- **browse.collections.R1** `GET /Items/{itemId}/Collections` answers a query result of the collections (`BoxSet`) the item is a direct member of, as far as the caller may see them; an item in none gets an empty result; a missing item is `404`.

## Index of rules to scenarios

| Rule | Scenario |
|---|---|
| `browse.genres.R1` | `browse.genres.list` |
| `browse.genres.R2` | `browse.genres.list` |
| `browse.genres.R3` | `browse.genres.query` |
| `browse.genres.R4` | `browse.genres.query` |
| `browse.genres.R5` | `browse.genres.single` |
| `browse.genres.R6` | `browse.genres.query` |
| `browse.studios.R1` | `browse.studios` |
| `browse.studios.R2` | `browse.studios` |
| `browse.persons.R1` | `browse.persons` |
| `browse.persons.R2` | `browse.persons` |
| `browse.persons.R3` | `browse.persons` |
| `browse.persons.R3` | `browse.persons.hidden` |
| `browse.persons.R4` | `browse.persons.credits` |
| `browse.persons.R5` | `browse.persons.image` |
| `browse.years.R1` | `browse.years` |
| `browse.years.R2` | `browse.years` |
| `browse.years.R3` | `browse.years.total` |
| `browse.facets.R1` | `browse.facets` |
| `browse.facets.R2` | `browse.facets` |
| `browse.item.R1` | `browse.genres.single` |
| `browse.item.R2` | `browse.item-filters` |
| `browse.item.R3` | `browse.item-filters` |
| `browse.item.R4` | `browse.item-filters` |
| `browse.similar.R1` | `browse.similar` |
| `browse.similar.R2` | `browse.similar` |
| `browse.similar.R3` | `browse.similar` |
| `browse.recommendations.R1` | `browse.recommendations` |
| `browse.recommendations.R2` | `browse.recommendations` |
| `browse.suggestions.R1` | `browse.suggestions` |
| `browse.intros.R1` | `browse.intros-and-collections` |
| `browse.collections.R1` | `browse.intros-and-collections` |
