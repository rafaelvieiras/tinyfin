package storage

import "time"

// Session is the durable half of a session: what the API layer's in-process
// registry (internal/api/sessions.go) knows about a device, kept in the
// store so that a restart does not empty GET /Sessions.
//
// A restart is the one event that would otherwise erase every session at
// once: the registry is a map in memory, and the clients that fill it
// (POST /Sessions/Capabilities/Full, the playback-report routes) only report
// again when they happen to talk to the server next. Persisting the map means
// the dashboard still lists the devices, pushes still have a session to fan
// out to, and a client that reconnects finds its session instead of a new
// one.
//
// What is deliberately NOT here: the live playback state (a session's
// NowPlayingItem and PlayState advance from the last report, so restoring
// them after a restart would show an item as playing that no client is
// playing) and the transcode bookkeeping (the job itself dies with the
// process). See docs/06-session-and-realtime-state.md.
type Session struct {
	// DeviceID is the registry's key: one session per device, as the
	// registry has always been keyed (docs/06).
	DeviceID DeviceID
	UserID   UserID
	// SessionID is the session's own id — the DTO's top-level Id, distinct
	// from DeviceID — minted once when the device is first seen and kept
	// stable across restarts, so a client holding it is not orphaned.
	SessionID string

	UserName       string
	Client         string
	DeviceName     string
	AppVersion     string
	RemoteEndPoint string

	// Capabilities is the client's last POST /Sessions/Capabilities/Full
	// body, kept as the JSON it arrived as and re-served verbatim on
	// GET /Sessions.
	Capabilities string

	LastActivityDate time.Time
}
