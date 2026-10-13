// websocket.go implements GET /socket, the realtime session channel
// docs/06-session-and-realtime-state.md scopes as its own interface.
// Behavior extracted per the two-phase clean-room process (AGENTS.md, D12)
// into docs/12-clean-room-behavior-specs.md's "Dashboard overview — the
// GET /socket realtime channel" section; the design choices below follow
// that section's "Known chronic issues in the reference implementation —
// deliberately not reproduced" subsection point by point (referenced
// inline as "chronic issue #N").
//
// Three data channels (Sessions, ScheduledTasksInfo, ActivityLogEntry) are
// push-on-event, never on a timer — see docs/12 for why. RefreshProgress
// (per-item scan progress) is deliberately not implemented: internal/scan's
// Scanner has no per-item progress callback to hook today, and fabricating
// one just for this wire message would be scope creep beyond what this
// pass set out to close; a documented gap, not a silent omission.
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const (
	// wsLostTimeout mirrors the reference server's own confirmed value
	// (docs/12: ForceKeepAlive's Data field, observed live as 60).
	wsLostTimeout = 60 * time.Second
	// wsWatchInterval is how often the watchdog scans every connection —
	// docs/12 observed roughly 12s (60s * 0.2) against the reference server.
	wsWatchInterval = 12 * time.Second
	// wsForceKeepAliveAt is the idle threshold (75% of wsLostTimeout,
	// docs/12) past which a connection gets an extra ForceKeepAlive nudge
	// before being declared lost outright at wsLostTimeout.
	wsForceKeepAliveAt = 45 * time.Second
	// wsActivityLogPushLimit bounds the recent-entries page notifyActivityLog
	// sends — see that function's doc comment for why this project sends
	// real entries at all, unlike the reference server's own equivalent
	// listener (docs/12's C# cross-check).
	wsActivityLogPushLimit = 10
)

// wsOutbound is the outbound frame envelope every pushed/acked message
// shares — docs/12's captured shape: {"MessageId":...,"MessageType":...,
// "Data":...}. MessageId is opaque; nothing in this project's own client
// ever needs to correlate it with anything, it exists only to match the
// wire shape a real client expects.
type wsOutbound struct {
	MessageID   string `json:"MessageId,omitempty"`
	MessageType string `json:"MessageType"`
	Data        any    `json:"Data,omitempty"`
}

// wsInbound is the shape of every client-sent frame this project needs to
// inspect. Data is only meaningful for the three "...Start" message types
// (a "<initialDelayMs>,<intervalMs>" string, docs/12) — ignored otherwise.
type wsInbound struct {
	MessageType string `json:"MessageType"`
	Data        string `json:"Data"`
}

// wsChannelState is one periodic channel's subscription state for one
// connection (Sessions/ScheduledTasksInfo/ActivityLogEntry, docs/12).
// intervalMs is the per-connection minimum spacing between throttled
// (non-forced) sends — a forced send (a real state transition, per
// docs/12's "Behavior, in prose") ignores it entirely.
type wsChannelState struct {
	subscribed  bool
	intervalMs  int64
	lastSentUTC time.Time
}

// wsConn is one accepted /socket connection's state. lastActivity is the
// single field the read loop and the keep-alive watchdog both read/write,
// and hub membership is torn down from exactly one place (handleSocket's
// deferred cleanup, triggered by the read loop returning) — see docs/12's
// chronic issue #2 ("connection cleanup is each subsystem's own
// independent responsibility, not one canonical signal") for why this
// project deliberately does not give each data channel its own
// independent "is this connection still alive" bookkeeping.
type wsConn struct {
	conn    *websocket.Conn
	userID  string
	isAdmin bool

	mu           sync.Mutex
	lastActivity time.Time
	sessions     wsChannelState
	tasks        wsChannelState
	activity     wsChannelState
}

func (c *wsConn) touch() {
	c.mu.Lock()
	c.lastActivity = time.Now().UTC()
	c.mu.Unlock()
}

func (c *wsConn) idleFor() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastActivity)
}

// send writes an outbound message to this connection. Safe to call
// concurrently with other sends to the same connection and with its own
// read loop — coder/websocket's *Conn documents every method except
// Reader/Read as concurrency-safe (D13, docs/00-decision-log.md), which is
// why this project doesn't need a dedicated single-writer goroutine per
// connection the way docs/12's chronic issue #1 describes the reference
// server's underlying socket type as requiring. A failed write is
// swallowed here rather than acted on: the read loop, blocked on the same
// underlying connection, is the single place a dead connection gets
// noticed and cleaned up (see the wsConn doc comment above) — a send
// failure here racing to draw the same conclusion independently is exactly
// the fragmentation docs/12 flags.
func (c *wsConn) send(ctx context.Context, messageType string, data any) {
	payload, err := json.Marshal(wsOutbound{MessageID: randomID(), MessageType: messageType, Data: data})
	if err != nil {
		log.Printf("api: marshaling websocket %s message: %v", messageType, err)
		return
	}
	_ = c.conn.Write(ctx, websocket.MessageText, payload)
}

// wsHub tracks every currently-connected /socket connection so a state
// change elsewhere in the server (a session's capabilities changing, a
// scan starting/finishing, a new activity-log entry) can push to whichever
// connections are interested. One hub per Server, created in NewServer,
// alive for the process's lifetime — matches this project's existing
// scanTracker/sessionRegistry precedent of a single long-lived, mutex-
// guarded in-process structure rather than per-request state.
type wsHub struct {
	mu    sync.Mutex
	conns map[*wsConn]struct{}
}

func newWSHub() *wsHub { return &wsHub{conns: make(map[*wsConn]struct{})} }

func (h *wsHub) add(c *wsConn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *wsHub) remove(c *wsConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

func (h *wsHub) snapshot() []*wsConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		out = append(out, c)
	}
	return out
}

// handleSocket is GET /socket, an unauthenticated upgrade attempt is refused
// (403) before the socket is ever accepted.
//
// The connection is tied to the caller's tracked session (sessionRegistry,
// sessions.go) the same way POST /Sessions/Capabilities/Full is, per
// docs/12's "connection is tied to a session, not created standalone" —
// remote-control command delivery over that session (GeneralCommand et al)
// stays explicitly out of scope, same as docs/11 already tracks it.
func (s *Server) handleSocket(w http.ResponseWriter, r *http.Request) {
	// users.socket.R2: a missing, unknown or revoked credential is refused
	// before the upgrade with 403 (not the 401 of the REST routes).
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil || !ac.Authenticated {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	c := &wsConn{
		conn:         conn,
		userID:       string(ac.User.ID),
		isAdmin:      ac.User.IsAdministrator,
		lastActivity: time.Now().UTC(),
	}
	s.hub.add(c)
	defer s.hub.remove(c)
	defer conn.CloseNow()

	s.sessions.touch(ac.DeviceID)

	ctx := r.Context()
	// Sent immediately on connect, before anything else — docs/12's
	// captured handshake order.
	c.send(ctx, "ForceKeepAlive", int64(wsLostTimeout/time.Second))

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		c.touch()

		var in wsInbound
		if err := json.Unmarshal(data, &in); err != nil {
			// A single malformed frame must not tear down the whole
			// connection — docs/12's chronic-issue research surfaced
			// jellyfin/jellyfin#11395 ("disconnecting when exception is
			// thrown during processing") as exactly this failure mode.
			continue
		}
		s.handleSocketMessage(ctx, c, in)
	}
}

// handleSocketMessage dispatches one already-deserialized inbound frame.
// KeepAlive is answered directly, exactly like the reference server
// special-cases it ahead of per-channel dispatch (docs/12). The three
// "...Start"/"...Stop" pairs only ever update this connection's own
// subscription state — the initial delay half of a Start message's
// "<initialDelayMs>,<intervalMs>" Data is parsed but intentionally unused:
// nothing in docs/12's extracted behavior schedules a first send off it (no
// send happens until a real event fires, forced sends ignore both halves
// entirely), and the Dashboard's own initial view already comes from the
// plain REST routes (GET /Sessions, GET /ScheduledTasks), not this channel.
func (s *Server) handleSocketMessage(ctx context.Context, c *wsConn, in wsInbound) {
	switch in.MessageType {
	case "KeepAlive":
		c.send(ctx, "KeepAlive", nil)
	case "SessionsStart":
		_, intervalMs := parseWSInterval(in.Data)
		c.mu.Lock()
		c.sessions = wsChannelState{subscribed: true, intervalMs: intervalMs}
		c.mu.Unlock()
	case "SessionsStop":
		c.mu.Lock()
		c.sessions = wsChannelState{}
		c.mu.Unlock()
	case "ScheduledTasksInfoStart":
		_, intervalMs := parseWSInterval(in.Data)
		c.mu.Lock()
		c.tasks = wsChannelState{subscribed: true, intervalMs: intervalMs}
		c.mu.Unlock()
	case "ScheduledTasksInfoStop":
		c.mu.Lock()
		c.tasks = wsChannelState{}
		c.mu.Unlock()
	case "ActivityLogEntryStart":
		// Admin-only, matching the reference server exactly (docs/12) —
		// unlike Sessions, which any authenticated connection may
		// subscribe to. Silently ignored for a non-admin, same as the
		// reference server logging-and-continuing rather than tearing the
		// whole connection down over one rejected subscription request.
		if !c.isAdmin {
			return
		}
		_, intervalMs := parseWSInterval(in.Data)
		c.mu.Lock()
		c.activity = wsChannelState{subscribed: true, intervalMs: intervalMs}
		c.mu.Unlock()
	case "ActivityLogEntryStop":
		c.mu.Lock()
		c.activity = wsChannelState{}
		c.mu.Unlock()
	}
}

// parseWSInterval parses a Start message's Data field
// ("<initialDelayMs>,<intervalMs>", docs/12). Malformed input yields
// (0, 0) rather than an error — intervalMs 0 just means "no throttling,"
// a safe and harmless degradation, not a divide-by-zero risk anywhere
// this value is used (time.Duration(0) is a valid, always-elapsed
// duration).
func parseWSInterval(data string) (initialDelayMs, intervalMs int64) {
	parts := strings.SplitN(data, ",", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	initialDelayMs, _ = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	intervalMs, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	return initialDelayMs, intervalMs
}

// runWSWatchdog is the single keep-alive authority for every connection in
// the hub — one shared ticker, not one timer per connection, matching the
// reference server's own approach (docs/12) and this project's existing
// preference for one shared background loop over N per-item goroutines.
// A connection idle past wsForceKeepAliveAt gets an extra nudge; one idle
// past wsLostTimeout the full wsLostTimeout is declared lost and force-
// closed — CloseNow unblocks that connection's own conn.Read in
// handleSocket, which is what actually runs the single, canonical cleanup
// path (docs/12's chronic issue #2, and the fixed behavior chronic issue
// #3 describes: a lost connection must end its session, not just stop
// being watched — closing it is what makes that happen here, the same way
// disposing the reference server's own lost socket does).
func (s *Server) runWSWatchdog(ctx context.Context) {
	ticker := time.NewTicker(wsWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, c := range s.hub.snapshot() {
				idle := c.idleFor()
				switch {
				case idle >= wsLostTimeout:
					_ = c.conn.CloseNow()
				case idle >= wsForceKeepAliveAt:
					c.send(ctx, "ForceKeepAlive", int64(wsLostTimeout/time.Second))
				}
			}
		}
	}
}

// notifySessions pushes the current session list to every connection
// subscribed to the Sessions channel. force bypasses a connection's own
// registered throttle interval — every real session-lifecycle transition
// (capabilities registered, playback started/stopped) is forced, matching
// the reference server's own force/throttle split (docs/12); only a bare
// playback-progress heartbeat passes force=false. A non-admin connection's
// payload is filtered to its own user's sessions — deliberately different
// from GET /Sessions's own admin-only REST scoping (sessions.go), matching
// the reference server's own per-connection filtering for this specific
// channel (docs/12's "Session-list visibility is per-connection, not
// admin-gated").
func (s *Server) notifySessions(ctx context.Context, force bool) {
	conns := s.hub.snapshot()
	if len(conns) == 0 {
		return
	}
	all := s.sessions.list(s.cfg.ServerID, 0, false)
	now := time.Now().UTC()
	for _, c := range conns {
		c.mu.Lock()
		due := c.sessions.subscribed && (force || now.Sub(c.sessions.lastSentUTC) >= time.Duration(c.sessions.intervalMs)*time.Millisecond)
		if due {
			c.sessions.lastSentUTC = now
		}
		isAdmin, userID := c.isAdmin, c.userID
		c.mu.Unlock()
		if !due {
			continue
		}
		payload := all
		if !isAdmin {
			payload = filterSessionsForUser(all, userID)
		}
		c.send(ctx, "Sessions", payload)
	}
}

func filterSessionsForUser(all []sessionInfoDto, userID string) []sessionInfoDto {
	out := make([]sessionInfoDto, 0, len(all))
	for _, sess := range all {
		if sess.UserID == userID {
			out = append(out, sess)
		}
	}
	return out
}

// notifyScheduledTasks pushes the current task list (scheduledTasksSnapshot,
// scheduled_tasks.go — the same data GET /ScheduledTasks reports) to every
// subscribed connection. force semantics match notifySessions: a scan
// starting or finishing is always forced (docs/12).
func (s *Server) notifyScheduledTasks(ctx context.Context, force bool) {
	conns := s.hub.snapshot()
	if len(conns) == 0 {
		return
	}
	tasks := s.scheduledTasksSnapshot()
	now := time.Now().UTC()
	for _, c := range conns {
		c.mu.Lock()
		due := c.tasks.subscribed && (force || now.Sub(c.tasks.lastSentUTC) >= time.Duration(c.tasks.intervalMs)*time.Millisecond)
		if due {
			c.tasks.lastSentUTC = now
		}
		c.mu.Unlock()
		if !due {
			continue
		}
		c.send(ctx, "ScheduledTasksInfo", tasks)
	}
}

// notifyActivityLog pushes the most recent activity-log entries to every
// admin connection subscribed to the ActivityLogEntry channel, whenever a
// new entry is recorded (auth_by_name.go's logAuthSuccess/logAuthFailure).
//
// The reference server's own equivalent listener sends an empty array on
// every push regardless of trigger (docs/12's C# cross-check found this
// directly in its GetDataToSend override) — apparently using the push
// purely as a "something changed, go re-fetch over REST" signal rather
// than a data-carrying one. Deliberately not replicated: no black-box
// observation ever depended on an empty payload here, and sending the real
// entries is strictly more useful with no confirmed compatibility cost —
// flagged here as an intentional divergence, per this project's own
// convention for such cases, not a silent "improvement."
func (s *Server) notifyActivityLog(ctx context.Context) {
	conns := s.hub.snapshot()
	haveSubscriber := false
	for _, c := range conns {
		c.mu.Lock()
		sub := c.activity.subscribed
		c.mu.Unlock()
		if sub {
			haveSubscriber = true
			break
		}
	}
	if !haveSubscriber {
		return
	}

	entries, _, err := s.store.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: wsActivityLogPushLimit})
	if err != nil {
		log.Printf("api: querying activity entries for websocket push: %v", err)
		return
	}
	items := make([]activityLogEntryDto, 0, len(entries))
	for _, e := range entries {
		dto := activityLogEntryDto{
			Id:            e.ID,
			Name:          e.Name,
			ShortOverview: e.ShortOverview,
			Type:          string(e.Type),
			Date:          e.Date,
			Severity:      string(e.Severity),
		}
		if e.UserID != nil {
			uid := string(*e.UserID)
			dto.UserId = &uid
		}
		items = append(items, dto)
	}

	for _, c := range conns {
		c.mu.Lock()
		sub := c.activity.subscribed
		c.mu.Unlock()
		if !sub {
			continue
		}
		c.send(ctx, "ActivityLogEntry", items)
	}
}

// notifyUserUpdated pushes a "UserUpdated" message carrying the user object
// to every open connection of that user (users.update: a rename is
// announced to the user's open sessions). Other users' connections are not
// told.
func (s *Server) notifyUserUpdated(ctx context.Context, u storage.User) {
	var payload *userDto
	for _, c := range s.hub.snapshot() {
		c.mu.Lock()
		match := c.userID == string(u.ID)
		c.mu.Unlock()
		if !match {
			continue
		}
		if payload == nil {
			dto := s.userDtoCtx(ctx, u)
			payload = &dto
		}
		c.send(ctx, "UserUpdated", payload)
	}
}
