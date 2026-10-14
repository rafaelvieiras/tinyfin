# Collections (`BoxSet`)

Contract for collections: the `BoxSet` item type, the routes that create, fill, empty and delete a collection, how a
collection is listed, shown and counted, what it derives from its members, and the collections a library scan builds
by itself from the `<set>` of movie metadata files.

Scenarios: [`../scenarios/collections.json`](../scenarios/collections.json). Fixture state: [`../fixtures.json`](../fixtures.json).
Rule IDs are stable (`collections.<topic>.R<n>`); scenarios reference them through `covers`.

## Fixture facts this spec relies on

* Collections are made by the scenarios themselves from the items of `Movies` (*Alpha Movie* 2015, *Bravo Movie* 2020,
  *Charlie Movie* 2018) and `Shows` (the series *Test Show* with three episodes). Every scenario deletes what it creates.
* Lazy libraries `Sets` (movies, library option `AutomaticallyAddToCollection` on) and `SetsOff` (movies, option off).
  `Sets` holds eleven movies whose `.nfo` carries a set: *Saga Alpha One* (2001, Drama, `PG`), *Saga Alpha Two*
  (2002, Action, `R`) and *Saga Alpha Three* (2003, no genre, no rating) in `Alpha Saga` (the third in the older
  `<set>Alpha Saga</set>` text form); *Saga Beta One/Two* in `The Beta Saga`; *Saga Pad One/Two* in
  `  Padded Saga  ` (white space around the name); *Saga Lonely One* alone in `Lonely Saga`; *Saga Case Upper* in
  `Case Saga` and *Saga Case Lower* in `case saga`; *Saga Plain* with no set. `SetsOff` holds *Saga Gamma One/Two*
  in `Gamma Saga`.
* The `collmgr` user has `EnableCollectionManagement`; `alice` has the default policy. Scenarios that restrict
  libraries create a throw-away account.

## Conventions

* A collection is an item of type `BoxSet`, a folder (`IsFolder` true) with `MediaType` `Unknown` and
  `LocationType` `FileSystem`. It lives in its own library, named `Collections`, whose `CollectionType` is `boxsets`.
  The library exists from the first collection on; its view is only shown while a collection is visible
  (collections.list.R8).
* Members keep their own library and parent: putting an item in a collection moves nothing and copies nothing.
  A collection holds each item at most once, and may hold any item type (movies, series, other collections).
* "Management" below means an administrator or a user whose policy has `EnableCollectionManagement`.

---

## <a id="create"></a>POST /Collections

**Purpose.** Create a collection, optionally with its first members.

**Auth.** Management (collections.auth).

**Request.** Query: `Name` (the collection's name), `Ids` (comma-separated member item ids), `ParentId`, `IsLocked`.

**Responses.**

| Status | Condition |
|---|---|
| 200 | `{"Id": "<32 hex>"}` |
| 400 | An id that names no item, a malformed id or a missing or blank name (collections.create.R6 to R8) |
| 401 / 403 | Anonymous / not allowed to manage collections |

### Rules

- **collections.create.R1** `POST /Collections?Name=X&Ids=a,b` answers `200` with `{"Id": id}`. `GET /Items/{id}` is a `BoxSet` named `X`, a folder, with `MediaType` `Unknown`, `LocationType` `FileSystem` and `DisplayOrder` `PremiereDate`; its `ChildCount` and `RecursiveItemCount` are the number of members. The members are still ordinary items of their own library.
- **collections.create.R2** `GET /Items?ParentId={id}` lists exactly the members given in `Ids`.
- **collections.create.R3** A collection can be created with no `Ids`: it is empty (`ChildCount` 0) and is listed like any other.
- **collections.create.R4** The collection id follows the name: creating a name that already exists answers the same `Id`, is not a second collection, and the members become exactly the ids of the new request.
- **collections.create.R5** An id repeated in `Ids` is one member.
- **collections.create.R6** An id that names no item (`00000000000000000000000000000001`), alone or beside valid ones, is answered `400`.
- **collections.create.R7** A rejected request creates no collection. **Divergence:** the reference creates the (empty) collection and then answers `400`/`500`; the scenario asserts the safe result and is skipped on the reference.
- **collections.create.R8** A missing or blank `Name` and an id that is not a GUID are answered `400`. **Divergence:** the reference answers `500` for both (an unhandled error, and it keeps the collection when the id is malformed); the scenario asserts `400` and is skipped on the reference.
- **collections.create.R9** `ParentId` and `IsLocked` are accepted and do not move the collection: it stays in the collections library.

## <a id="auth"></a>Who may manage collections

- **collections.auth.R1** Anonymous requests to `POST /Collections`, `POST` and `DELETE /Collections/{id}/Items` and `DELETE /Items/{id}` are answered `401`.
- **collections.auth.R2** A user who is not an administrator and lacks `EnableCollectionManagement` is answered `403` by the three collection routes and `401` (body `"Unauthorized access"`) by `DELETE /Items/{id}`. Nothing changes, and the collection reads `CanDelete` false for that user.
- **collections.auth.R3** With `EnableCollectionManagement` the user may create, add, remove and delete (`204` for the last three) and reads `CanDelete` true. Deleting a collection needs this permission, not `EnableContentDeletion`.

## <a id="add"></a>POST /Collections/{collectionId}/Items

**Purpose.** Add members to a collection.

**Auth.** Management. **Request.** Query `Ids` (comma-separated).

| Status | Condition |
|---|---|
| 204 | Done (also when nothing was to add) |
| 400 | An id that names no item; a `collectionId` that is not a collection; a `collectionId` that is not a GUID (problem document) |

- **collections.add.R1** `204` with an empty body; the new members are listed with the others.
- **collections.add.R2** Adding an item that is already a member changes nothing (`204`, no second link).
- **collections.add.R3** An id that names no item is answered `400`.
- **collections.add.R4** An unknown id, the id of an item that is not a collection and a `collectionId` that is not a GUID are answered `400`; the last one is a validation problem document naming `collectionId`.
- **collections.add.R5** No `Ids`, or ids that are not GUIDs, is `204` and changes nothing.

## <a id="remove"></a>DELETE /Collections/{collectionId}/Items

- **collections.remove.R1** `204` with an empty body; the removed items are no longer listed in the collection and still exist.
- **collections.remove.R2** Ids that are not members, ids that name nothing and a missing `Ids` are ignored (`204`). Removing the last member leaves an empty collection.
- **collections.remove.R3** A `collectionId` that names no collection is answered `400`.

## <a id="delete"></a>DELETE /Items/{itemId}

**Purpose.** Delete a collection. Tinyfin never deletes media files: any other item is refused (`403`, or `401`
for a caller without content-deletion rights, as the reference answers a caller that may not delete); the reference
deletes the file, so no scenario sends it.

- **collections.delete.R1** `204` with an empty body; `GET /Items/{id}` is then `404`, the collection is not listed any more, a second delete is `404` and a malformed id is `400` (problem document).
- **collections.delete.R2** The members are untouched: they still exist and keep their user data.

## <a id="item"></a>What a collection derives from its members

- **collections.item.R1** At creation: `ProductionYear` is the earliest production year of the members, `Genres` the members' genres without repeats, in member order, and `OfficialRating` the lowest rating of the members (`PG` below `R`).
- **collections.item.R2** `SortName` follows the usual sort-name rules: `The Conf Derived` sorts as `conf derived`.
- **collections.item.R3** Members without a year, genre or rating contribute nothing; a collection built from such members has no `OfficialRating` and no genres.
- **collections.item.R4** Adding members lowers `OfficialRating` to the lowest of the old and the new, fills `Genres` from the new members only while the collection has none, and leaves `ProductionYear` as it is.
- **collections.item.R5** Removing members changes none of the three. `POST /Items/{id}/Refresh` recomputes all of them from the current members (after a short delay).
- **collections.item.R6** A member may be a series: `ChildCount` counts the direct members, `RecursiveItemCount` and `UserData.UnplayedItemCount` count the playable items below them (three episodes and a movie make 4), and `GET /Items?ParentId={id}&Recursive=true` lists the members and everything below them.

## <a id="userdata"></a>Played state

A collection has no played state of its own: it reports one derived from the leaves below its members.

- **collections.userdata.R1** `POST /UserPlayedItems/{id}` on a collection marks every leaf below it played for the user (`Played` true, `PlayCount` 1); the answer and the collection report `Played` true, `UnplayedItemCount` 0, `PlayedPercentage` 100.
- **collections.userdata.R2** `DELETE /UserPlayedItems/{id}` resets every leaf (`Played` false, `PlayCount` 0); the collection reports `Played` false, `UnplayedItemCount` the number of leaves and `PlayedPercentage` 0. Unlike a series, a collection mark is written to the leaves.
- **collections.userdata.R3** Between the two, `UnplayedItemCount` and `PlayedPercentage` follow the members' own state (one of two played: 1 and 50). The `IsPlayed` filter agrees.

## <a id="list"></a>Listing

- **collections.list.R1** `GET /Items?ParentId={collections library}` lists the collections (and nothing else). They are also found by `GET /Items?IncludeItemTypes=BoxSet&Recursive=true`.
- **collections.list.R2** `GET /Items?ParentId={content library}&IncludeItemTypes=BoxSet&Recursive=true` lists the collections that have at least one direct member in that library, and none for a library none of them draws from. The same listing with `IncludeItemTypes=Movie` holds no collection.
- **collections.list.R3** The members of a collection list in premiere-date order (production year when there is no date) unless the request sorts.
- **collections.list.R4** `SortBy`/`SortOrder`, `StartIndex` and `Limit` apply to the members; `TotalRecordCount` is the number of members.
- **collections.list.R5** `Recursive=true` on a collection sorts by name by default; `ExcludeItemIds` applies.
- **collections.list.R6** `CollapseBoxSetItems=true` replaces the items that are members of a visible collection by that collection: the members drop out of the result and each collection with a matching member takes their place, whatever type the query names. Inside a collection, and with `false`, nothing is collapsed.
- **collections.list.R7** The collections library appears in `GET /Library/VirtualFolders` (`CollectionType` `boxsets`, the same `ItemId` as its view) from the first collection on; a collection's `ParentId` is a folder item named `collections`, and the library is among its `Ancestors`.
- **collections.list.R8** The `Collections` view (`CollectionFolder`, `CollectionType` `boxsets`) is shown in `GET /UserViews` and in the root listing only while the user can see at least one collection, and goes away with the last one.
- **collections.list.R9** `GET /Items/Latest` leaves collections out unless `IncludeItemTypes` names `BoxSet`; `GET /Items/Counts` reports them in `BoxSetCount`.

## <a id="visibility"></a>Visibility

- **collections.visibility.R1** A collection is listed for a user who can see the collections library when it is empty, or when at least one member is visible to that user; one whose members are all hidden is not listed, and opening it as a parent is `401`. Without access to the collections library no collection is listed and the library view is not shown.
- **collections.visibility.R2** `GET /Items/{id}` of a collection is readable (`200`) when it is empty or has a visible member, whatever libraries the user may see; it is `404` when it has members and none is visible.
- **collections.visibility.R3** The `Collections` view follows R1: it is shown to a user who can see at least one collection and hidden from one who cannot.
- **collections.visibility.R4** A user sees only the members visible to them when listing a collection, while `ChildCount` and `RecursiveItemCount` count every member.

## <a id="auto"></a>Automatic collections

When a library scan runs for a movies library whose library option `AutomaticallyAddToCollection` is `true`, movies
that name the same set are grouped in a collection. The set is the movie `.nfo`'s `<set><name>X</name></set>` or `<set>X</set>`.

- **collections.auto.R1** At least two movies of enabled libraries that name the same set make a collection named after it, with those movies as members (year order), its year, genres and rating derived as in collections.item.R1. It lives in the collections library like any other.
- **collections.auto.R2** A library whose option is off (the default) creates nothing, whatever its movies name.
- **collections.auto.R3** A set named by a single movie creates nothing.
- **collections.auto.R4** Movies are grouped by the name exactly as written: `Case Saga` and `case saga` are two sets, and so are `  Padded Saga  ` and `Padded Saga`. The collection's name is the written name with surrounding white space removed.
- **collections.auto.R5** Both `.nfo` forms are read; a movie naming a collection is still a plain movie (`Type` `Movie`).
- **collections.auto.R6** `POST /Library/Refresh` (and the first scan of a library) adds movies that name the set to the existing collection of that name, again including ones removed by hand, keeps members added by hand, never removes anything and builds a deleted collection again.
- **collections.auto.R7** Refreshing a single library (`POST /Items/{libraryId}/Refresh`) does not run this step.

## Not exercisable or not specified

* Removal when a movie's file disappears: the member link goes with the item. Tinyfin's scan does not remove items whose
  files vanished at all (an existing gap, not specific to collections), so no scenario drives it.
* The reference moves a movie out of its automatic collection after a later scan in some cases (an edited `<set>`
  observed once and not reproduced); Tinyfin never removes a member on a scan.
* `IncludeItemTypes=BoxSet` together with `CollapseBoxSetItems=true` returns, on the reference, only collections that
  themselves hold collections; Tinyfin returns every visible collection that is not inside another. Not asserted.
* Deleting the `Collections` library by name (`DELETE /Library/VirtualFolders`) leaves the collections on the
  reference; Tinyfin deletes them with the library. Not asserted.
* A library id in `Ids` is accepted and ignored by the reference; Tinyfin does the same, a library id is not an item.
* `GET /Items?IsPlayed=true` lists empty collections on the reference (nothing below them is unplayed); Tinyfin does
  not (a folder with no leaves is not played).

## Rule → scenarios

| Rule | Scenarios |
|---|---|
| `collections.create.R1` | `collections.create.basic` |
| `collections.create.R2` | `collections.create.basic` |
| `collections.create.R3` | `collections.create.empty` |
| `collections.create.R4` | `collections.create.same-name` |
| `collections.create.R5` | `collections.create.duplicate-ids` |
| `collections.create.R6` | `collections.create.unknown-id` |
| `collections.create.R7` | `collections.create.rejected-creates-nothing` |
| `collections.create.R8` | `collections.create.invalid-input` |
| `collections.create.R9` | `collections.create.parent-and-locked` |
| `collections.auth.R1` | `collections.auth.anonymous` |
| `collections.auth.R2` | `collections.auth.no-permission` |
| `collections.auth.R3` | `collections.auth.manager` |
| `collections.add.R1` | `collections.add.appends` |
| `collections.add.R2` | `collections.add.appends` |
| `collections.add.R3` | `collections.add.errors` |
| `collections.add.R4` | `collections.add.errors` |
| `collections.add.R5` | `collections.add.errors` |
| `collections.remove.R1` | `collections.remove.basic` |
| `collections.remove.R2` | `collections.remove.basic` |
| `collections.remove.R3` | `collections.remove.basic` |
| `collections.delete.R1` | `collections.delete.basic` |
| `collections.delete.R2` | `collections.delete.basic` |
| `collections.item.R1` | `collections.item.derived-metadata` |
| `collections.item.R2` | `collections.item.derived-metadata` |
| `collections.item.R3` | `collections.item.derived-metadata` |
| `collections.item.R4` | `collections.item.metadata-on-change` |
| `collections.item.R5` | `collections.item.metadata-on-change` |
| `collections.item.R6` | `collections.item.series-member` |
| `collections.userdata.R1` | `collections.userdata.leaves` |
| `collections.userdata.R2` | `collections.userdata.leaves` |
| `collections.userdata.R3` | `collections.userdata.leaves` |
| `collections.list.R1` | `collections.list.library` |
| `collections.list.R2` | `collections.list.by-content-library` |
| `collections.list.R3` | `collections.add.appends` |
| `collections.list.R4` | `collections.list.members-sort-paging` |
| `collections.list.R5` | `collections.list.members-sort-paging` |
| `collections.list.R6` | `collections.list.collapse` |
| `collections.list.R7` | `collections.list.library` |
| `collections.list.R8` | `collections.list.library-view-appears` |
| `collections.list.R9` | `collections.list.latest-and-counts` |
| `collections.visibility.R1` | `collections.visibility.restricted-user` |
| `collections.visibility.R2` | `collections.visibility.restricted-user` |
| `collections.visibility.R3` | `collections.visibility.restricted-user` |
| `collections.visibility.R4` | `collections.visibility.partial-members` |
| `collections.auto.R1` | `collections.auto.build` |
| `collections.auto.R2` | `collections.auto.option-off` |
| `collections.auto.R3` | `collections.auto.build` |
| `collections.auto.R4` | `collections.auto.build` |
| `collections.auto.R5` | `collections.auto.build` |
| `collections.auto.R6` | `collections.auto.rescan` |
| `collections.auto.R7` | `collections.auto.rescan` |
