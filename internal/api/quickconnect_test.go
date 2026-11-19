package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

func authHeader(device, token string) string {
	h := `MediaBrowser Client="Test", Device="Test", DeviceId="` + device + `", Version="1.0.0"`
	if token != "" {
		h += `, Token="` + token + `"`
	}
	return h
}

// rawDo sends a request with an explicit Authorization header (or none).
func rawDo(t *testing.T, base, method, path, body, auth string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func deviceLogin(t *testing.T, base, name, pw, device string) string {
	t.Helper()
	b, _ := json.Marshal(authenticateUserByName{Username: name, Pw: pw})
	code, body := rawDo(t, base, http.MethodPost, "/Users/AuthenticateByName", string(b), authHeader(device, ""))
	if code != http.StatusOK {
		t.Fatalf("login %s on %s: status %d", name, device, code)
	}
	var res authenticationResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	return res.AccessToken
}

type qcInit struct {
	Secret string
	Code   string
}

func qcInitiate(t *testing.T, base, device string) qcInit {
	t.Helper()
	code, body := rawDo(t, base, http.MethodPost, "/QuickConnect/Initiate", "", authHeader(device, ""))
	if code != http.StatusOK {
		t.Fatalf("initiate: status %d", code)
	}
	var r qcInit
	if err := json.Unmarshal(body, &r); err != nil || r.Secret == "" || r.Code == "" {
		t.Fatalf("initiate body %s: %v", body, err)
	}
	return r
}

func qcExchange(t *testing.T, base, secret string) (int, authenticationResult) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"Secret": secret})
	code, body := rawDo(t, base, http.MethodPost, "/Users/AuthenticateWithQuickConnect", string(b), "")
	var res authenticationResult
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatal(err)
		}
	}
	return code, res
}

func TestQuickConnectFlow(t *testing.T) {
	ts, st := newTestServer(t)
	alice := createTestUser(t, st, "alice", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")

	if code, body := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Enabled", "", ""); code != 200 || string(bytes.TrimSpace(body)) != "true" {
		t.Fatalf("enabled: %d %s", code, body)
	}
	phone := deviceLogin(t, ts.URL, "alice", "pw", "phone")
	oldTV := deviceLogin(t, ts.URL, "alice", "pw", "tv")
	qc := qcInitiate(t, ts.URL, "tv")

	// R2: not authorized yet.
	if code, _ := qcExchange(t, ts.URL, qc.Secret); code != http.StatusNotFound {
		t.Errorf("exchange before authorize: %d, want 404", code)
	}
	// Polling endpoint.
	if code, body := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect?secret="+qc.Secret, "", ""); code != 200 {
		t.Errorf("connect: %d %s", code, body)
	}
	// Authorize needs a token and a known code.
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code, "", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous authorize: %d, want 401", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code=000000x", "", authHeader("phone", phone)); code != http.StatusNotFound {
		t.Errorf("unknown code: %d, want 404", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code, "", authHeader("phone", phone)); code != http.StatusOK {
		t.Fatalf("authorize: %d", code)
	}

	code, res := qcExchange(t, ts.URL, qc.Secret)
	if code != http.StatusOK {
		t.Fatalf("exchange: %d", code)
	}
	if res.User.ID != string(alice.ID) || res.SessionInfo.DeviceID != "tv" || len(res.AccessToken) != 32 {
		t.Errorf("unexpected result: user %s device %s token len %d", res.User.ID, res.SessionInfo.DeviceID, len(res.AccessToken))
	}
	if c, _ := policyDo(t, ts.URL, res.AccessToken, http.MethodGet, "/Users/Me", ""); c != http.StatusOK {
		t.Errorf("issued token: %d, want 200", c)
	}
	// R6: the device's earlier token is revoked, the phone is not.
	if c, _ := policyDo(t, ts.URL, oldTV, http.MethodGet, "/Users/Me", ""); c != http.StatusUnauthorized {
		t.Errorf("old device token: %d, want 401", c)
	}
	if c, _ := policyDo(t, ts.URL, phone, http.MethodGet, "/Users/Me", ""); c != http.StatusOK {
		t.Errorf("phone token: %d, want 200", c)
	}
	// R7: single use.
	if code, _ := qcExchange(t, ts.URL, qc.Secret); code != http.StatusNotFound {
		t.Errorf("second exchange: %d, want 404", code)
	}
}

// TestQuickConnectExchangeAppendsActivityLogEntries: the exchange writes
// the same two login entries as a password login (users.auth.R23-R25),
// with the device name given at Initiate and an empty address.
func TestQuickConnectExchangeAppendsActivityLogEntries(t *testing.T) {
	ts, st := newTestServer(t)
	alice := createTestUser(t, st, "alice", "pw")
	phone := deviceLogin(t, ts.URL, "alice", "pw", "phone")
	qc := qcInitiate(t, ts.URL, "tv")
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code, "", authHeader("phone", phone)); code != http.StatusOK {
		t.Fatalf("authorize: %d", code)
	}
	if code, _ := qcExchange(t, ts.URL, qc.Secret); code != http.StatusOK {
		t.Fatalf("exchange: %d", code)
	}
	entries, _, err := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 2})
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %d, err %v, want 2", len(entries), err)
	}
	want := []struct {
		typ  storage.ActivityType
		name string
	}{
		{storage.ActivityTypeAuthenticationSucceeded, "alice successfully authenticated"},
		{storage.ActivityTypeSessionStarted, "alice is online from Test"},
	}
	for i, w := range want {
		e := entries[i]
		if e.Type != w.typ || e.Name != w.name {
			t.Errorf("entries[%d] = %q %q, want %q %q", i, e.Type, e.Name, w.typ, w.name)
		}
		if e.ShortOverview != "IP address: " {
			t.Errorf("entries[%d].ShortOverview = %q, want %q", i, e.ShortOverview, "IP address: ")
		}
		if e.UserID == nil || *e.UserID != alice.ID {
			t.Errorf("entries[%d].UserID = %v, want %q", i, e.UserID, alice.ID)
		}
	}
}

func TestQuickConnectValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, body := range []string{`{}`, `not json`, `{"Secret":""}`} {
		if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/AuthenticateWithQuickConnect", body, ""); code != http.StatusBadRequest {
			t.Errorf("body %q: %d, want 400", body, code)
		}
	}
	if code, _ := qcExchange(t, ts.URL, "0000000000000000000000000000000000000000000000000000000000000000"); code != http.StatusNotFound {
		t.Errorf("unknown secret: %d, want 404", code)
	}
	// Initiate needs the client identification header.
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Initiate", "", ""); code != http.StatusBadRequest {
		t.Errorf("initiate without header: %d, want 400", code)
	}
}

func TestQuickConnectAuthorizeOtherUser(t *testing.T) {
	ts, st := newTestServer(t)
	alice := createTestUser(t, st, "alice", "pw")
	bob := createTestUser(t, st, "bob", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	admin := deviceLogin(t, ts.URL, "admin", "adminpw", "adm")
	aliceTok := deviceLogin(t, ts.URL, "alice", "pw", "phone")

	// R5: a non-administrator naming another user is refused.
	qc := qcInitiate(t, ts.URL, "tv")
	path := "/QuickConnect/Authorize?code=" + qc.Code + "&userId=" + string(bob.ID)
	if code, _ := rawDo(t, ts.URL, http.MethodPost, path, "", authHeader("phone", aliceTok)); code != http.StatusForbidden {
		t.Errorf("non-admin naming another: %d, want 403", code)
	}
	// Naming themselves is fine for anyone.
	self := "/QuickConnect/Authorize?code=" + qc.Code + "&userId=" + string(alice.ID)
	if code, _ := rawDo(t, ts.URL, http.MethodPost, self, "", authHeader("phone", aliceTok)); code != http.StatusOK {
		t.Errorf("non-admin naming self: %d, want 200", code)
	}
	// An administrator authorizes on behalf of another user.
	qc2 := qcInitiate(t, ts.URL, "tv2")
	path = "/QuickConnect/Authorize?code=" + qc2.Code + "&userId=" + string(bob.ID)
	if code, _ := rawDo(t, ts.URL, http.MethodPost, path, "", authHeader("adm", admin)); code != http.StatusOK {
		t.Fatalf("admin authorize: %d", code)
	}
	code, res := qcExchange(t, ts.URL, qc2.Secret)
	if code != http.StatusOK || res.User.ID != string(bob.ID) {
		t.Errorf("exchange: %d user %s, want 200 %s", code, res.User.ID, bob.ID)
	}
}

func TestQuickConnectDisabledAccountRefused(t *testing.T) {
	ts, st := newTestServer(t)
	alice := createTestUser(t, st, "alice", "pw")
	phone := deviceLogin(t, ts.URL, "alice", "pw", "phone")
	qc := qcInitiate(t, ts.URL, "tv")
	rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code, "", authHeader("phone", phone))
	alice.IsDisabled = true
	if err := st.UpdateUser(t.Context(), alice); err != nil {
		t.Fatal(err)
	}
	if code, _ := qcExchange(t, ts.URL, qc.Secret); code != http.StatusForbidden {
		t.Errorf("disabled account exchange: %d, want 403", code)
	}
}

func TestQuickConnectDisabled(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(startTestServer(t, st, Config{ServerName: "T", QuickConnectDisabled: true}).Handler())
	t.Cleanup(ts.Close)

	if code, body := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Enabled", "", ""); code != 200 || string(bytes.TrimSpace(body)) != "false" {
		t.Errorf("enabled: %d %s", code, body)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Initiate", "", authHeader("tv", "")); code != http.StatusUnauthorized {
		t.Errorf("initiate: %d, want 401", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/AuthenticateWithQuickConnect", `{"Secret":"x"}`, ""); code != http.StatusUnauthorized {
		t.Errorf("exchange: %d, want 401", code)
	}
}

// quickconnect.connect.R2 / authorize.R3 / authorize.R6-R7 / disabled.R1:
// parameter validation comes before everything but authentication.
func TestQuickConnectParameterValidation(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpw")
	admin := deviceLogin(t, ts.URL, "admin", "adminpw", "adm")
	auth := authHeader("adm", admin)
	qc := qcInitiate(t, ts.URL, "tv")

	if code, _ := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect", "", ""); code != http.StatusBadRequest {
		t.Errorf("connect without secret: %d, want 400", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect?secret="+qc.Secret+"x", "", ""); code != http.StatusNotFound {
		t.Errorf("connect unknown secret: %d, want 404", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize", "", auth); code != http.StatusBadRequest {
		t.Errorf("authorize without code: %d, want 400", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code+"&userId=zz", "", auth); code != http.StatusBadRequest {
		t.Errorf("authorize junk userId: %d, want 400", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code+"&userId=12345678123412341234123456789abc", "", auth); code != http.StatusBadRequest {
		t.Errorf("authorize unknown userId: %d, want 400", code)
	}
	var r struct{ Authenticated bool }
	_, body := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect?secret="+qc.Secret, "", "")
	_ = json.Unmarshal(body, &r)
	if r.Authenticated {
		t.Error("a refused authorize must not authorize")
	}
	// The empty id counts as "not given".
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code+"&userId=00000000000000000000000000000000", "", auth); code != http.StatusOK {
		t.Errorf("authorize with the empty id: %d, want 200", code)
	}
}

func TestQuickConnectDisabledValidationFirst(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	createTestAdminUser(t, st, "admin", "adminpw")
	ts := httptest.NewServer(startTestServer(t, st, Config{ServerName: "T", QuickConnectDisabled: true}).Handler())
	t.Cleanup(ts.Close)
	admin := deviceLogin(t, ts.URL, "admin", "adminpw", "adm")
	auth := authHeader("adm", admin)
	if code, _ := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect?secret=abc", "", ""); code != http.StatusUnauthorized {
		t.Errorf("connect: %d, want 401", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect", "", ""); code != http.StatusBadRequest {
		t.Errorf("connect without secret: %d, want 400", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code=000000", "", auth); code != http.StatusUnauthorized {
		t.Errorf("authorize: %d, want 401", code)
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize", "", auth); code != http.StatusBadRequest {
		t.Errorf("authorize without code: %d, want 400", code)
	}
}

// quickconnect.authorize.R8: a code that is already authorized is refused
// with a 500 (as on the reference); the first authorization stands.
func TestQuickConnectReauthorizeRefused(t *testing.T) {
	ts, st := newTestServer(t)
	alice := createTestUser(t, st, "alice", "pw")
	createTestAdminUser(t, st, "admin", "adminpw")
	phone := deviceLogin(t, ts.URL, "alice", "pw", "phone")
	adminTok := deviceLogin(t, ts.URL, "admin", "adminpw", "adminphone")
	qc := qcInitiate(t, ts.URL, "tv")

	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code, "", authHeader("phone", phone)); code != http.StatusOK {
		t.Fatalf("first authorize: %d", code)
	}
	for _, tok := range []struct{ device, token string }{{"phone", phone}, {"adminphone", adminTok}} {
		code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+qc.Code, "", authHeader(tok.device, tok.token))
		if code != http.StatusInternalServerError {
			t.Errorf("re-authorize with %s: %d, want 500", tok.device, code)
		}
	}
	// Still authorized, still for the first user.
	code, res := qcExchange(t, ts.URL, qc.Secret)
	if code != http.StatusOK || res.User.ID != string(alice.ID) {
		t.Errorf("exchange after refused re-authorize: %d user %s, want 200 for alice", code, res.User.ID)
	}
}

// quickconnect.connect.R3: authorizing moves DateAdded forward (one minute
// past the authorization); polling alone leaves it untouched.
func TestQuickConnectDateAddedAdvancesOnAuthorize(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "pw")
	phone := deviceLogin(t, ts.URL, "alice", "pw", "phone")
	_, body := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Initiate", "", authHeader("tv", ""))
	var init quickConnectResult
	if err := json.Unmarshal(body, &init); err != nil {
		t.Fatal(err)
	}
	poll := func() quickConnectResult {
		_, b := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect?secret="+init.Secret, "", "")
		var r quickConnectResult
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := poll().DateAdded; got != init.DateAdded {
		t.Errorf("pending poll DateAdded %s, want unchanged %s", got, init.DateAdded)
	}
	before := time.Now()
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+init.Code, "", authHeader("phone", phone)); code != http.StatusOK {
		t.Fatalf("authorize: %d", code)
	}
	got, err := time.Parse(time.RFC3339Nano, poll().DateAdded)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Sub(before); d < 59*time.Second || d > 62*time.Second {
		t.Errorf("DateAdded after authorize is %v after the authorization, want about 1 minute", d)
	}
}

// quickconnect.expiry.R1/R2: a pending request is gone ten minutes after
// Initiate; an authorized one is visible to Connect until ten minutes after
// its (advanced) DateAdded but can be exchanged for ten minutes after the
// authorization only, whenever it was initiated.
func TestQuickConnectLifetimes(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := startTestServer(t, st, Config{ServerName: "TestServer"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	createTestUser(t, st, "alice", "pw")
	phone := deviceLogin(t, ts.URL, "alice", "pw", "phone")

	// The server under test shares the process: advance its clock.
	var offset time.Duration
	var mu sync.Mutex
	srv.quick.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Now().Add(offset)
	}
	advance := func(d time.Duration) { mu.Lock(); offset += d; mu.Unlock() }

	pending := qcInitiate(t, ts.URL, "tv-pending")
	early := qcInitiate(t, ts.URL, "tv-early")
	late := qcInitiate(t, ts.URL, "tv-late")
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+early.Code, "", authHeader("phone", phone)); code != 200 {
		t.Fatalf("authorize early: %d", code)
	}
	advance(5 * time.Minute)
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+late.Code, "", authHeader("phone", phone)); code != 200 {
		t.Fatalf("authorize late: %d", code)
	}
	advance(5*time.Minute + 15*time.Second) // 10 min 15 s after Initiate

	connect := func(secret string) int {
		c, _ := rawDo(t, ts.URL, http.MethodGet, "/QuickConnect/Connect?secret="+secret, "", "")
		return c
	}
	if c := connect(pending.Secret); c != http.StatusNotFound {
		t.Errorf("pending after 10 min: connect %d, want 404", c)
	}
	if c, _ := rawDo(t, ts.URL, http.MethodPost, "/QuickConnect/Authorize?code="+pending.Code, "", authHeader("phone", phone)); c != http.StatusNotFound {
		t.Errorf("pending after 10 min: authorize %d, want 404", c)
	}
	if c := connect(early.Secret); c != http.StatusOK {
		t.Errorf("authorized 10 min 15 s ago: connect %d, want 200 (visible until 11 min)", c)
	}
	if c, _ := qcExchange(t, ts.URL, early.Secret); c != http.StatusNotFound {
		t.Errorf("authorized 10 min 15 s ago: exchange %d, want 404", c)
	}
	if c, res := qcExchange(t, ts.URL, late.Secret); c != http.StatusOK || res.AccessToken == "" {
		t.Errorf("authorized 5 min 15 s ago: exchange %d, want 200", c)
	}
	advance(time.Minute)
	if c := connect(early.Secret); c != http.StatusNotFound {
		t.Errorf("authorized 11 min 15 s ago: connect %d, want 404", c)
	}
}
