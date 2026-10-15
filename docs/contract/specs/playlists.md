# Playlists

Contract for playlists: the `Playlist` item type, the routes that create, read, change and share a playlist and
its entries, who may read and edit one, how a playlist is listed (the Playlists view) and what it derives from
its entries.

Scenarios: [`../scenarios/playlists.json`](../scenarios/playlists.json). Fixture state: [`../fixtures.json`](../fixtures.json).
Rule IDs are stable (`playlists.<topic>.R<n>`); scenarios reference them through `covers`.

## Fixture facts this spec relies on

* Playlists are made by the scenarios from the items of `Movies` (*Alpha Movie*, *Bravo Movie*, *Charlie Movie*, each
  6 s long), `Shows` (the series *Test Show* with episodes S01E01, S01E02, S02E01), the lazy `Rated` library
  (*Rated G*, *Rated R*) and the lazy `Search` library (see search.md). Every scenario deletes what it creates.
* `alice` and `collmgr` have the default policy; `nofolders` has no library; `rating10` has `MaxParentalRating` 10.

## Conventions

* A playlist is an item of type `Playlist`: a folder (`IsFolder` true), `LocationType` `FileSystem`, with a
  `MediaType` of its own (`Video`, `Audio`, ...). It sits under the *Playlists folder* (type `ManualPlaylistsFolder`,
  `CollectionType` `playlists`), which is created with the first playlist and is its `ParentId`.
* A playlist holds *entries*: ordered links to items. The same item may appear more than once. An entry is named
  by the id of its item (`PlaylistItemId` equals the item's id), so removing "an entry" removes every entry of that item.
* Counts: `TotalRecordCount` of a listing counts the playlists the user may read. **Divergence note:** the reference counts the playlists of the whole query before it hides the unreadable ones, so its `TotalRecordCount` can exceed the items it returns; scenarios of other users assert the items.
* A playlist has an *owner* (its creator, or the user named at creation), a flag that makes it *open* to every
  user (`OpenAccess`) and a list of *shares*: users with an `CanEdit` flag.
* *Read* access: the owner, a user it is shared with, anybody when it is open. *Edit* access: the owner and shares
  with `CanEdit`. A playlist the user may not read does not exist for them (`404`, the JSON string
  `"Playlist not found"`); one they may read but not edit answers `403` to changes. Administrators have no
  extra access to playlists of other users.

* Every route below needs an authenticated user.

- **playlists.auth.R1** Anonymous requests to any `/Playlists` route are answered `401`.

---

## <a id="create"></a>POST /Playlists

**Purpose.** Create a playlist, optionally with entries and shares.

**Auth.** Any authenticated user (anonymous is `401`).

**Request.** JSON body `{Name, Ids, UserId, MediaType, Users, IsPublic}` (`Name` required). `name`, `ids`, `userId`
and `mediaType` in the query are the older form and take precedence over the body.

**Responses.**

| Status | Condition |
|---|---|
| 200 | `{"Id": "<32 hex>"}` |
| 400 | No `Name`; an `Ids` value that is not a GUID or names no item the caller can read |
| 403 | `UserId` names another user and the caller is not an administrator |

### Rules

- **playlists.create.R1** `POST /Playlists` with `{"Name": X, "Ids": [a, b, a]}` answers `200` with the id of a new `Playlist` item named `X` whose entries are `a`, `b`, `a` in that order (an id may repeat). `GET /Playlists/{id}` (playlists.read.R1) and `GET /Items/{id}` (playlists.item.R1) read it back. Two playlists may have the same name.
- **playlists.create.R2** A request with a JSON body and no `IsPublic` creates an open playlist; `IsPublic` false a private one; a request with no body at all (name and ids in the query) a private one. `Users` lists the shares (`UserId`, `CanEdit`).
- **playlists.create.R3** `MediaType` is the one given; when it is absent (or `Unknown`) it is the media type of the first entry that has one (`Video` for movies and episodes), and `Audio` when there is none.
- **playlists.create.R4** A playlist may be created with no entries: `ChildCount` 0, `RunTimeTicks` 0, `MediaType` `Audio` unless given.
- **playlists.create.R5** An id of a series, season or folder stands for the playable items below it, in listing order (episodes by season then episode); a collection stands for its members; a playlist for its entries. The entries of the new playlist are those items.
- **playlists.create.R6** A missing `Name` is `400` (a validation problem); an id that is not a GUID is `400` (a validation problem); an id that names no item the caller can read is `400` with the plain text body `Error processing request.`. A rejected request creates nothing.
- **playlists.create.R7** `UserId` (body or query) names the owner: an administrator may create a playlist for another user (the creator then does not see it unless it is open or shared); anybody else naming another user is `403` (plain text `Error processing request.`) and nothing is created. The id of the caller, or all zeros, is the caller.

## <a id="read"></a>GET /Playlists/{playlistId}

### Rules

- **playlists.read.R1** Answers `{"OpenAccess": bool, "Shares": [{"UserId", "CanEdit"}], "ItemIds": [...]}`: the access flag, every share, and the entries' item ids in order (repeats included). A non-GUID id is `400` (validation problem); a playlist that does not exist or the caller may not read is `404`.

## <a id="entries"></a>Entries

**Routes.** `GET /Playlists/{id}/Items`, `POST /Playlists/{id}/Items` (`ids`, `position`), `DELETE /Playlists/{id}/Items`
(`entryIds`), `POST /Playlists/{id}/Items/{itemId}/Move/{newIndex}`.

### Rules

- **playlists.entries.R1** `GET /Playlists/{id}/Items` answers a query result of the entries in order (an item repeated is listed repeatedly), each a full item plus `PlaylistItemId` (the item's id); `TotalRecordCount` is the number of entries. `Fields`, `EnableUserData`, `EnableImages` shape the items like any list.
- **playlists.entries.R2** `POST .../Items?ids=a,b&position=n` answers `204` and inserts the items before index `n` (default: append). A position past the end appends; a negative position inserts first. Ids that are not GUIDs or name nothing readable are ignored. The same item may be added again.
- **playlists.entries.R3** `DELETE .../Items?entryIds=a,b` answers `204` and removes every entry of each named item; a value that names no entry changes nothing.
- **playlists.entries.R4** `POST .../Items/{itemId}/Move/{newIndex}` answers `204` and moves the first entry of that item to index `newIndex` (the other entries keep their order). An item that is not in the playlist changes nothing. **Divergence:** an index outside the list (`newIndex` below 0 or at or past the number of entries) is `400` here; the reference answers `500` or an unspecified result, and such steps are not scenarios.
- **playlists.entries.R5** `StartIndex` and `Limit` page the entry list; `TotalRecordCount` is the number of entries before paging. A `UserId` of another user is `403` for a user who is not an administrator. An unknown playlist is `404`, a non-GUID `400`.
- **playlists.entries.R6** An entry the caller's parental limits hide (rating above the maximum, blocked tags) is left out of `GET .../Items` and of the entries listed under the playlist (`GET /Items?ParentId=<playlist>`); library access does not hide entries (a user with no library still lists them), and `ItemIds` of the read route and `ChildCount` count every entry.

## <a id="update"></a>POST /Playlists/{playlistId}

### Rules

- **playlists.update.R1** `POST /Playlists/{id}` with `{Name, Ids, Users, IsPublic}` answers `204`; a member left out (or null) is not changed. `Name` renames the playlist (the new name shows in `GET /Items/{id}` and is found by name), `Ids` replaces the entries (expanded like playlists.create.R5; an empty list empties the playlist), `Users` replaces the shares, `IsPublic` sets the access flag. An empty body changes nothing. **Divergence:** the reference applies `Ids` only in some combinations of members (for a private playlist it leaves the entries as they were unless `Name` or `Users` is sent too), and  the reference does not keep a rename sent on its own (a later read shows the old name); the rename scenario is skipped on the reference.
- **playlists.update.R2** Needs edit access: a user who may read but not edit is `403`, one who may not read `404`; a non-GUID id `400`; ids that name nothing readable are `400` and nothing changes (**divergence:** the reference answers `204` and keeps the entries; that step is in the skipped scenario of playlists.update.R1).

## <a id="access"></a>Who may read and edit

### Rules

- **playlists.acl.R1** A private playlist (not open, not shared with the user) is `404` for every route of every other user, administrators included; `GET /Items` lists show only the playlists the user may read.
- **playlists.acl.R2** A playlist shared with a user is readable by them; with `CanEdit` they may also add, remove, move entries, rename and change `Users` / `IsPublic`.
- **playlists.acl.R3** An open playlist is readable by everybody and editable only by the owner and shares with `CanEdit`: add, remove, move and update by another user are `403`.

## <a id="users"></a>Shares

**Routes.** `GET /Playlists/{id}/Users`, `GET`, `POST` and `DELETE /Playlists/{id}/Users/{userId}`.

### Rules

- **playlists.users.R1** `GET .../Users` answers the shares (`[{UserId, CanEdit}]`) to the owner only; any other user who can read the playlist is `403`, one who cannot `404`.
- **playlists.users.R2** `GET .../Users/{userId}` answers `{UserId, CanEdit}` of that user: the owner is `CanEdit` true; a shared user their record; a user with no record `404` (the JSON string `"User permissions not found"`). The owner and users with `CanEdit` may ask about any user; a user who may only read gets `403` for an id other than their own (and `404` for their own when they have no share). **Divergence:** the reference answers the owner's own record when the owner asks about a shared user; that scenario is skipped on the reference.
- **playlists.users.R3** `POST .../Users/{userId}` with `{"CanEdit": bool}` answers `204`, adds the user to the shares or changes their flag; an omitted `CanEdit` is false. Needs edit access (`403`); a non-GUID user id is `400`.
- **playlists.users.R4** `DELETE .../Users/{userId}` answers `204` and removes the share; a user with no share is `404`. **Divergence:** the reference answers `204` and keeps the share; the scenario is skipped on the reference.

## <a id="view"></a>The Playlists view

### Rules

- **playlists.view.R1** `GET /UserViews` lists, among the libraries and ordered with them by name, a view of type `UserView`, `CollectionType` `playlists`, `Name` `Playlists` once the user may read at least one playlist; there is none before. The Playlists folder (type `ManualPlaylistsFolder`) is the last item of the root form of `GET /Items` for every user (items.list.R4).
- **playlists.view.R2** `GET /Items?ParentId=<view or folder id>` lists the playlists the user may read, paged and sorted like any folder listing; `GET /Items/{view id}` is the view's item.
- **playlists.view.R3** Playlists are items of the listings that name the type: `GET /Items?IncludeItemTypes=Playlist&Recursive=true`, search, `Genres`/`Studios` filters. A playlist's `ParentId` is the Playlists folder.

## <a id="delete"></a>DELETE /Items/{itemId} for a playlist

### Rules

- **playlists.delete.R1** The owner deletes the playlist (`204`); it and its entries disappear and the items it linked are untouched.
- **playlists.delete.R2** Another user who may read it is `401` (body `"Unauthorized access"`), one who may not `404` (problem document).

## <a id="item"></a>The playlist item

### Rules

- **playlists.item.R1** `GET /Items/{id}` of a playlist: `Type` `Playlist`, `IsFolder` true, `MediaType`, `ChildCount` and `RecursiveItemCount` the number of distinct items it holds, `RunTimeTicks` the total running time of the entries (a repeated item counts each time), `CanDelete` true for the owner. In list forms `ChildCount` is the number of entries and is present when it is not 0.
- **playlists.item.R2** `Genres` and `Studios` are the distinct genres and studios of the entries in order of first appearance, `OfficialRating` the lowest rating of the entries (those with a rating), updated when the entries change. **Divergence:** the reference refreshes this metadata after an entry change only sometimes; the scenario that adds and removes entries is skipped on the reference (the metadata of a freshly created playlist is checked on both).
- **playlists.item.R3** The genres and studios a playlist carries make it a carrier of those names: `GET /Genres?IncludeItemTypes=Playlist` lists them, and search finds the playlist by name for users who may read it.
- **playlists.item.R4** `UserData` of a `Video` playlist has `UnplayedItemCount` (distinct entries not played), `PlayedPercentage` (share of distinct entries played) and `Played` true when all are.

## Index of rules to scenarios

| Rule | Scenario |
|---|---|
| `playlists.auth.R1` | `playlists.auth.anonymous` |
| `playlists.create.R1` | `playlists.create.basic` |
| `playlists.create.R2` | `playlists.create.variants` |
| `playlists.create.R3` | `playlists.create.variants` |
| `playlists.create.R4` | `playlists.create.variants` |
| `playlists.create.R5` | `playlists.create.expand` |
| `playlists.create.R6` | `playlists.create.errors` |
| `playlists.create.R7` | `playlists.create.for-user` |
| `playlists.read.R1` | `playlists.create.basic` |
| `playlists.entries.R1` | `playlists.entries.mutate` |
| `playlists.entries.R2` | `playlists.entries.mutate` |
| `playlists.entries.R3` | `playlists.entries.mutate` |
| `playlists.entries.R4` | `playlists.entries.mutate` |
| `playlists.entries.R5` | `playlists.entries.paging` |
| `playlists.entries.R6` | `playlists.entries.visibility` |
| `playlists.update.R1` | `playlists.update` |
| `playlists.update.R1` | `playlists.update.rename` |
| `playlists.update.R1` | `playlists.update.entries` |
| `playlists.update.R2` | `playlists.update` |
| `playlists.acl.R1` | `playlists.acl.matrix` |
| `playlists.acl.R2` | `playlists.acl.matrix` |
| `playlists.acl.R3` | `playlists.acl.matrix` |
| `playlists.users.R1` | `playlists.users.read` |
| `playlists.users.R2` | `playlists.users.read` |
| `playlists.users.R2` | `playlists.users.owner-asks` |
| `playlists.users.R3` | `playlists.users.manage` |
| `playlists.users.R3` | `playlists.users.grant` |
| `playlists.users.R4` | `playlists.users.unshare` |
| `playlists.view.R1` | `playlists.view` |
| `playlists.view.R2` | `playlists.view` |
| `playlists.view.R3` | `playlists.view` |
| `playlists.delete.R1` | `playlists.delete` |
| `playlists.delete.R2` | `playlists.delete` |
| `playlists.item.R1` | `playlists.create.basic` |
| `playlists.item.R2` | `playlists.item.metadata` |
| `playlists.item.R2` | `playlists.item.metadata-refresh` |
| `playlists.item.R3` | `playlists.item.metadata` |
| `playlists.item.R3` | `playlists.item.metadata-refresh` |
| `playlists.item.R4` | `playlists.item.userdata` |
