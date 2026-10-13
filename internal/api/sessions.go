// sessions.go implements the in-process session registry docs/06 scopes as
// the default "session/realtime state" interface (as opposed to durable
// storage, internal/storage) and GET /Sessions — jellyfin-web's admin
// Dashboard "Devices" widget calls this, passing activeWithinSeconds, to
// show who's currently connected.
//
// This is deliberately a small slice, not a general session-liveness
// system: the registry is populated from exactly two signals this project
// already produces — POST /Sessions/Capabilities/Full (system.go's
// handleSessionsCapabilitiesFull, previously discarded entirely) and the
// three playback-report routes (sessions_playback.go) touching
// LastActivityDate on an already-registered device. No other route updates
// it, and there is no expiry/eviction beyond GET /Sessions's own
// activeWithinSeconds filter — a session for a device that stops calling
// either of those stays listed (IsActive: true) forever until the process
// restarts. That matches this slice's scope: proving the shape is right for
// the one real client that reads it today, not building session-liveness
// tracking wholesale.
package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// sessionCapabilitiesDto covers both directions of the one real capability
// signal this project has: it is decoded directly from a POST
// /Sessions/Capabilities/Full request body (ground truth confirmed live
// against a real Jellyfin server) and re-served verbatim as GET
// /Sessions[].Capabilities — the request and response shapes share exactly
// these four fields, so one type does both jobs rather than maintaining a
// duplicate.
type sessionCapabilitiesDto struct {
	PlayableMediaTypes           []string `json:"PlayableMediaTypes"`
	SupportedCommands            []string `json:"SupportedCommands"`
	SupportsPersistentIdentifier bool     `json:"SupportsPersistentIdentifier"`
	SupportsMediaControl         bool     `json:"SupportsMediaControl"`
}

// sessionPlayStateDto mirrors the OpenAPI schema's PlayerStateInfo, trimmed
// to the fields worth reporting honestly for a session this project never
// observes mid-playback (sessions_playback.go's playback-report routes
// update durable Playstate, not this in-process registry — see
// sessionInfoDto's NowPlayingQueue doc comment below for the gap this
// implies). CanSeek/IsPaused/IsMuted are always their zero value (false)
// rather than fabricated; RepeatMode/PlaybackOrder use the reference
// server's own "nothing playing" defaults, confirmed live.
type sessionPlayStateDto struct {
	CanSeek       bool   `json:"CanSeek"`
	IsPaused      bool   `json:"IsPaused"`
	IsMuted       bool   `json:"IsMuted"`
	RepeatMode    string `json:"RepeatMode"`
	PlaybackOrder string `json:"PlaybackOrder"`
}

// sessionInfoDto mirrors the OpenAPI schema's SessionInfo, trimmed to the
// fields confirmed live against a real Jellyfin server (called by
// jellyfin-web's admin Dashboard "Devices" widget) via GET
// /Sessions?activeWithinSeconds=960.
type sessionInfoDto struct {
	PlayState       sessionPlayStateDto    `json:"PlayState"`
	AdditionalUsers []struct{}             `json:"AdditionalUsers"`
	Capabilities    sessionCapabilitiesDto `json:"Capabilities"`
	RemoteEndPoint  string                 `json:"RemoteEndPoint"`
	// PlayableMediaTypes duplicates Capabilities.PlayableMediaTypes at the
	// top level — confirmed live, not a mistake: the reference server
	// really does report it in both places.
	PlayableMediaTypes []string  `json:"PlayableMediaTypes"`
	ID                 string    `json:"Id"`
	UserID             string    `json:"UserId"`
	UserName           string    `json:"UserName"`
	Client             string    `json:"Client"`
	LastActivityDate   time.Time `json:"LastActivityDate"`
	// LastPlaybackCheckIn is always the zero time.Time value — this
	// registry has no now-playing tracking wired to it at all (see
	// NowPlayingQueue below), so there is never a real check-in to report.
	LastPlaybackCheckIn time.Time `json:"LastPlaybackCheckIn"`
	DeviceName          string    `json:"DeviceName"`
	DeviceID            string    `json:"DeviceId"`
	ApplicationVersion  string    `json:"ApplicationVersion"`
	// IsActive is true unconditionally — no logout/expiry tracking exists
	// beyond GET /Sessions's own activeWithinSeconds filter (this file's
	// package doc comment).
	IsActive bool `json:"IsActive"`
	// SupportsRemoteControl mirrors SupportsMediaControl: the only signal
	// this project has for either concept is the single
	// SupportsMediaControl flag a client reports via
	// Sessions/Capabilities/Full, so it stands in for both rather than
	// fabricating a distinct capability this project can't actually tell
	// apart.
	SupportsMediaControl  bool `json:"SupportsMediaControl"`
	SupportsRemoteControl bool `json:"SupportsRemoteControl"`
	// NowPlayingQueue/NowPlayingQueueFullItems are always empty — there is
	// no now-playing tracking wired to this registry. Playback-progress
	// reporting (sessions_playback.go's handleSessionsPlaying/Progress/
	// Stopped) updates a user's durable Playstate in storage, not "what is
	// this session playing right now"; threading that through this
	// registry is a separate, larger feature, deliberately left as a known
	// gap rather than fabricated here.
	NowPlayingQueue          []struct{} `json:"NowPlayingQueue"`
	NowPlayingQueueFullItems []struct{} `json:"NowPlayingQueueFullItems"`
	// HasCustomDeviceName is always false — no per-device rename feature
	// exists (the reference server lets a user rename a device from its
	// registered name; this project has no such store).
	HasCustomDeviceName bool     `json:"HasCustomDeviceName"`
	ServerID            string   `json:"ServerId"`
	SupportedCommands   []string `json:"SupportedCommands"`
}

// trackedSession is one entry in sessionRegistry, keyed by DeviceID. Every
// field here is sourced from one of the two signals this file's package doc
// comment describes — nothing is inferred or fabricated beyond the zero
// values sessionInfoDto's own doc comments already call out.
type trackedSession struct {
	id               string
	userID           string
	userName         string
	client           string
	deviceName       string
	deviceID         string
	appVersion       string
	remoteEndPoint   string
	capabilities     sessionCapabilitiesDto
	lastActivityDate time.Time
}

// sessionRegistry is the in-process (docs/06 default mode) session
// registry: a mutex-guarded map, held on *Server, with no persistence and
// no cross-node sharing — exactly the "zero additional cost, zero
// additional process" in-process mode docs/06 describes, not the scale-mode
// Redis/Valkey alternative it also documents.
type sessionRegistry struct {
	mu       sync.Mutex
	byDevice map[string]*trackedSession
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{byDevice: make(map[string]*trackedSession)}
}

// upsertCapabilities registers or updates the tracked session for deviceID
// from a POST /Sessions/Capabilities/Full call (system.go's
// handleSessionsCapabilitiesFull), stamping LastActivityDate to now. A
// session's id (the DTO's top-level "Id", distinct from DeviceId) is minted
// once, the first time a given device is seen, and kept stable across
// later updates — matching the reference server's own session identity
// being stable across repeated capability registrations from the same
// device. A blank deviceID is a no-op: without one there is nothing to key
// this entry by, and every other field is meaningless without it too.
func (r *sessionRegistry) upsertCapabilities(deviceID, userID, userName, client, deviceName, appVersion, remoteEndPoint string, caps sessionCapabilitiesDto) {
	if deviceID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ts, ok := r.byDevice[deviceID]
	if !ok {
		ts = &trackedSession{id: randomID()}
		r.byDevice[deviceID] = ts
	}
	ts.userID = userID
	ts.userName = userName
	ts.client = client
	ts.deviceName = deviceName
	ts.deviceID = deviceID
	ts.appVersion = appVersion
	ts.remoteEndPoint = remoteEndPoint
	ts.capabilities = caps
	ts.lastActivityDate = time.Now().UTC()
}

// touch stamps LastActivityDate to now for an already-registered session,
// keyed by deviceID. A no-op if no session has ever registered capabilities
// for that device — this file's package doc comment's deliberately narrow
// scope: sessions_playback.go's playback-report routes touch liveness for
// an existing session, they don't create one on their own.
func (r *sessionRegistry) touch(deviceID string) {
	if deviceID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ts, ok := r.byDevice[deviceID]; ok {
		ts.lastActivityDate = time.Now().UTC()
	}
}

// list renders every tracked session as a sessionInfoDto, filtering to
// those whose LastActivityDate is within the last activeWithinSeconds
// seconds of now when hasFilter is true (GET /Sessions's optional query
// parameter — confirmed live and against the OpenAPI schema as an optional
// filter, not a required one with a magic default, so an absent/unparseable
// value returns every tracked session unfiltered rather than guessing a
// window). Always returns a non-nil, possibly-empty slice so an empty
// registry serializes to "[]", not "null".
func (r *sessionRegistry) list(serverID string, activeWithinSeconds int, hasFilter bool) []sessionInfoDto {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	out := make([]sessionInfoDto, 0, len(r.byDevice))
	for _, ts := range r.byDevice {
		if hasFilter && now.Sub(ts.lastActivityDate) > time.Duration(activeWithinSeconds)*time.Second {
			continue
		}
		out = append(out, sessionInfoDto{
			PlayState:          sessionPlayStateDto{RepeatMode: "RepeatNone", PlaybackOrder: "Default"},
			AdditionalUsers:    []struct{}{},
			Capabilities:       ts.capabilities,
			RemoteEndPoint:     ts.remoteEndPoint,
			PlayableMediaTypes: ts.capabilities.PlayableMediaTypes,
			ID:                 ts.id,
			UserID:             ts.userID,
			UserName:           ts.userName,
			Client:             ts.client,
			LastActivityDate:   ts.lastActivityDate,
			// Zero value — see sessionInfoDto's doc comment.
			LastPlaybackCheckIn:      time.Time{},
			DeviceName:               ts.deviceName,
			DeviceID:                 ts.deviceID,
			ApplicationVersion:       ts.appVersion,
			IsActive:                 true,
			SupportsMediaControl:     ts.capabilities.SupportsMediaControl,
			SupportsRemoteControl:    ts.capabilities.SupportsMediaControl,
			NowPlayingQueue:          []struct{}{},
			NowPlayingQueueFullItems: []struct{}{},
			HasCustomDeviceName:      false,
			ServerID:                 serverID,
			SupportedCommands:        ts.capabilities.SupportedCommands,
		})
	}
	return out
}

// handleSessionsList is GET /Sessions, called by jellyfin-web's admin
// Dashboard "Devices" widget with activeWithinSeconds set. Registered
// admin-only (server.go) — it lists every tracked session across every
// user, IP address included, and that's the only confirmed caller.
// POST /Sessions/Capabilities/Full (the route that populates this registry)
// stays open to every signed-in client regardless of admin status — that
// asymmetry is deliberate: registering your own session's capabilities and
// listing everyone else's are different privilege levels.
func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	activeWithinSeconds, hasFilter := 0, false
	if v := r.URL.Query().Get("activeWithinSeconds"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			activeWithinSeconds, hasFilter = n, true
		}
	}
	writeJSON(w, http.StatusOK, s.sessions.list(s.cfg.ServerID, activeWithinSeconds, hasFilter))
}

// remoteEndPoint reports the request's TCP peer address as a bare IP string
// (RemoteEndPoint's shape, confirmed live: "203.0.113.7", no port) — falling
// back to the raw, unparsed RemoteAddr on the same defensive case
// system.go's remoteIP already documents (a test harness's httptest.Request
// might not set a parseable one).
func remoteEndPoint(r *http.Request) string {
	if ip := remoteIP(r); ip != nil {
		return ip.String()
	}
	return r.RemoteAddr
}
