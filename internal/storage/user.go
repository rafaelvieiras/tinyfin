package storage

import "time"

// User is the storage-side counterpart of the reference server's UserDto,
// plus the credential and authorization fields UserDto never exposes on
// the wire (it's assembled server-side; the API layer strips PasswordHash
// before building a UserDto response). IsAdministrator/IsDisabled come
// from UserPolicy — the reference server splits User/Configuration/Policy
// three ways, but nothing in this project's scope needs that split at the
// storage layer, so they're flattened onto User directly. Docs/05:
// "Password verification ... is API-layer auth policy, not a storage
// concern — this interface only stores and returns the hash."
type User struct {
	ID   UserID
	Name string

	// PasswordHash is opaque to this package: whatever the API layer's
	// hashing scheme produces (algorithm, salt, cost — all encoded into
	// the string itself, e.g. a PHC-formatted hash). Empty for a user
	// with no password set (UserDto.HasConfiguredPassword == false).
	PasswordHash string

	IsAdministrator bool
	IsDisabled      bool
	EnableAutoLogin bool

	// Policy is the account's permission/restriction settings as an
	// opaque JSON document (the API layer owns its schema, like
	// PasswordHash's format). Empty means "never set"; IsAdministrator
	// and IsDisabled stay separate columns because authentication reads
	// them on every request.
	Policy string

	LastLoginDate    *time.Time
	LastActivityDate *time.Time
}

// AuthToken is the storage-side record behind an issued access token
// (04's authentication section; the reference server's AccessToken
// concept, returned as AuthenticationResult.AccessToken on login). Token
// is the opaque bearer value a client sends back in every subsequent
// request's MediaBrowser Token=... — it is the lookup key for
// LookupToken/RevokeToken, not IssuedAt or the IDs.
type AuthToken struct {
	Token    string
	UserID   UserID
	DeviceID DeviceID
	IssuedAt time.Time
}

// DisplayPreferences is the storage-side counterpart of the reference
// server's DisplayPreferencesDto, minus Id and Client — those are the
// (userID, client) key GetDisplayPreferences/SetDisplayPreferences already
// take as parameters, not stored fields.
type DisplayPreferences struct {
	ViewType string
	IndexBy  string

	SortBy    string
	SortOrder string // "Ascending" or "Descending", matching the reference server's SortOrder enum

	RememberIndexing bool
	RememberSorting  bool

	ScrollDirection string // "Horizontal" or "Vertical"
	ShowBackdrop    bool
	ShowSidebar     bool

	PrimaryImageWidth  int
	PrimaryImageHeight int

	// CustomPrefs carries client-defined key/value settings the reference
	// server's DisplayPreferencesDto.CustomPrefs also leaves free-form —
	// Tinyfin doesn't need to understand their meaning to store them.
	CustomPrefs map[string]string
}

// APIKey is a credential that belongs to no user.
type APIKey struct {
	ID          int64
	Token       string
	AppName     string
	DateCreated time.Time
}
