package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newSocketTestServer is newTestServer (api_test.go) plus returning *Server
// itself — GET /socket is registered directly in server.go's routes() (no
// separate test-local mux needed, unlike scheduled_tasks_test.go's
// newTestServerWithScheduledTasks, which predates that route's real
// registration), but tests below need srv.scanState/srv.hub directly to
// drive a push without a real filesystem scan.
func newSocketTestServer(t *testing.T) (*httptest.Server, *sqlite.Store, *Server) {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	srv := NewServer(st, Config{ServerName: "TestServer"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, srv
}

// dialSocket dials GET /socket with the given token as an Authorization
// header (docs/12: the same auth mechanism as every other route, not a
// separate WebSocket-specific scheme).
func dialSocket(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, ts.URL+"/socket", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{`MediaBrowser Token="` + token + `"`}},
	})
	if err != nil {
		t.Fatalf("dialing /socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// readSocketMessage reads and decodes one outbound frame within a short
// deadline — every test below expects a specific message to arrive
// promptly (a real push, not a timing-dependent throttled one), so a
// generous but bounded timeout catches a regression (message never sent)
// as a test failure rather than a hang.
func readSocketMessage(t *testing.T, conn *websocket.Conn) wsOutbound {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading socket message: %v", err)
	}
	var out wsOutbound
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decoding socket message %s: %v", data, err)
	}
	return out
}

// waitForSubscription polls until srv's hub shows at least one connection
// with the given channel subscribed, or fails the test. Sending a Start
// message (sendSocketMessage) only guarantees the client wrote the frame —
// the server's own read loop still has to receive and process it before
// srv.hub reflects the subscription, so a test that immediately triggers a
// notify* call right after sending Start has a real (if usually narrow)
// race without this.
func waitForSubscription(t *testing.T, srv *Server, subscribed func(*wsConn) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range srv.hub.snapshot() {
			if subscribed(c) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no connection reached the expected subscription state before timeout")
}

func sendSocketMessage(t *testing.T, conn *websocket.Conn, messageType, data string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload, err := json.Marshal(wsInbound{MessageType: messageType, Data: data})
	if err != nil {
		t.Fatalf("marshaling %s: %v", messageType, err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("writing %s: %v", messageType, err)
	}
}

// TestSocketRequiresAuth covers requireAuth's role here: an unauthenticated
// upgrade attempt never becomes a WebSocket connection at all.
func TestSocketRequiresAuth(t *testing.T) {
	ts, _, _ := newSocketTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, ts.URL+"/socket", nil)
	if err == nil {
		t.Fatal("Dial succeeded with no auth, want an error")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 401", status)
	}
}

// TestSocketKeepAliveHandshake covers docs/12's captured handshake: an
// immediate ForceKeepAlive on connect, and a KeepAlive request answered
// with a KeepAlive ack.
func TestSocketKeepAliveHandshake(t *testing.T) {
	ts, st, _ := newSocketTestServer(t)
	user := createTestUser(t, st, "alice", "hunter2")
	token := loginToken(t, ts.URL, user.Name, "hunter2")

	conn := dialSocket(t, ts, token)

	first := readSocketMessage(t, conn)
	if first.MessageType != "ForceKeepAlive" {
		t.Fatalf("first message type = %q, want ForceKeepAlive", first.MessageType)
	}
	if got, want := int64(first.Data.(float64)), int64(wsLostTimeout/time.Second); got != want {
		t.Errorf("ForceKeepAlive Data = %d, want %d", got, want)
	}

	sendSocketMessage(t, conn, "KeepAlive", "")
	ack := readSocketMessage(t, conn)
	if ack.MessageType != "KeepAlive" {
		t.Fatalf("ack message type = %q, want KeepAlive", ack.MessageType)
	}
}

// TestSocketSessionsPushOnCapabilitiesChange covers the Sessions channel's
// push-on-event model (docs/12): subscribing alone sends nothing (no
// initial snapshot — the Dashboard's own initial view comes from GET
// /Sessions instead), but a real capabilities registration from another
// device forces an immediate push to a subscribed, authenticated
// connection, filtered to nothing beyond the admin scope tested here (an
// admin sees every session, not just its own — see the non-admin variant
// below for the filtered case).
func TestSocketSessionsPushOnCapabilitiesChange(t *testing.T) {
	ts, st, srv := newSocketTestServer(t)
	admin := createTestAdminUser(t, st, "admin", "adminpass")
	adminToken := loginToken(t, ts.URL, admin.Name, "adminpass")

	conn := dialSocket(t, ts, adminToken)
	_ = readSocketMessage(t, conn) // ForceKeepAlive

	sendSocketMessage(t, conn, "SessionsStart", "0,1000")
	waitForSubscription(t, srv, func(c *wsConn) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.sessions.subscribed
	})

	// A second, unrelated device registers capabilities — the trigger
	// notifySessions(force=true) fires from (system.go's
	// handleSessionsCapabilitiesFull).
	other := createTestUser(t, st, "bob", "bobpass")
	otherToken := loginToken(t, ts.URL, other.Name, "bobpass")
	capResp := sessionsAuthedRequest(t, ts.URL, otherToken, "Jellyfin Web", "Chrome", "device-bob-1", "10.11.11", http.MethodPost, "/Sessions/Capabilities/Full", sessionCapabilitiesDto{SupportsMediaControl: true})
	capResp.Body.Close()

	msg := readSocketMessage(t, conn)
	if msg.MessageType != "Sessions" {
		t.Fatalf("message type = %q, want Sessions", msg.MessageType)
	}
	sessions, ok := msg.Data.([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("Sessions Data = %#v, want a one-element array", msg.Data)
	}
}

// TestSocketSessionsFilteredForNonAdmin covers docs/12's "Session-list
// visibility is per-connection, not admin-gated" finding: any authenticated
// connection may subscribe, but a non-admin's own payload is filtered down
// to sessions it owns — deliberately different from GET /Sessions's
// admin-only REST scoping (sessions.go).
func TestSocketSessionsFilteredForNonAdmin(t *testing.T) {
	ts, st, srv := newSocketTestServer(t)
	alice := createTestUser(t, st, "alice2", "alicepass")
	aliceToken := loginToken(t, ts.URL, alice.Name, "alicepass")

	conn := dialSocket(t, ts, aliceToken)
	_ = readSocketMessage(t, conn) // ForceKeepAlive
	sendSocketMessage(t, conn, "SessionsStart", "0,1000")
	waitForSubscription(t, srv, func(c *wsConn) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.sessions.subscribed
	})

	// Alice's own device registers capabilities...
	capResp := sessionsAuthedRequest(t, ts.URL, aliceToken, "Jellyfin Web", "Chrome", "device-alice-2", "10.11.11", http.MethodPost, "/Sessions/Capabilities/Full", sessionCapabilitiesDto{SupportsMediaControl: true})
	capResp.Body.Close()

	msg := readSocketMessage(t, conn)
	sessions, ok := msg.Data.([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("Sessions Data = %#v, want exactly Alice's own one session", msg.Data)
	}
	entry, ok := sessions[0].(map[string]any)
	if !ok || entry["UserId"] != string(alice.ID) {
		t.Fatalf("session entry = %#v, want UserId %q", entry, alice.ID)
	}

	// ...a different user's device also registers capabilities. Alice's
	// subscription must not see it.
	bob := createTestUser(t, st, "bobby", "bobbypass")
	bobToken := loginToken(t, ts.URL, bob.Name, "bobbypass")
	capResp2 := sessionsAuthedRequest(t, ts.URL, bobToken, "Jellyfin Web", "Chrome", "device-bobby-1", "10.11.11", http.MethodPost, "/Sessions/Capabilities/Full", sessionCapabilitiesDto{SupportsMediaControl: true})
	capResp2.Body.Close()

	msg2 := readSocketMessage(t, conn)
	sessions2, ok := msg2.Data.([]any)
	if !ok || len(sessions2) != 1 {
		t.Fatalf("Sessions Data after Bob's registration = %#v, want still exactly one (Alice's own)", msg2.Data)
	}
	entry2, ok := sessions2[0].(map[string]any)
	if !ok || entry2["UserId"] != string(alice.ID) {
		t.Fatalf("session entry after Bob's registration = %#v, want still Alice's own", entry2)
	}
}

// TestSocketScheduledTasksInfoPushOnScanStateChange covers the
// ScheduledTasksInfo channel's push-on-event model, driving Server.scanState
// directly (same technique scheduled_tasks_test.go uses) rather than
// running a real filesystem scan.
func TestSocketScheduledTasksInfoPushOnScanStateChange(t *testing.T) {
	ts, st, srv := newSocketTestServer(t)
	admin := createTestAdminUser(t, st, "admin2", "adminpass")
	token := loginToken(t, ts.URL, admin.Name, "adminpass")

	conn := dialSocket(t, ts, token)
	_ = readSocketMessage(t, conn) // ForceKeepAlive
	sendSocketMessage(t, conn, "ScheduledTasksInfoStart", "0,1000")
	waitForSubscription(t, srv, func(c *wsConn) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.tasks.subscribed
	})

	srv.scanState.begin()
	srv.notifyScheduledTasks(context.Background(), true)

	msg := readSocketMessage(t, conn)
	if msg.MessageType != "ScheduledTasksInfo" {
		t.Fatalf("message type = %q, want ScheduledTasksInfo", msg.MessageType)
	}
	tasks, ok := msg.Data.([]any)
	if !ok || len(tasks) != 1 {
		t.Fatalf("ScheduledTasksInfo Data = %#v, want a one-element array", msg.Data)
	}
	task, ok := tasks[0].(map[string]any)
	if !ok || task["State"] != "Running" {
		t.Fatalf("task = %#v, want State \"Running\"", task)
	}
}

// TestSocketActivityLogEntryAdminOnly covers docs/12's finding that
// ActivityLogEntryStart is rejected outright for a non-admin (silently, the
// connection stays open) while an admin's subscription receives real
// recent entries on the next append (auth_by_name.go's
// logAuthSuccess/logAuthFailure).
func TestSocketActivityLogEntryAdminOnly(t *testing.T) {
	ts, st, srv := newSocketTestServer(t)
	// Created (and logged in) before either socket connects, so their own
	// SessionStarted login entries don't land in between the subscription
	// below and the failed-login trigger this test actually cares about.
	plain := createTestUser(t, st, "plain", "plainpass")
	plainToken := loginToken(t, ts.URL, plain.Name, "plainpass")
	admin := createTestAdminUser(t, st, "admin3", "adminpass")
	adminToken := loginToken(t, ts.URL, admin.Name, "adminpass")

	plainConn := dialSocket(t, ts, plainToken)
	_ = readSocketMessage(t, plainConn) // ForceKeepAlive
	sendSocketMessage(t, plainConn, "ActivityLogEntryStart", "0,1000")

	adminConn := dialSocket(t, ts, adminToken)
	_ = readSocketMessage(t, adminConn) // ForceKeepAlive
	sendSocketMessage(t, adminConn, "ActivityLogEntryStart", "0,1000")
	waitForSubscription(t, srv, func(c *wsConn) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.activity.subscribed
	})

	// Trigger a real activity-log append: a failed login.
	resp := authenticate(t, ts.URL, "no-such-user", "whatever")
	resp.Body.Close()

	msg := readSocketMessage(t, adminConn)
	if msg.MessageType != "ActivityLogEntry" {
		t.Fatalf("admin message type = %q, want ActivityLogEntry", msg.MessageType)
	}
	// notifyActivityLog sends the most recent entries (up to
	// wsActivityLogPushLimit), not just the one that triggered this push
	// (websocket.go's own doc comment on notifyActivityLog explains why) —
	// the two logins above already appended their own SessionStarted
	// entries, so this asserts the newest entry (query order is newest
	// first) is the failed login that triggered this specific push, not
	// that it's the only entry present.
	entries, ok := msg.Data.([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("ActivityLogEntry Data = %#v, want at least one entry", msg.Data)
	}
	newest, ok := entries[0].(map[string]any)
	if !ok || newest["Type"] != "AuthenticationFailed" {
		t.Fatalf("newest entry = %#v, want Type AuthenticationFailed", newest)
	}

	// The non-admin connection must never receive an ActivityLogEntry push
	// at all — its subscription request was rejected outright (docs/12).
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, _, err := plainConn.Read(ctx); err == nil {
		t.Fatal("plainConn.Read succeeded, want a timeout (no push should ever arrive)")
	}
}

func TestParseWSInterval(t *testing.T) {
	cases := []struct {
		data               string
		wantInitial, wantI int64
	}{
		{"0,1500", 0, 1500},
		{"1000,1000", 1000, 1000},
		{"garbage", 0, 0},
		{"", 0, 0},
		// SplitN(data, ",", 2) caps at two parts, so a third comma just
		// makes the second part unparseable rather than rejecting the
		// whole value — documented, lenient behavior, not a strict "must
		// be exactly two comma-separated integers" validator.
		{"1,2,3", 1, 0},
	}
	for _, c := range cases {
		gotInitial, gotInterval := parseWSInterval(c.data)
		if gotInitial != c.wantInitial || gotInterval != c.wantI {
			t.Errorf("parseWSInterval(%q) = (%d, %d), want (%d, %d)", c.data, gotInitial, gotInterval, c.wantInitial, c.wantI)
		}
	}
}

// TestSocketUserUpdatedOnRename covers users.update: a rename is announced
// to the renamed user's open sockets (and only theirs); an update that keeps
// the name is not.
func TestSocketUserUpdatedOnRename(t *testing.T) {
	ts, st, _ := newSocketTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	createTestUser(t, st, "bob", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")

	aliceConn := dialSocket(t, ts, loginToken(t, ts.URL, "alice", "pw"))
	_ = readSocketMessage(t, aliceConn) // ForceKeepAlive
	bobConn := dialSocket(t, ts, loginToken(t, ts.URL, "bob", "pw"))
	_ = readSocketMessage(t, bobConn) // ForceKeepAlive

	// Same name: no announcement.
	if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users?userId="+string(alice.ID), updateBody("alice", "")); code != http.StatusNoContent {
		t.Fatalf("no-op update: %d", code)
	}
	if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users?userId="+string(alice.ID), updateBody("alice-renamed", "")); code != http.StatusNoContent {
		t.Fatalf("rename: %d", code)
	}

	msg := readSocketMessage(t, aliceConn)
	if msg.MessageType != "UserUpdated" {
		t.Fatalf("message type = %q, want UserUpdated", msg.MessageType)
	}
	data, ok := msg.Data.(map[string]any)
	if !ok || data["Name"] != "alice-renamed" || data["Id"] != string(alice.ID) {
		t.Fatalf("UserUpdated Data = %#v", msg.Data)
	}

	// Bob's socket got nothing: the next frame he sees is the keep-alive
	// answer to his own probe, not a UserUpdated.
	sendSocketMessage(t, bobConn, "KeepAlive", "")
	if m := readSocketMessage(t, bobConn); m.MessageType != "KeepAlive" {
		t.Errorf("bob received %q, want only his KeepAlive answer", m.MessageType)
	}
}
