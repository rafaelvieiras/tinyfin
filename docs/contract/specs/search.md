# Search

Contract for text search: `GET /Search/Hints` (ranked hints over media items and over the genre, studio and people
names the user can see) and the `SearchTerm` of `GET /Items` (which items.md covers for listing; the parts that depend
on the shared matching rules are here).

Scenarios: [`../scenarios/search.json`](../scenarios/search.json). Fixture state: [`../fixtures.json`](../fixtures.json).
Rule IDs are stable (`search.<topic>.R<n>`); scenarios reference them through `covers`.

## Fixture facts this spec relies on

* Always present: library `Movies` (*Alpha Movie* 2015, *Bravo Movie* 2020, *Charlie Movie* 2018, no metadata) and
  `Shows` (the series *Test Show* with seasons 1 and 2 and three episodes, all named *Test Show*).
* Lazy library `Search` (movies, with `.nfo` metadata): *Zeta Quest* (2011, Action and Comedy, studio Acme Pictures,
  actors Ann Actor and Bob Player, director Dana Director, tag `cult`, `PG`), *Yankee Dawn* (2012, Drama, studios Acme
  Pictures and Bolt Films, actor Ann Actor, director Dana Director, tags `cult` and `indie`, `R`), *Xray Vision* (2013,
  Action, studio Bolt Films, actor Cy Cast, director Evan Eye, `G`), *Whiskey Alpha* (2013, Animation and Family,
  studio Cartoon Co, actor Ann Actor, `G`), *Alpha Wolf* (2014, Horror, `R`) and *Writers Cut* (2017, writers Wally
  Writer and Walt Writer, directors Dana Director and Dee Second, actors Ann Actor, Gus Guest (type `GuestStar`),
  Zed Last and Dana Director, studio Acme Pictures, genre Action).
* Lazy library `SearchShows` (series): *Search Show* (2016, Drama and Comedy, studio Bolt Films, actor Ann Actor,
  `TV-14`) with one episode *Pilot Search* (season 1, episode 1, director Dana Director).
* Lazy library `Punct` (movies named with punctuation and accents): `Mr. O'Neil-Smith_Jr: Part 2! & 3`,
  `Café Über Straße`, `Dot.Dash` and `Comma,Join`.
* The `nofolders` user has no library; `rating10` has `MaxParentalRating` 10.

## Conventions

* A *name* is a genre, a studio or a person: it is not an item of a library but exists while an item the user can see
  carries it; its identifier is derived from its kind and its name (browse.md).
* The *search key* of a text is its letters and digits, lower-cased with accents removed (`ß` is `ss`); apostrophes and
  quotation marks are dropped, every other character (spaces, `-`, `_`, `.`, `:`, `!`, `&`, `(`, ...) separates words,
  and runs of separators are one space.

---

## <a id="hints"></a>GET /Search/Hints

**Purpose.** Find items and names that match a text, as compact hints (the web client's search box).

**Auth.** Any authenticated user; `UserId` may name another user only for an administrator (items.scope).

**Request.** Query: `SearchTerm` (required), `StartIndex`, `Limit`, `UserId`, `IncludeItemTypes`, `ExcludeItemTypes`,
`MediaTypes`, `ParentId`, `IsMovie`, `IsSeries`, `IsNews`, `IsKids`, `IsSports`, `IncludePeople`, `IncludeMedia`,
`IncludeGenres`, `IncludeStudios`, `IncludeArtists`.

**Responses.**

| Status | Condition |
|---|---|
| 200 | `{"SearchHints": [...], "TotalRecordCount": n}` |
| 400 | Missing, empty or blank `SearchTerm`; a value that cannot be bound (`Limit`, `StartIndex`, `ParentId`, a boolean) |
| 401 | Anonymous |

### Rules

- **search.hints.R1** `SearchTerm` is required: absent, empty or only white space is answered `400` (a validation problem naming `searchTerm`).
- **search.hints.R2** Each hint carries `ItemId` and `Id` (the same value), `Name`, `Type`, `MediaType`, `Artists` (always empty), `ChannelId` and, when they have a value, `ProductionYear`, `RunTimeTicks`, `IndexNumber`, `ParentIndexNumber`, `Series` (the series name of a season or an episode) and `IsFolder` (`true`, present only for folders such as series and seasons). `MatchedTerm` is not present. `MediaType` is `Video` for movies and episodes and `Unknown` for series, seasons and names. A season without a year of its own shows its series' year.
- **search.hints.R3** A hint matches when the search key of the term is contained in the search key of the item's name, or the term's sort form is contained in the item's sort name. The term is trimmed; punctuation in the term is ignored the same way as in names; a term with no letter or digit matches nothing. Words must be adjacent and in order; a separator is never removed (`alphamovie` does not find `Alpha Movie`, `dotdash` does not find `Dot.Dash`), except that `,`, `&` and a few typographic marks also join (`commajoin` finds `Comma,Join`).
- **search.hints.R4** The results are the matching media items of the libraries the user may see (never folders or years) and, when no item type list or `MediaTypes` narrows them, the matching genre, studio and person names carried by those items (and by the playlists the user may read). A name appears once however many items carry it; names compare without regard to case.
- **search.hints.R5** Results are ordered by relevance, then by sort name with a binary comparison: (1) the key starts with the term, (2) the term ends a word (is followed by a space), (3) seasons and episodes, (4) everything else. A person's sort name is the name as written, so capitalised names sort ahead of the lower-case sort names of items, genres and studios of the same rank; numbers in a season or episode sort name come first of all.
- **search.hints.R6** `IncludeItemTypes` names what is searched: item types select media items, `Person`, `Genre` and `Studio` select the names; a list without a name type leaves names out and a list with only name types leaves media out. `ExcludeItemTypes` applies only when `IncludeItemTypes` is empty. `IncludePeople`, `IncludeGenres` and `IncludeStudios` false leave that kind out. `IncludeMedia` false leaves the media items out and keeps the genres only (not people or studios). `IncludeArtists` is accepted and has no effect.
- **search.hints.R7** `MediaTypes` keeps the media items of those media types (`Video`: movies, episodes, trailers and videos); names have no media type and are left out; a type with no items (`Audio`) matches nothing.
- **search.hints.R8** `ParentId` narrows the media items to the subtree of a library or folder (a series, a season): seasons and episodes below it are matched; names are not (they are not under a parent). A `ParentId` that is not a GUID is `400`.
- **search.hints.R9** `Limit` keeps the first results, `StartIndex` then skips from those, and `TotalRecordCount` counts what `Limit` kept (not the matches beyond it): `Limit=3&StartIndex=2` is one hint and a total of 3. `Limit=0` (and a negative limit) is an empty result with a total of 0. Without a `Limit`, `StartIndex` skips from the whole list and the total is the number of matches. **Divergence note:** with a `Limit` the reference chooses which results it keeps in an unspecified order; the scenarios assert only counts.
- **search.hints.R10** `IsSeries`, `IsNews`, `IsKids` and `IsSports` with `true` match nothing (only live TV programs carry them); with `false` they change nothing; `IsMovie` changes nothing either way. A value that is not a boolean is `400`.
- **search.hints.R11** What the user may not see is not found, and neither is a name only that content carries: libraries the policy leaves out, items over the parental limit, tag rules. A user with no library finds nothing.
- **search.hints.R12** Anonymous requests are answered `401`.

## <a id="items"></a>GET /Items with SearchTerm

The listing rules (parents, types, paging, shaping) are in items.md; these are the search-specific rules.

### Rules

- **search.items.R1** A `SearchTerm` with no `IncludeItemTypes` and no `ParentId` also finds genre, studio and person names (as items of type `Genre`, `Studio`, `Person`) among the matching items, ranked together. With a type list, or under a parent, only items are found.
- **search.items.R2** A type list that names no name type finds no names (`SearchTerm=ann&IncludeItemTypes=Movie` is empty).
- **search.items.R3** A season or an episode is matched by its own name only: not by the name of its series.
- **search.items.R4** The term and the names are compared exactly as for hints (search.hints.R3): case, accents and punctuation do not matter. Unlike hints, a term made only of punctuation (`...`, `&`, `%`) restricts nothing: every item matches. The result is ranked as in search.hints.R5 and `SortBy` / `SortOrder` are ignored (items.search.R2).

## Index of rules to scenarios

| Rule | Scenario |
|---|---|
| `search.hints.R1` | `search.hints.auth-and-required` |
| `search.hints.R2` | `search.hints.shape-movie` |
| `search.hints.R2` | `search.hints.shape-other` |
| `search.hints.R3` | `search.hints.matching` |
| `search.hints.R3` | `search.hints.matching-names` |
| `search.hints.R4` | `search.hints.shape-other` |
| `search.hints.R4` | `search.hints.kinds` |
| `search.hints.R5` | `search.hints.ranking` |
| `search.hints.R6` | `search.hints.kinds` |
| `search.hints.R7` | `search.hints.media-types` |
| `search.hints.R8` | `search.hints.parent` |
| `search.hints.R9` | `search.hints.paging` |
| `search.hints.R10` | `search.hints.flags` |
| `search.hints.R11` | `search.hints.access` |
| `search.hints.R12` | `search.hints.auth-and-required` |
| `search.items.R1` | `search.items.names` |
| `search.items.R2` | `search.items.names` |
| `search.items.R3` | `search.items.matching` |
| `search.items.R4` | `search.items.matching` |
