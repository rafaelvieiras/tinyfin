package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// createKey creates an API key through the admin route and returns its
// token, read back from the listing by its app name.
func createKey(t *testing.T, base, adminTok, app string) string {
	t.Helper()
	if code, _ := policyDo(t, base, adminTok, http.MethodPost, "/Auth/Keys?app="+app, ""); code != http.StatusNoContent {
		t.Fatalf("create key: %d", code)
	}
	code, body := policyDo(t, base, adminTok, http.MethodGet, "/Auth/Keys", "")
	if code != http.StatusOK {
		t.Fatalf("list keys: %d", code)
	}
	var res struct {
		Items []struct{ AccessToken, AppName, UserId string }
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if it.AppName == app {
			if it.UserId != "00000000000000000000000000000000" {
				t.Errorf("UserId = %q, want all-zero", it.UserId)
			}
			return it.AccessToken
		}
	}
	t.Fatalf("key %q not in listing: %s", app, body)
	return ""
}

func apiKeyDo(t *testing.T, base, key, method, path string) (int, []byte) {
	t.Helper()
	return rawDo(t, base, method, path+sep(path)+"ApiKey="+key, "", "")
}

func sep(p string) string {
	if strings.Contains(p, "?") {
		return "&"
	}
	return "?"
}

func assertProblem(t *testing.T, code int, body []byte, wantStatus int, wantTitle string, wantErrors bool) {
	t.Helper()
	if code != wantStatus {
		t.Fatalf("status = %d, want %d (%s)", code, wantStatus, body)
	}
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("body is not JSON: %q", body)
	}
	if int(p["status"].(float64)) != wantStatus || p["title"] != wantTitle {
		t.Errorf("problem = %v", p)
	}
	if s, _ := p["traceId"].(string); s == "" {
		t.Errorf("no traceId: %v", p)
	}
	if _, has := p["errors"].(map[string]any); has != wantErrors {
		t.Errorf("errors object present = %v, want %v: %v", has, wantErrors, p)
	}
}

func TestMalformedIDIsValidationProblem(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	tok := policyLogin(t, ts.URL, "admin", "adminpass")

	code, body := policyDo(t, ts.URL, tok, http.MethodGet, "/Users/not-a-guid", "")
	assertProblem(t, code, body, 400, "One or more validation errors occurred.", true)

	code, body = policyDo(t, ts.URL, tok, http.MethodGet, "/Items/not-a-guid", "")
	assertProblem(t, code, body, 400, "One or more validation errors occurred.", true)

	code, body = policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", "{bad")
	assertProblem(t, code, body, 400, "One or more validation errors occurred.", true)

	code, body = policyDo(t, ts.URL, tok, http.MethodPost, "/Users/New", `{"Name":""}`)
	assertProblem(t, code, body, 400, "Bad Request", false)
}

func TestAPIKeyLifecycleAndIdentity(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	createTestUser(t, st, "alice", "pw")
	admin := policyLogin(t, ts.URL, "admin", "adminpass")
	aliceTok := policyLogin(t, ts.URL, "alice", "pw")

	// Administrator only.
	if code, _ := policyDo(t, ts.URL, aliceTok, http.MethodGet, "/Auth/Keys", ""); code != http.StatusForbidden {
		t.Errorf("non-admin list: %d, want 403", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodGet, "/Auth/Keys", "", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous list: %d, want 401", code)
	}
	if code, body := policyDo(t, ts.URL, admin, http.MethodPost, "/Auth/Keys", ""); code != http.StatusBadRequest {
		t.Errorf("missing app: %d %s", code, body)
	}

	key := createKey(t, ts.URL, admin, "test-app")

	// The key authenticates, but as no user.
	code, body := apiKeyDo(t, ts.URL, key, http.MethodGet, "/Users/Me")
	assertProblem(t, code, body, 400, "Bad Request", false)
	if code, body := apiKeyDo(t, ts.URL, key, http.MethodGet, "/Users"); code != http.StatusOK || !strings.HasPrefix(string(body), "[") {
		t.Errorf("list users with key: %d %s", code, body)
	}
	if code, _ := apiKeyDo(t, ts.URL, key, http.MethodGet, "/Auth/Keys"); code != http.StatusOK {
		t.Errorf("key lists keys: %d", code)
	}

	// Revoke: the key stops working; revoking again is a no-op.
	if code, _ := policyDo(t, ts.URL, admin, http.MethodDelete, "/Auth/Keys/"+key, ""); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := apiKeyDo(t, ts.URL, key, http.MethodGet, "/Users"); code != http.StatusUnauthorized {
		t.Errorf("revoked key: %d, want 401", code)
	}
	if code, _ := policyDo(t, ts.URL, admin, http.MethodDelete, "/Auth/Keys/"+key, ""); code != http.StatusNoContent {
		t.Errorf("second revoke: %d", code)
	}
}

func TestAPIKeyActingUserOnPlayedRoutes(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	admin := policyLogin(t, ts.URL, "admin", "adminpass")
	key := createKey(t, ts.URL, admin, "played")

	// playstate.user.R7: no userId -> 400.
	code, _ := apiKeyDo(t, ts.URL, key, http.MethodPost, "/UserPlayedItems/0123456789abcdef0123456789abcdef")
	if code != http.StatusBadRequest {
		t.Errorf("key without userId: %d, want 400", code)
	}
	// With a userId the key acts on that user (the item is unknown, so
	// 404 proves the user resolved).
	code, _ = apiKeyDo(t, ts.URL, key, http.MethodPost, "/UserPlayedItems/0123456789abcdef0123456789abcdef?userId="+string(alice.ID))
	if code != http.StatusNotFound {
		t.Errorf("key with userId: %d, want 404", code)
	}
}

func TestSocketAPIKeyUpgradeAndLegacyParamRefused(t *testing.T) {
	ts, st, _ := newSocketTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	admin := policyLogin(t, ts.URL, "admin", "adminpass")
	key := createKey(t, ts.URL, admin, "ws")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, ts.URL+"/socket?ApiKey="+key, nil)
	if err != nil {
		t.Fatalf("key upgrade: %v", err)
	}
	defer conn.CloseNow()
	if m := readSocketMessage(t, conn); m.MessageType != "ForceKeepAlive" {
		t.Errorf("first message %q", m.MessageType)
	}

	_, resp, err := websocket.Dial(ctx, ts.URL+"/socket?api_key="+key, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("legacy api_key param: err=%v resp=%v, want 403", err, resp)
	}
}

func TestSocketUserUpdatedOnPolicyChange(t *testing.T) {
	ts, st, _ := newSocketTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	alice := createTestUser(t, st, "alice", "pw")
	createTestUser(t, st, "bob", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")

	aliceConn := dialSocket(t, ts, loginToken(t, ts.URL, "alice", "pw"))
	_ = readSocketMessage(t, aliceConn)
	bobConn := dialSocket(t, ts, loginToken(t, ts.URL, "bob", "pw"))
	_ = readSocketMessage(t, bobConn)

	if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users/"+string(alice.ID)+"/Policy", `{`+providers+`,"IsHidden":true}`); code != http.StatusNoContent {
		t.Fatalf("policy: %d", code)
	}
	msg := readSocketMessage(t, aliceConn)
	data, _ := msg.Data.(map[string]any)
	pol, _ := data["Policy"].(map[string]any)
	if msg.MessageType != "UserUpdated" || data["Id"] != string(alice.ID) || pol["IsHidden"] != true {
		t.Fatalf("message = %#v", msg)
	}
	sendSocketMessage(t, bobConn, "KeepAlive", "")
	if m := readSocketMessage(t, bobConn); m.MessageType != "KeepAlive" {
		t.Errorf("bob received %q", m.MessageType)
	}

	// A configuration update announces nothing.
	if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Users/Configuration?userId="+string(alice.ID), `{}`); code != http.StatusNoContent {
		t.Fatalf("configuration: %d", code)
	}
	sendSocketMessage(t, aliceConn, "KeepAlive", "")
	if m := readSocketMessage(t, aliceConn); m.MessageType != "KeepAlive" {
		t.Errorf("alice received %q after a configuration update", m.MessageType)
	}
}

func TestSystemConfigurationQuickConnectToggle(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	createTestUser(t, st, "alice", "pw")
	admin := policyLogin(t, ts.URL, "admin", "adminpass")
	alice := policyLogin(t, ts.URL, "alice", "pw")

	if code, _ := policyDo(t, ts.URL, alice, http.MethodGet, "/System/Configuration", ""); code != http.StatusOK {
		t.Errorf("non-admin get: %d, want 200 (admin.config.R6)", code)
	}
	code, body := policyDo(t, ts.URL, admin, http.MethodGet, "/System/Configuration", "")
	var cfg map[string]any
	if code != 200 || json.Unmarshal(body, &cfg) != nil || cfg["QuickConnectAvailable"] != true {
		t.Fatalf("get: %d %s", code, body)
	}
	cfg["QuickConnectAvailable"] = false
	b, _ := json.Marshal(cfg)
	if code, _ := policyDo(t, ts.URL, admin, http.MethodPost, "/System/Configuration", string(b)); code != http.StatusNoContent {
		t.Fatalf("post: %d", code)
	}
	if code, body := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Enabled", "", ""); code != 200 || strings.TrimSpace(string(body)) != "false" {
		t.Errorf("enabled after toggle: %d %s", code, body)
	}
	if code, _ := policyDo(t, ts.URL, admin, http.MethodPost, "/System/Configuration", "{bad"); code != http.StatusBadRequest {
		t.Errorf("bad body: %d", code)
	}
	// Restore.
	if code, _ := policyDo(t, ts.URL, admin, http.MethodPost, "/System/Configuration", `{"QuickConnectAvailable":true}`); code != http.StatusNoContent {
		t.Fatalf("restore: %d", code)
	}
	if code, body := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Enabled", "", ""); code != 200 || strings.TrimSpace(string(body)) != "true" {
		t.Errorf("enabled after restore: %d %s", code, body)
	}
}
