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
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
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
	PositionTicks       *int64 `json:"PositionTicks,omitempty"`
	CanSeek             bool   `json:"CanSeek"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	VolumeLevel         *int   `json:"VolumeLevel,omitempty"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex,omitempty"`
	MediaSourceID       string `json:"MediaSourceId,omitempty"`
	PlayMethod          string `json:"PlayMethod,omitempty"`
	RepeatMode          string `json:"RepeatMode"`
	PlaybackOrder       string `json:"PlaybackOrder"`
	LiveStreamID        string `json:"LiveStreamId,omitempty"`
}

// transcodingInfoDto is a session's TranscodingInfo: what the stream the
// device is being served looks like (playstate.session.R10). The web client
// reads it to tell a copied picture from an encoded one and to fill its
// playback statistics.
type transcodingInfoDto struct {
	AudioCodec               string   `json:"AudioCodec,omitempty"`
	VideoCodec               string   `json:"VideoCodec,omitempty"`
	Container                string   `json:"Container,omitempty"`
	IsVideoDirect            bool     `json:"IsVideoDirect"`
	IsAudioDirect            bool     `json:"IsAudioDirect"`
	Bitrate                  int      `json:"Bitrate,omitempty"`
	Width                    int      `json:"Width,omitempty"`
	Height                   int      `json:"Height,omitempty"`
	AudioChannels            int      `json:"AudioChannels,omitempty"`
	HardwareAccelerationType string   `json:"HardwareAccelerationType"`
	TranscodeReasons         []string `json:"TranscodeReasons"`
}

// deviceTranscode is the stream a device was last served by a transcode job:
// the job's key, the item, and the TranscodingInfo shown on the session
// (nil once a playback report said the stream is not transcoded).
type deviceTranscode struct {
	key         string
	item        storage.ItemID
	playSession string
	info        *transcodingInfoDto
}

// nowPlayingQueueItemDto is one entry of a session's play queue
// (QueueItem): the item id and the client's playlist entry id.
type nowPlayingQueueItemDto struct {
	ID             string `json:"Id"`
	PlaylistItemID string `json:"PlaylistItemId,omitempty"`
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
	PlayableMediaTypes []string `json:"PlayableMediaTypes"`
	ID                 string   `json:"Id"`
	UserID             string   `json:"UserId"`
	UserName           string   `json:"UserName"`
	Client             string   `json:"Client"`
	LastActivityDate   wireTime `json:"LastActivityDate"`
	// LastPlaybackCheckIn is always the zero time.Time value — this
	// registry has no now-playing tracking wired to it at all (see
	// NowPlayingQueue below), so there is never a real check-in to report.
	LastPlaybackCheckIn wireTime `json:"LastPlaybackCheckIn"`
	DeviceName          string   `json:"DeviceName"`
	DeviceID            string   `json:"DeviceId"`
	ApplicationVersion  string   `json:"ApplicationVersion"`
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
	// NowPlayingQueue has no NowPlayingQueueFullItems sibling: the reference
	// never writes that member (users.shape.R7). The queue is empty unless
	// a client stored one — there is no now-playing tracking wired to this
	// registry beyond that. Playback-progress
	// reporting (sessions_playback.go's handleSessionsPlaying/Progress/
	// Stopped) updates a user's durable Playstate in storage, not "what is
	// this session playing right now"; threading that through this
	// registry is a separate, larger feature, deliberately left as a known
	// gap rather than fabricated here.
	// NowPlayingItem is present only while an item is being played.
	// NowPlayingQueue is whatever the client last stored with a stopped
	// report (playstate.session.R7).
	NowPlayingItem  *baseItemDto             `json:"NowPlayingItem,omitempty"`
	NowPlayingQueue []nowPlayingQueueItemDto `json:"NowPlayingQueue"`
	// HasCustomDeviceName is always false — no per-device rename feature
	// exists (the reference server lets a user rename a device from its
	// registered name; this project has no such store).
	HasCustomDeviceName bool     `json:"HasCustomDeviceName"`
	ServerID            string   `json:"ServerId"`
	SupportedCommands   []string `json:"SupportedCommands"`
	// TranscodingInfo is present from the moment the device's transcode job
	// starts until a report says the stream is not transcoded, or the item
	// stops (playstate.session.R10).
	TranscodingInfo *transcodingInfoDto `json:"TranscodingInfo,omitempty"`
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

	// playing is the live playback state, set by a start/progress report
	// and cleared by a stopped one. queue outlives it: only a stopped
	// report replaces it (playstate.session.R7).
	playing *playingState
	queue   []nowPlayingQueueItemDto
}

// playingState is what a session reports while an item plays: the item as a
// DTO, the mirrored player state and when it was reported (the position
// advances from that moment while the player is not paused).
type playingState struct {
	item       *baseItemDto
	state      sessionPlayStateDto
	runtime    int64
	reportedAt time.Time
}

// sessionRegistry is the in-process (docs/06 default mode) session
// registry: a mutex-guarded map, held on *Server, with no cross-node sharing
// — exactly the "zero additional cost, zero additional process" in-process
// mode docs/06 describes, not the scale-mode Redis/Valkey alternative it also
// documents.
//
// The map is written through to the store on every update (SessionStore) and
// read back at startup (restore), so a restart no longer empties
// GET /Sessions: before that, a redeploy erased every session at once and the
// clients that fill this map only report again when they next happen to talk
// to the server. Live playback state and transcode bookkeeping are not
// persisted — restoring them would claim an item is playing when nothing is,
// and the transcode job dies with the process.
type sessionRegistry struct {
	mu       sync.Mutex
	byDevice map[string]*trackedSession
	// transcodes is keyed by DeviceId and kept apart from byDevice: a
	// device's stream can start before its session reports anything.
	transcodes map[string]*deviceTranscode
	// store is the durable side (nil in tests that only exercise the map:
	// every write-through is skipped then).
	store storage.SessionStore
}

func newSessionRegistry(store storage.SessionStore) *sessionRegistry {
	return &sessionRegistry{byDevice: make(map[string]*trackedSession), transcodes: make(map[string]*deviceTranscode), store: store}
}

// snapshot renders ts as the storage record written through to the store.
// The caller holds the lock.
func (ts *trackedSession) snapshot() storage.Session {
	caps, err := json.Marshal(ts.capabilities)
	if err != nil {
		caps = []byte("{}")
	}
	return storage.Session{
		DeviceID:         storage.DeviceID(ts.deviceID),
		UserID:           storage.UserID(ts.userID),
		SessionID:        ts.id,
		UserName:         ts.userName,
		Client:           ts.client,
		DeviceName:       ts.deviceName,
		AppVersion:       ts.appVersion,
		RemoteEndPoint:   ts.remoteEndPoint,
		Capabilities:     string(caps),
		LastActivityDate: ts.lastActivityDate,
	}
}

// persist writes a session through to the store. Called after the registry's
// own lock is released: a store write never happens under the registry's
// mutex. A store error is dropped on purpose — the live map is the
// authoritative one for the running process, and a failed write only costs
// the session's survival across a restart, not the request being served.
func (r *sessionRegistry) persist(ctx context.Context, session storage.Session) {
	if r.store == nil {
		return
	}
	_ = r.store.SaveSession(ctx, session)
}

// restore loads the persisted sessions into the map at startup, so the
// sessions of a previous run are listed again before any client reports. A
// store error leaves the registry empty rather than failing the start: the
// registry is populated on demand anyway.
func (r *sessionRegistry) restore(ctx context.Context) {
	if r.store == nil {
		return
	}
	saved, err := r.store.QuerySessions(ctx)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sv := range saved {
		if sv.DeviceID == "" {
			continue
		}
		ts := &trackedSession{
			id:               sv.SessionID,
			userID:           string(sv.UserID),
			userName:         sv.UserName,
			client:           sv.Client,
			deviceName:       sv.DeviceName,
			deviceID:         string(sv.DeviceID),
			appVersion:       sv.AppVersion,
			remoteEndPoint:   sv.RemoteEndPoint,
			lastActivityDate: sv.LastActivityDate,
		}
		if ts.id == "" {
			ts.id = randomID()
		}
		if sv.Capabilities != "" {
			_ = json.Unmarshal([]byte(sv.Capabilities), &ts.capabilities)
		}
		r.byDevice[string(sv.DeviceID)] = ts
	}
}

// setTranscode records the transcode job deviceID is now served from.
func (r *sessionRegistry) setTranscode(deviceID, key string, item storage.ItemID, playSession string, info *transcodingInfoDto) {
	if deviceID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transcodes[deviceID] = &deviceTranscode{key: key, item: item, playSession: playSession, info: info}
}

// hasTranscode reports whether deviceID has a stream on record, hidden or not.
func (r *sessionRegistry) hasTranscode(deviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.transcodes[deviceID]
	return ok
}

// transcodeKey is the key of the job deviceID was last served for item, when
// it belongs to the reported play session (an empty one matches any).
func (r *sessionRegistry) transcodeKey(deviceID string, item storage.ItemID, playSession string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.transcodes[deviceID]; ok && t.item == item && (playSession == "" || t.playSession == "" || strings.EqualFold(playSession, t.playSession)) {
		return t.key, true
	}
	return "", false
}

// clearTranscodeInfo hides the TranscodingInfo of deviceID; the job stays known.
func (r *sessionRegistry) clearTranscodeInfo(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.transcodes[deviceID]; ok {
		t.info = nil
	}
}

// dropTranscode forgets the stream of deviceID when it belongs to item (an
// empty item matches any).
func (r *sessionRegistry) dropTranscode(deviceID string, item storage.ItemID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.transcodes[deviceID]; ok && (item == "" || t.item == item) {
		delete(r.transcodes, deviceID)
	}
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
func (r *sessionRegistry) upsertCapabilities(ctx context.Context, deviceID, userID, userName, client, deviceName, appVersion, remoteEndPoint string, caps sessionCapabilitiesDto) {
	if deviceID == "" {
		return
	}
	r.mu.Lock()
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
	snap := ts.snapshot()
	r.mu.Unlock()
	r.persist(ctx, snap)
}

// loginSession renders the session a login creates (users.auth.R12)
// without registering it: the registry only tracks sessions that reported
// capabilities (this file's package doc comment). An already-registered
// session of the same device keeps its id and capabilities.
func (r *sessionRegistry) loginSession(serverID, deviceID, userID, userName, client, deviceName, appVersion, remoteEndPoint string) sessionInfoDto {
	ts := trackedSession{id: randomID()}
	r.mu.Lock()
	if cur, ok := r.byDevice[deviceID]; ok {
		ts.id = cur.id
		ts.capabilities = cur.capabilities
	} else {
		// users.shape.R4: a fresh login session reports persistent ids.
		ts.capabilities.SupportsPersistentIdentifier = true
	}
	r.mu.Unlock()
	ts.userID, ts.userName, ts.client = userID, userName, client
	ts.deviceName, ts.deviceID, ts.appVersion = deviceName, deviceID, appVersion
	ts.remoteEndPoint = remoteEndPoint
	ts.lastActivityDate = time.Now().UTC()
	return ts.dto(serverID)
}

// touch stamps LastActivityDate to now for an already-registered session,
// keyed by deviceID. A no-op if no session has ever registered capabilities
// for that device — this file's package doc comment's deliberately narrow
// scope: sessions_playback.go's playback-report routes touch liveness for
// an existing session, they don't create one on their own.
func (r *sessionRegistry) touch(ctx context.Context, deviceID string) {
	if deviceID == "" {
		return
	}
	r.mu.Lock()
	ts, ok := r.byDevice[deviceID]
	if !ok {
		r.mu.Unlock()
		return
	}
	ts.lastActivityDate = time.Now().UTC()
	snap := ts.snapshot()
	r.mu.Unlock()
	r.persist(ctx, snap)
}

// reportPlayback creates or refreshes the session of deviceID from a
// playback report (identity fields come from the authorization header) and
// lets update change its playing state under the registry lock. Unlike
// touch, it registers a session that never sent capabilities: a client that
// reports playback has a session, and progress reported without a start
// creates it on the fly (playstate.progress.R8).
func (r *sessionRegistry) reportPlayback(ctx context.Context, deviceID, userID, userName, client, deviceName, appVersion, remoteEndPoint string, update func(ts *trackedSession)) {
	if deviceID == "" {
		return
	}
	r.mu.Lock()
	ts, ok := r.byDevice[deviceID]
	if !ok {
		ts = &trackedSession{id: randomID(), deviceID: deviceID}
		r.byDevice[deviceID] = ts
	}
	ts.userID, ts.userName, ts.client = userID, userName, client
	ts.deviceName, ts.appVersion, ts.remoteEndPoint = deviceName, appVersion, remoteEndPoint
	ts.lastActivityDate = time.Now().UTC()
	update(ts)
	snap := ts.snapshot()
	r.mu.Unlock()
	r.persist(ctx, snap)
}

// nowPlayingFor lists the item ids the user is playing in any session.
func (r *sessionRegistry) nowPlayingFor(userID string) []storage.ItemID {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []storage.ItemID
	for _, ts := range r.byDevice {
		if ts.userID == userID && ts.playing != nil && ts.playing.item != nil {
			ids = append(ids, storage.ItemID(ts.playing.item.ID))
		}
	}
	return ids
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
		d := ts.dto(serverID)
		if t := r.transcodes[ts.deviceID]; t != nil {
			d.TranscodingInfo = t.info
		}
		out = append(out, d)
	}
	return out
}

// dto renders the tracked session as a sessionInfoDto.
func (ts *trackedSession) dto(serverID string) sessionInfoDto {
	playState := sessionPlayStateDto{RepeatMode: "RepeatNone", PlaybackOrder: "Default"}
	var nowPlaying *baseItemDto
	var checkIn time.Time
	if p := ts.playing; p != nil {
		playState = p.state
		nowPlaying = p.item
		checkIn = p.reportedAt
		// Automatic progress: while a started item is not paused the
		// position advances in real time, capped at the runtime
		// (playstate.session.R9). Computed on read; nothing is written.
		if playState.PositionTicks != nil && !playState.IsPaused {
			pos := *playState.PositionTicks + time.Since(p.reportedAt).Nanoseconds()/100
			if p.runtime > 0 && pos > p.runtime {
				pos = p.runtime
			}
			playState.PositionTicks = &pos
		}
	}
	queue := ts.queue
	if queue == nil {
		queue = []nowPlayingQueueItemDto{}
	}
	// Wire lists are never null (clients declare them non-nullable).
	caps := ts.capabilities
	if caps.PlayableMediaTypes == nil {
		caps.PlayableMediaTypes = []string{}
	}
	if caps.SupportedCommands == nil {
		caps.SupportedCommands = []string{}
	}
	return sessionInfoDto{
		PlayState:             playState,
		NowPlayingItem:        nowPlaying,
		LastPlaybackCheckIn:   wireTime{checkIn},
		AdditionalUsers:       []struct{}{},
		Capabilities:          caps,
		RemoteEndPoint:        ts.remoteEndPoint,
		PlayableMediaTypes:    caps.PlayableMediaTypes,
		ID:                    ts.id,
		UserID:                ts.userID,
		UserName:              ts.userName,
		Client:                ts.client,
		LastActivityDate:      wireTime{ts.lastActivityDate},
		DeviceName:            ts.deviceName,
		DeviceID:              ts.deviceID,
		ApplicationVersion:    ts.appVersion,
		IsActive:              true,
		SupportsMediaControl:  ts.capabilities.SupportsMediaControl,
		SupportsRemoteControl: ts.capabilities.SupportsMediaControl,
		NowPlayingQueue:       queue,
		HasCustomDeviceName:   false,
		ServerID:              serverID,
		SupportedCommands:     caps.SupportedCommands,
	}
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
	list := s.sessions.list(s.cfg.ServerID, activeWithinSeconds, hasFilter)
	// deviceId narrows the list to the sessions of one device id.
	if dev := caseInsensitiveQuery(r).Get("deviceid"); dev != "" {
		kept := list[:0]
		for _, sess := range list {
			if sess.DeviceID == dev {
				kept = append(kept, sess)
			}
		}
		list = kept
	}
	// A custom device name (admin.devices.R7) replaces the reported one.
	for i := range list {
		if _, name, found, err := s.store.GetDeviceOptions(r.Context(), storage.DeviceID(list[i].DeviceID)); err == nil && found && name != nil && *name != "" {
			list[i].DeviceName = *name
			list[i].HasCustomDeviceName = true
		}
	}
	writeJSON(w, http.StatusOK, list)
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

// dropDevice forgets the session and the transcode bookkeeping of a device
// that was deleted (DELETE /Devices), and removes its persisted session.
func (r *sessionRegistry) dropDevice(ctx context.Context, deviceID string) {
	r.mu.Lock()
	delete(r.byDevice, deviceID)
	delete(r.transcodes, deviceID)
	r.mu.Unlock()
	if r.store != nil && deviceID != "" {
		_ = r.store.DeleteSession(ctx, storage.DeviceID(deviceID))
	}
}

// dropUser forgets and unpins every session of a deleted account.
func (r *sessionRegistry) dropUser(ctx context.Context, userID string) {
	r.mu.Lock()
	for id, ts := range r.byDevice {
		if ts.userID == userID {
			delete(r.byDevice, id)
			delete(r.transcodes, id)
		}
	}
	r.mu.Unlock()
	if r.store != nil && userID != "" {
		_ = r.store.DeleteUserSessions(ctx, storage.UserID(userID))
	}
}
