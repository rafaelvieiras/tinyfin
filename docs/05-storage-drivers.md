# 05 — Storage drivers

## The interface

One storage interface, two implementations. Every read and write the API/metadata role performs
goes through this interface — nothing in the rest of the codebase is aware of which
implementation is active.

- **Embedded mode** (default): a single SQLite database file, no separate process.
- **Scale mode** (opt-in): a networked implementation an operator turns on when running more than
  one API/metadata node.

## The Go interface (O3)

`Store` is the one interface the API/metadata role holds. Callers never see which implementation
is behind it, or which physical connection (the embedded file, or — in scale mode — the write
leader vs. a read replica) served a given call; that routing is entirely internal to the
implementation.

It is organized as a set of narrow, per-concern interfaces embedded into one `Store` type, rather
than one flat interface — each sub-interface maps directly to one of the API-surface groups in
`04` that Phase 1 needs (library/item navigation, users/devices, durable playstate, images,
subtitles), so a change to one API group touches one sub-interface, not a monolith. `Store` is
still "one interface" from the caller's point of view: one value, passed down from `main`, that
satisfies all of them at once.

```go
package storage

import "context"

// Store is the single interface the API/metadata role depends on (docs/03).
// Every read and write it performs goes through this interface — nothing
// elsewhere in the codebase holds a SQL/driver-specific type.
type Store interface {
	ItemStore
	UserStore
	DeviceStore
	PlaystateStore
	ImageStore
	TrickplayStore
	SubtitleStore
	LyricStore
	LibraryStore

	// Tx runs fn against a Store bound to a single transaction: everything
	// fn does through it commits or rolls back together. In scale mode a
	// transaction always executes against the write leader — Tx returning
	// nil guarantees the write is durable on the leader, not that it has
	// already reached every read replica (replica lag, see below).
	Tx(ctx context.Context, fn func(Store) error) error

	// Healthy reports whether the store can currently serve reads (and,
	// for a write leader, writes). A read replica that has fallen behind
	// the leader past an implementation-defined bound reports unhealthy
	// rather than silently serving stale data — this is the interface's
	// only caller-visible acknowledgment that replicas can lag; ordinary
	// reads never expose a staleness parameter.
	Healthy(ctx context.Context) error

	// Close releases the underlying connection(s). Safe to call once, at
	// process shutdown.
	Close() error
}

// ItemStore covers the generic item model — movies, series, seasons,
// episodes, music, and the specialized types layered over it (04) — plus
// the search that backs it (FTS5, 09).
//
// There is deliberately no ListAll/GetCatalog method. QueryItems is the
// only read path, and ItemFilter always carries a Limit an implementation
// must reject if <= 0 — so no caller can accidentally materialize a full
// catalog in memory. This is the interface-level enforcement of the
// no-full-catalog-cache principle in AGENTS.md/09, not just a convention.
type ItemStore interface {
	GetItem(ctx context.Context, id ItemID) (Item, error)
	QueryItems(ctx context.Context, filter ItemFilter) (ItemPage, error)
	UpsertItem(ctx context.Context, item Item) error
	DeleteItem(ctx context.Context, id ItemID) error

	// QuerySeriesLatestActivity ranks distinct series by their own most
	// recent DateCreated among rows matching libraryID/itemTypes, newest
	// first — the per-series counterpart of QueryItems' per-item ranking,
	// letting a caller page through "most recently active series" without a
	// raw-item pool masking a low-episode-count series behind a
	// high-episode-count one. Added for `GET /Shows/NextUp`'s "last-played
	// episode per series" query; Limit is required, same no-full-catalog
	// enforcement as QueryItems.
	QuerySeriesLatestActivity(ctx context.Context, libraryID LibraryID, itemTypes []ItemType, limit int) ([]SeriesActivity, error)

	// QueryRelations/UpsertRelation/DeleteRelation cover N:M and ordered
	// item links that a single ParentID can't express: collection
	// membership, playlist membership (with its own per-playlist order),
	// artist/album credits. Series→season→episode stays a plain ParentID
	// on Item (each episode has exactly one parent) — relations are only
	// for links an item can have more than one of. One generic relation
	// table (kind + explicit order) instead of one join table per relation
	// kind, following the pattern the personal cataloging tool this
	// project's provider interface was prototyped against (my-media-db's
	// entity_relation) converged on independently.
	//
	// DeleteRelation takes the relation's own ID, not a (parent, child,
	// kind) triple — the reference server's PlaylistItemId confirms the
	// same item can appear more than once under the same parent (a track
	// added twice to one playlist), so parent+child+kind alone doesn't
	// identify a single entry to remove.
	QueryRelations(ctx context.Context, itemID ItemID, kind string, asParent bool) ([]ItemRelation, error)
	UpsertRelation(ctx context.Context, rel ItemRelation) error
	DeleteRelation(ctx context.Context, id ItemRelationID) error
}

// UserStore covers accounts and auth tokens (the "Users, sessions,
// devices" group in 04 — sessions themselves are session/realtime state,
// 06, not here). QueryUsers is intentionally unbounded: unlike items,
// user count is operator-scale, not catalog-scale, so it carries no
// paging requirement. Password verification (comparing a submitted
// password against the stored hash) is API-layer auth policy, not a
// storage concern — this interface only stores and returns the hash.
type UserStore interface {
	GetUser(ctx context.Context, id UserID) (User, error)
	GetUserByName(ctx context.Context, name string) (User, error)
	QueryUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, user User) (UserID, error)
	UpdateUser(ctx context.Context, user User) error
	DeleteUser(ctx context.Context, id UserID) error

	IssueToken(ctx context.Context, userID UserID, deviceID DeviceID) (AuthToken, error)
	LookupToken(ctx context.Context, token string) (AuthToken, error)
	RevokeToken(ctx context.Context, token string) error

	// GetDisplayPreferences/SetDisplayPreferences cover the per-user,
	// per-client UI settings (sort order, view style, ...) the reference
	// server's DisplayPreferences endpoint exposes (04) — durable, so it
	// belongs here rather than in session state (06).
	GetDisplayPreferences(ctx context.Context, userID UserID, client string) (DisplayPreferences, error)
	SetDisplayPreferences(ctx context.Context, userID UserID, client string, prefs DisplayPreferences) error
}

// DeviceStore covers registered devices (the DeviceId a client sends on
// every authenticated request, 04) — durable device records, not the live
// session tied to one.
type DeviceStore interface {
	GetDevice(ctx context.Context, id DeviceID) (Device, error)
	QueryDevices(ctx context.Context, userID UserID) ([]Device, error)
	UpsertDevice(ctx context.Context, device Device) error
	DeleteDevice(ctx context.Context, id DeviceID) error
}

// PlaystateStore covers durable resume position and watched status —
// explicitly storage, not session/realtime state, per the lifetime
// distinction in 06 (it must survive a restart).
type PlaystateStore interface {
	GetPlaystate(ctx context.Context, userID UserID, itemID ItemID) (Playstate, error)
	SetPlaystate(ctx context.Context, userID UserID, itemID ItemID, state Playstate) error
}

// ImageStore covers references to item/branding images (04). Image bytes
// live on disk/object storage like media files do, not as blobs in
// SQLite; this store holds only the refs and cache metadata pointing at
// them.
//
// A (itemID, kind) pair can have more than one image (e.g. multiple
// backdrops), so this returns/addresses a list, not a single ref. Each
// entry is addressed by its own ID rather than a positional index — a
// positional index breaks under concurrent insert/delete/reorder (two
// writers each independently computing "index 2" can collide), while a
// stable ID makes Upsert/Delete order-independent.
type ImageStore interface {
	GetImageRefs(ctx context.Context, itemID ItemID, kind ImageKind) ([]ImageRef, error)
	UpsertImageRef(ctx context.Context, ref ImageRef) error
	DeleteImageRef(ctx context.Context, id ImageRefID) error
}

// TrickplayStore covers per-resolution trickplay (seek thumbnail
// sprite-sheet) metadata. Kept separate from ImageStore and off Item:
// trickplay generation is optional and happens after the fact (07 — the
// scanner doesn't generate it), and one item can end up with more than one
// resolution's worth over time. Folding it onto Item would mean every
// plain GetItem call carries generation-time data most callers don't
// need, working against the memory-conscious read shape in 09 — a
// dedicated store keeps it opt-in to load.
type TrickplayStore interface {
	QueryTrickplay(ctx context.Context, itemID ItemID) ([]TrickplayInfo, error)
	UpsertTrickplayInfo(ctx context.Context, itemID ItemID, info TrickplayInfo) error
	DeleteTrickplayInfo(ctx context.Context, itemID ItemID, width int) error
}

// SubtitleStore covers externally-added subtitle files only — uploaded or
// pulled from a provider (04's UploadSubtitleDto/RemoteSubtitleInfo) — same
// refs-not-blobs shape as ImageStore. Embedded subtitle tracks are not
// here: the reference server reports those as MediaStream entries
// (Type=Subtitle) alongside audio/video tracks, i.e. scan-derived metadata
// that belongs on Item itself, not a separate store.
type SubtitleStore interface {
	QuerySubtitles(ctx context.Context, itemID ItemID) ([]SubtitleRef, error)
	UpsertSubtitle(ctx context.Context, ref SubtitleRef) error
	DeleteSubtitle(ctx context.Context, id SubtitleID) error
}

// LyricStore covers lyric file references (04) — same refs-not-blobs
// shape as SubtitleStore, but kept separate rather than folded into it:
// the reference server's lyric shape (LyricDto: timed LyricLines) is
// structurally different from a subtitle track, not a variant of one.
type LyricStore interface {
	QueryLyrics(ctx context.Context, itemID ItemID) ([]LyricRef, error)
	UpsertLyric(ctx context.Context, ref LyricRef) error
	DeleteLyric(ctx context.Context, id LyricID) error
}

// LibraryStore covers library/folder definitions and scan bookkeeping —
// what the scanner (08) needs to know which libraries exist and what it
// last saw in each.
type LibraryStore interface {
	GetLibrary(ctx context.Context, id LibraryID) (Library, error)
	QueryLibraries(ctx context.Context) ([]Library, error)
	UpsertLibrary(ctx context.Context, lib Library) error
	DeleteLibrary(ctx context.Context, id LibraryID) error
}

// Identifier and query/result types referenced above. Field-level shape
// for the domain structs below (Item, User, Device, Library, Playstate,
// and the rest marked "fields TBD") is intentionally not finalized in this
// decision — those follow from the wire shapes in 04 as each API group
// gets implemented, rather than being predicted or duplicated here. What
// O3 fixes is: the identifier types, the shape of a bounded/paginated item
// query, and which concerns get their own store.
//
// A person (04's "Persons" group — cast/crew) is not a separate store: the
// reference server itself models a person as the same BaseItem family as
// everything else, so it's ItemType = "Person" inside ItemStore, with
// credit info (role, sort order) riding on ItemRelation(kind="credit").
type (
	ItemID         string
	UserID         string
	DeviceID       string
	LibraryID      string
	SubtitleID     string
	LyricID        string
	ItemRelationID string
	ImageRefID     string
	ImageKind      string // e.g. "primary", "backdrop", "logo" — see 04

	Item      struct{ /* fields TBD against 04 at implementation time */ }
	ItemType  string
	SortField struct{ /* field + direction */ }

	// ItemRelation is a typed, ordered link between two items. ID is the
	// relation row's own identity — required because the same (parent,
	// child, kind) can legitimately repeat, e.g. the reference server's
	// PlaylistItemId shows one item can appear more than once in the same
	// playlist. Kind names are implementation-defined strings, not a
	// closed Go enum, so a new relation kind doesn't require an interface
	// change — e.g. "parent" mirrors what Item.ParentID already gives for
	// the tree case, "collection", "playlist", "credit".
	ItemRelation struct {
		ID        ItemRelationID
		ParentID  ItemID
		ChildID   ItemID
		Kind      string
		SortIndex int
	}

	User               struct{ /* fields TBD against 04 */ }
	AuthToken          struct{ /* fields TBD against 04 */ }
	Device             struct{ /* fields TBD against 04 */ }
	DisplayPreferences struct{ /* fields TBD against 04 */ }
	LyricRef           struct{ /* fields TBD against 04 */ }
	Library            struct{ /* fields TBD against 04 */ }
	Playstate          struct{ /* fields TBD against 04 */ }
	SubtitleRef        struct{ /* fields TBD against 04 */ }

	// ImageRef is one entry in the list ImageStore.GetImageRefs returns
	// for a given (itemID, kind). ID is its storage identity (see
	// ImageStore above for why this is ID-addressed, not positional).
	// Remaining fields TBD against 04.
	ImageRef struct {
		ID ImageRefID
	}

	// TrickplayInfo is one resolution's worth of seek-thumbnail
	// sprite-sheet metadata for an item — grid layout and interval, not
	// image bytes. Shape mirrors the reference server's public
	// TrickplayInfoDto: Width, Height, TileWidth, TileHeight,
	// ThumbnailCount, Interval, Bandwidth. Fields TBD against 04.
	TrickplayInfo struct{ /* fields TBD against 04, shape as above */ }
)

// ItemFilter drives every catalog read. Limit is required — implementations
// reject Limit <= 0 rather than defaulting to "return everything".
//
// UserID/Played/Favorite are here (not left to a separate playstate query)
// because playstate-aware browsing — Continue Watching, Next Up, Favorites
// — is core "library and item navigation" (04's highest-priority group),
// not an admin add-on. QueryItems is free to join PlaystateStore
// internally to answer them; that join never crosses the Store boundary
// back out to the caller.
type ItemFilter struct {
	LibraryID  *LibraryID
	ParentID   *ItemID
	ItemTypes  []ItemType
	Genres     []string
	SearchText string // backs FTS5 search, 09
	Recursive  bool
	UserID     *UserID
	Played     *bool
	Favorite   *bool
	SortBy     []SortField
	Limit      int
	Offset     int
}

// ItemPage is the result of a bounded QueryItems call. TotalCount is the
// pre-pagination match count (what list responses in 04 report as
// TotalRecordCount), not the size of Items.
type ItemPage struct {
	Items      []Item
	TotalCount int
	Offset     int
}
```

Both implementations (embedded SQLite, scale mode) satisfy the same `Store`; scale mode's `Tx`
targets the write leader, and non-transactional reads may be served by any replica the
implementation judges current enough per `Healthy`.

**Implementation note for `Recursive` queries:** a naive walk over `ParentID` at query time for
`ItemFilter.Recursive` risks becoming an unindexed scan as a library grows — directly against the
no-full-scan principle in `AGENTS.md`/`09`. An embedded implementation should consider maintaining
a materialized ancestor-closure table (each item's full set of ancestor IDs, kept in sync at write
time) so "everything under this folder" is a single indexed lookup instead of a recursive walk.
Not part of the `Store` signature itself — an internal technique for whoever implements
`QueryItems`.

### Validated against my-media-db and the reference server's published OpenAPI schema

Checked against `my-media-db` (the personal cataloging tool the provider interface, `08`, was
prototyped against) and against `https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json`
(the published schema `04` already names as a secondary compatibility-verification source).
`UserItemDataDto.Played`/`IsFavorite`, `DisplayPreferencesDto.Client`, `BaseItemDto.PlaylistItemId`,
and `TrickplayInfoDto` confirmed design choices already reflected above (`ItemFilter`'s playstate
fields, `GetDisplayPreferences`'s `client` param, `ItemRelation.ID`, `TrickplayInfo`'s shape).
Lyrics, previously left open here, got their own `LyricStore`: `LyricDto`'s timed lines are
structurally distinct from a subtitle track.

Two `04` API groups still don't have an obvious home and are left open rather than guessed at:

- **Per-user library access** (parental-control-style restriction to specific libraries) — open
  in `my-media-db` too (`docs/10-usuarios-e-permissoes.md`), not just here. Low priority: v1
  targets single-node Direct Play first (`10-roadmap.md`).
- **Facet listing** (distinct genres/years/studios in a library, to back `FilterController`) — a
  convenience read, likely buildable on top of `QueryItems` by an implementation without a
  dedicated method; not urgent.

Neither blocks Phase 1 (`04`'s highest-priority groups — item navigation, playback,
users/sessions/devices — are covered above). Revisit as a new decision-log item if one becomes
load-bearing before its area of `04` is implemented.

## Why embedded mode is SQLite, not something else

This is the default and the target for the primary use case in `01`: single machine, near-zero
idle memory, zero operational overhead. SQLite embedded, WAL mode, with well-designed indexes
(FTS5 for search, generated columns where useful) serves this directly. No separate database
process to run, monitor, or account for in a memory budget.

## Why scale mode isn't "just switch to Postgres"

Jellyfin itself is limited by exactly this: the entire state (library, users, sessions,
playstate) lives in one SQLite file, and SQLite is single-writer — two Jellyfin processes cannot
write to the same state at once, which is why the reference implementation has no real multi-node
story today. Postgres support has been on Jellyfin's own roadmap for a 12.0+ release without a
committed date.

PocketBase, a comparable single-binary/embedded-SQLite Go project, made the same observation and
went further: its maintainer has stated that switching PocketBase's storage to Postgres *still
wouldn't* make it horizontally scalable, because there is no synchronization mechanism for
in-process application state (hooks, realtime subscriptions) independent of the database engine.
PocketBase's answer to this is to not pursue horizontal scaling at all — a legitimate choice for
its use case, and evidence the problem is genuinely hard, not evidence it's unsolvable.

Tinyfin's answer is to unbundle the two things PocketBase's finding shows are actually separate:
the **storage engine** (this document) and **session/realtime state** (`06`). Fixing storage alone
would repeat PocketBase's finding almost exactly; fixing both is the actual requirement.

## Why scale mode is a write-leader with streaming read replicas, not a Raft cluster, by default

A media server's workload is read-heavy by nature: browsing the library, fetching images,
requesting `PlaybackInfo`, checking playstate. Writes are comparatively rare and small: playstate
updates, scan results, admin changes. That shape doesn't call for a fully symmetric multi-writer
database — it calls for scaling reads.

The scale-mode default follows the model Litestream uses for live read replication: one write
leader receives all writes; one or more read replicas stay in sync by streaming the SQLite
write-ahead log and serve reads locally, at SQLite-embedded latency, from whichever node a client
happens to hit. This keeps the "SQLite in spirit" property even in scale mode — same schema, same
query patterns, no new SQL dialect to support — while removing the single-node ceiling for the
read traffic that dominates the workload. Writes still funnel through one leader, which is not a
practical bottleneck for the write volume this workload actually produces.

`rqlite` (a Go-native, Raft-based distributed SQL layer over SQLite, with local reads on any node
and writes replicated via consensus) is the fallback if a genuinely write-heavy, symmetric
multi-writer scenario ever shows up — heavier operationally, and not needed for the target use
case as currently understood. Kept explicitly out rather than built preemptively; see open item
**O5**.

`dqlite` (Canonical's C library, also Raft-based) was considered and set aside: it's a library to
integrate into your own software rather than a runnable service, which is a heavier lift than
either of the two options above for no clear benefit here.

## Operational shape

- **Embedded mode**: `tinyfin` with a data directory. One process, one file. No configuration
  beyond a path.
- **Scale mode**: one node configured as the write leader; additional nodes configured to stream
  from it and serve reads locally. Turning this on is a configuration change on top of the same
  binary — not a data migration to a different database product, and not a code path that only
  exists in a "scale build."

Open item **O4** (`00-decision-log.md`) — whether the replication mechanism is built directly, or
Tinyfin depends on Litestream itself as a library/sidecar rather than reimplementing WAL
streaming — is not yet settled.
