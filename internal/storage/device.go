package storage

import "time"

// Device is the storage-side counterpart of the reference server's
// DeviceInfoDto, minus the fields that belong to a live session rather
// than a durable device record (Capabilities/IconUrl — the device profile
// used for playback negotiation, 07, is session-scoped, not stored here;
// see docs/05's DeviceStore comment). AccessToken is also omitted: a
// device can have more than one token over its lifetime (re-login,
// multiple concurrent tokens), so that relationship lives in AuthToken
// (DeviceID field), not duplicated here.
type Device struct {
	ID   DeviceID
	Name string
	// CustomName is the per-DeviceId name set through the device options;
	// it is read from there, never written by UpsertDevice.
	CustomName *string
	AppName    string
	AppVersion string

	// LastUserID/LastUserName mirror DeviceInfoDto's fields of the same
	// name — QueryDevices(ctx, userID) filters on LastUserID, since a
	// device record isn't itself scoped to one user (a shared TV app can
	// be used by more than one account over time; this is "who used it
	// most recently", not an ownership relation).
	LastUserID   UserID
	LastUserName string

	DateLastActivity time.Time
}
