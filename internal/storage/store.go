// Package storage defines the storage interface described in
// docs/05-storage-drivers.md. Store is the single interface the API/metadata
// role depends on; docs/05 is the source of truth for its shape — this file
// mirrors it, it does not extend it. Domain struct field shapes (Item, User,
// Device, ...) were left as "fields TBD against 04" in that document and are
// filled in here, derived from docs/04-api-compatibility.md and the public
// Jellyfin OpenAPI schema (see item.go, user.go, device.go, ... for the
// per-struct derivation notes).
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
	ActivityLogStore
	APIKeyStore
	SettingStore

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
	// first (a stable SeriesID tie-break, same convention QueryItems'
	// DateCreated sort uses at the API layer) — the per-series counterpart
	// of QueryItems' per-item ranking, letting a caller page through "most
	// recently active series" without a raw-item pool masking a
	// low-episode-count series behind a high-episode-count one (see
	// SeriesActivity's doc comment). Limit is required, same
	// no-full-catalog enforcement as QueryItems.
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

	// IssueToken mints a token for (userID, deviceID), revoking any earlier
	// token of that same pair (one live token per user and device).
	// GetUserByName matches the name case-insensitively.
	IssueToken(ctx context.Context, userID UserID, deviceID DeviceID) (AuthToken, error)
	// IssueSessionToken is IssueToken that also records the device name and
	// remote address of the session the token stands for.
	IssueSessionToken(ctx context.Context, userID UserID, deviceID DeviceID, deviceName, remoteAddress string) (AuthToken, error)
	// QueryUserTokens lists the user's live tokens (one per device).
	QueryUserTokens(ctx context.Context, userID UserID) ([]AuthToken, error)
	LookupToken(ctx context.Context, token string) (AuthToken, error)
	RevokeToken(ctx context.Context, token string) error
	// RevokeUserTokens removes the user's tokens issued to any device not
	// listed in keepDevices; a nil/empty list removes all of them.
	RevokeUserTokens(ctx context.Context, userID UserID, keepDevices []DeviceID) error

	// GetDisplayPreferences/SetDisplayPreferences cover the per-user,
	// per-client UI settings (sort order, view style, ...) the reference
	// server's DisplayPreferences endpoint exposes (04) — durable, so it
	// belongs here rather than in session state (06).
	GetDisplayPreferences(ctx context.Context, userID UserID, client string) (DisplayPreferences, error)
	SetDisplayPreferences(ctx context.Context, userID UserID, client string, prefs DisplayPreferences) error

	// GetUserConfiguration/SetUserConfiguration cover the per-account
	// preferences document (subtitle mode, language preferences, home
	// screen ordering, ...). The document is opaque to storage, like
	// User.Policy; an account that never saved one reads back "". The
	// document is removed together with the account.
	GetUserConfiguration(ctx context.Context, userID UserID) (string, error)
	SetUserConfiguration(ctx context.Context, userID UserID, configuration string) error
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
	// FolderLeafCounts reports how many leaf items (non-folder descendants)
	// the folder has and how many of them are unplayed for the user, where a
	// leaf's played state is its own unless an ancestor folder was marked
	// more recently than the leaf was last changed.
	FolderLeafCounts(ctx context.Context, userID UserID, folderID ItemID) (total, unplayed int, err error)
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

// ActivityLogStore covers the durable activity log GET
// /System/ActivityLog/Entries serves (04's admin dashboard group) —
// deliberately scoped to the two events this project actually generates: a
// successful login (SessionStarted) and a failed login attempt
// (AuthenticationFailed), the same narrow-but-complete scoping precedent
// internal/api/localization.go's TMDB-only fetcher list documents (no
// scan-progress, item-added, or user-management events here — see
// internal/api/auth_by_name.go, the one place both events actually
// happen, and internal/api/activity_log.go for the read side).
type ActivityLogStore interface {
	// AppendActivityEntry mints the entry's own auto-increment ID — a
	// caller never supplies one, same "store is the ID authority" rule
	// UserStore.CreateUser follows — and returns it. Appending a log entry
	// is intentionally never allowed to fail the request that triggered it
	// (internal/api's callers log-and-continue on error): this method
	// itself still reports a real error rather than swallowing one, the
	// caller decides what to do with it.
	AppendActivityEntry(ctx context.Context, entry ActivityEntry) (int64, error)

	// QueryActivityEntries pages newest-first (Date descending) — the only
	// ordering that produces a sane "recent activity" widget, and what
	// jellyfin-web's dashboard overview page actually expects. MinDate/
	// HasUserID are optional filters (ActivityEntryFilter: nil means
	// "don't filter on this"); Limit is required, same no-full-scan
	// enforcement ItemFilter uses. The second return value is the
	// pre-pagination match count (TotalRecordCount on the wire), same
	// convention as ItemPage.TotalCount.
	QueryActivityEntries(ctx context.Context, filter ActivityEntryFilter) ([]ActivityEntry, int, error)
}

// APIKeyStore covers API keys: opaque credentials that belong to no user.
type APIKeyStore interface {
	// CreateAPIKey mints a new key for appName.
	CreateAPIKey(ctx context.Context, appName string) (APIKey, error)
	// QueryAPIKeys lists every key, oldest first.
	QueryAPIKeys(ctx context.Context) ([]APIKey, error)
	// LookupAPIKey resolves a key's token; an unknown token is an error.
	LookupAPIKey(ctx context.Context, token string) (APIKey, error)
	// RevokeAPIKey removes a key; revoking an unknown token is not an error.
	RevokeAPIKey(ctx context.Context, token string) error
}

// SettingStore persists small server-wide runtime settings as strings.
type SettingStore interface {
	// GetSetting returns the stored value; ok is false when never set.
	GetSetting(ctx context.Context, key string) (value string, ok bool, err error)
	SetSetting(ctx context.Context, key, value string) error
}
