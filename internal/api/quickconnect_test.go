package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
	ts := httptest.NewServer(NewServer(st, Config{ServerName: "T", QuickConnectDisabled: true}).Handler())
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
