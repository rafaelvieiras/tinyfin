package storage

import "time"

// ActivityType is the reference server's ActivityLogEntry.Type discriminator
// (04), narrowed to the values this project actually generates — see
// ActivityLogStore's doc comment (store.go) for why the scope stops there.
// Kept as a plain string type, same convention as ItemType/SortBy (item.go):
// a value this project doesn't have a constant for yet doesn't need an
// interface change to store.
type ActivityType string

const (
	// ActivityTypeSessionStarted is a successful login — internal/api's
	// handleAuthenticateByName appends one right before issuing a token.
	ActivityTypeSessionStarted ActivityType = "SessionStarted"

	// ActivityTypeAuthenticationSucceeded is written right after
	// SessionStarted on every successful login (users.auth.R23).
	ActivityTypeAuthenticationSucceeded ActivityType = "AuthenticationSucceeded"

	// ActivityTypeAuthenticationFailed is a rejected login attempt —
	// wrong/unknown username or wrong password. The reference server's own
	// value for this case is inferred by analogy (its dashboard "Alerts"
	// widget shows an entry worded "Failed login attempt from <user>" with
	// no confirmed Type string byte-for-byte), not independently confirmed
	// against a live response the way SessionStarted was.
	ActivityTypeAuthenticationFailed ActivityType = "AuthenticationFailed"

	// Account and session entries (users.auth.R28-R32, users.create.R10,
	// users.password.R15, users.delete.R8).
	ActivityTypeUserLockedOut       ActivityType = "UserLockedOut"
	ActivityTypeSessionEnded        ActivityType = "SessionEnded"
	ActivityTypeUserCreated         ActivityType = "UserCreated"
	ActivityTypeUserPasswordChanged ActivityType = "UserPasswordChanged"
	ActivityTypeUserDeleted         ActivityType = "UserDeleted"

	// Playback entries: a start (video / audio / any other media type) and
	// the matching stop. There is no stop entry for other media types.
	ActivityTypeVideoPlayback        ActivityType = "VideoPlayback"
	ActivityTypeAudioPlayback        ActivityType = "AudioPlayback"
	ActivityTypePlayback             ActivityType = "Playback"
	ActivityTypeVideoPlaybackStopped ActivityType = "VideoPlaybackStopped"
	ActivityTypeAudioPlaybackStopped ActivityType = "AudioPlaybackStopped"
)

// ActivitySeverity is the reference server's ActivityLogEntry.Severity
// (04). Only two values are ever produced today, matching the two
// ActivityType values above one-to-one; kept as a plain string type for the
// same reason ActivityType is.
type ActivitySeverity string

const (
	// ActivitySeverityInfo is a successful login's severity — confirmed
	// live (the reference server's own SessionStarted example carries
	// "Severity":"Information").
	ActivitySeverityInfo ActivitySeverity = "Information"

	// ActivitySeverityError is a failed login's severity — inferred, not
	// independently confirmed byte-for-byte: the reference server's
	// dashboard visually red-flags failed-login entries under its
	// "Alerts" column rather than the plain "Activity" column a
	// SessionStarted entry appears under, which is consistent with
	// "Error" but wasn't checked against a raw JSON response the way
	// ActivitySeverityInfo was.
	ActivitySeverityError ActivitySeverity = "Error"
)

// ActivityEntry is one row of the durable activity log ActivityLogStore
// covers — deliberately scoped to the two events this project actually
// generates: a successful login (SessionStarted) and a failed login
// attempt (AuthenticationFailed). See ActivityLogStore's doc comment
// (store.go) for why nothing broader (scan progress, item-added,
// user-management, ...) belongs here.
//
// ID is a plain auto-incrementing int64, not the opaque UUID-shaped
// ItemID/UserID/... string type this package uses everywhere else: the
// reference server's own ActivityLogEntry.Id is a plain integer (confirmed
// live), and jellyfin-web's dashboard widgets page through it as one, so
// there's no wire-compatibility reason to hide it behind this project's
// usual ID scheme.
type ActivityEntry struct {
	ID int64

	Name          string
	ShortOverview string
	Type          ActivityType
	Date          time.Time

	// UserID is nil for a failed login attempt: no user was ever
	// authenticated for that request, only a (possibly nonexistent)
	// attempted username — which Name mentions instead. Set for a
	// successful login. This is also the field ActivityEntryFilter.HasUserID
	// filters on.
	UserID *UserID

	// ItemID is the item a playback entry is about; nil for entries that
	// are not about an item (logins).
	ItemID *ItemID

	Severity ActivitySeverity
}

// ActivityEntryFilter drives ActivityLogStore.QueryActivityEntries.
//
// MinDate/HasUserID are optional: nil means "don't filter on this". Limit
// is required — same no-full-scan enforcement ItemFilter uses (item.go) —
// an implementation must reject Limit <= 0 rather than defaulting to
// "return everything"; jellyfin-web's dashboard widgets that are this
// store's only caller today always send one.
type ActivityEntryFilter struct {
	MinDate   *time.Time
	HasUserID *bool
	Limit     int
	Offset    int
}
