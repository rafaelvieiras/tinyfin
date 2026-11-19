package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

type forgotAnswer struct {
	Action            string
	PinFile           string
	PinExpirationDate string
}

func newForgotServer(t *testing.T) (*httptest.Server, *sqlite.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "tinyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := startTestServer(t, st, Config{ServerName: "T", DataDir: dir})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, dir
}

func TestForgotPasswordShape(t *testing.T) {
	ts, st, dir := newForgotServer(t)
	createTestUser(t, st, "alice", "pw")

	ask := func(name string) (forgotAnswer, []byte) {
		b, _ := json.Marshal(map[string]string{"EnteredUsername": name})
		code, body := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword", string(b), "")
		if code != http.StatusOK {
			t.Fatalf("forgot %q: %d", name, code)
		}
		var a forgotAnswer
		if err := json.Unmarshal(body, &a); err != nil {
			t.Fatal(err)
		}
		return a, body
	}
	known, raw := ask("ALICE")
	unknown, _ := ask("nobody-here")
	for _, a := range []forgotAnswer{known, unknown} {
		if a.Action != "PinCode" || a.PinFile == "" {
			t.Errorf("answer %+v", a)
		}
		exp, err := time.Parse(time.RFC3339, a.PinExpirationDate)
		if err != nil || time.Until(exp) < 25*time.Minute || time.Until(exp) > 31*time.Minute {
			t.Errorf("expiration %q (%v)", a.PinExpirationDate, err)
		}
		// The response must not disclose the server's path layout.
		if strings.ContainsAny(a.PinFile, `/\`) || strings.Contains(string(raw), dir) {
			t.Errorf("PinFile leaks a path: %q", a.PinFile)
		}
	}
	// users.forgot.R5: the account is untouched.
	if !loginOK(t, ts.URL, "alice", "pw") {
		t.Error("password changed by starting the flow")
	}
	for _, body := range []string{`{}`, `not json`, `{"EnteredUsername":""}`} {
		if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword", body, ""); code != http.StatusBadRequest {
			t.Errorf("body %q: %d, want 400", body, code)
		}
	}
}

func TestForgotPasswordPinRedeem(t *testing.T) {
	ts, st, dir := newForgotServer(t)
	createTestUser(t, st, "alice", "pw")
	tok := deviceLogin(t, ts.URL, "alice", "pw", "d1")

	rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword", `{"EnteredUsername":"alice"}`, "")
	files, _ := filepath.Glob(filepath.Join(dir, "passwordreset", "*"))
	if len(files) != 1 {
		t.Fatalf("expected one PIN file, got %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	var f struct{ Pin string }
	if err := json.Unmarshal(raw, &f); err != nil || f.Pin == "" {
		t.Fatalf("pin file %s: %v", raw, err)
	}

	for _, body := range []string{`{}`, `not json`} {
		if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", body, ""); code != http.StatusBadRequest {
			t.Errorf("pin body %q: %d, want 400", body, code)
		}
	}
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", `{"Pin":"ZZ-ZZ-ZZ-ZZ"}`, ""); code != http.StatusNotFound {
		t.Errorf("wrong pin: %d, want 404", code)
	}
	// Dashes are optional in the comparison.
	plain := strings.ReplaceAll(f.Pin, "-", "")
	b, _ := json.Marshal(map[string]string{"Pin": plain})
	code, body := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", string(b), "")
	if code != http.StatusOK {
		t.Fatalf("redeem: %d %s", code, body)
	}
	var res struct {
		Success    bool
		UsersReset []string
	}
	_ = json.Unmarshal(body, &res)
	if !res.Success || len(res.UsersReset) != 1 || res.UsersReset[0] != "alice" {
		t.Errorf("result %s", body)
	}
	if !loginOK(t, ts.URL, "alice", plain) {
		t.Error("password was not set to the PIN text")
	}
	if c, _ := policyDo(t, ts.URL, tok, http.MethodGet, "/Users/Me", ""); c != http.StatusOK {
		t.Errorf("existing token after redeem: %d, want 200", c)
	}
	// Consumed.
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", string(b), ""); code != http.StatusNotFound {
		t.Errorf("second redeem: %d, want 404", code)
	}
}

func TestForgotPasswordUnknownUserCreatesNoPin(t *testing.T) {
	ts, _, dir := newForgotServer(t)
	rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword", `{"EnteredUsername":"ghost"}`, "")
	files, _ := filepath.Glob(filepath.Join(dir, "passwordreset", "*"))
	if len(files) != 0 {
		t.Errorf("PIN file created for unknown user: %v", files)
	}
}

func TestPinThrottleUnit(t *testing.T) {
	th := newPinThrottle()
	now := time.Now()
	for i := 0; i < pinMaxFailuresPerSource; i++ {
		if !th.allow("10.0.0.1", now) {
			t.Fatalf("attempt %d refused too early", i)
		}
		th.fail("10.0.0.1", now)
	}
	if th.allow("10.0.0.1", now) {
		t.Error("source not locked out after the limit")
	}
	if !th.allow("10.0.0.2", now) {
		t.Error("another source must not be affected")
	}
	if !th.allow("10.0.0.1", now.Add(pinLockout+time.Second)) {
		t.Error("lockout must end")
	}
	// Pending-request budget: failures from any source add up and then
	// request invalidation of everything pending, once.
	th = newPinThrottle()
	invalidated := false
	for i := 0; i < pinMaxFailuresPerRequest; i++ {
		src := string(rune('a' + i))
		if th.fail(src, now) {
			invalidated = true
			if i != pinMaxFailuresPerRequest-1 {
				t.Errorf("invalidated too early at %d", i)
			}
		}
	}
	if !invalidated {
		t.Error("pending requests not invalidated after the budget")
	}
	if th.fail("z", now) {
		t.Error("budget must restart after an invalidation")
	}
}

func TestForgotPasswordPinThrottled(t *testing.T) {
	ts, st, dir := newForgotServer(t)
	createTestUser(t, st, "alice", "pw")
	rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword", `{"EnteredUsername":"alice"}`, "")
	files, _ := filepath.Glob(filepath.Join(dir, "passwordreset", "*"))
	if len(files) != 1 {
		t.Fatalf("pin files: %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	var f struct{ Pin string }
	_ = json.Unmarshal(raw, &f)

	for i := 0; i < pinMaxFailuresPerRequest; i++ {
		if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", `{"Pin":"00-00-00-0`+string(rune('0'+i))+`"}`, ""); code != http.StatusNotFound {
			t.Fatalf("wrong pin %d: %d, want 404", i, code)
		}
	}
	// The pending request is gone, so even the right PIN now fails with the
	// same uniform answer.
	if left, _ := filepath.Glob(filepath.Join(dir, "passwordreset", "*")); len(left) != 0 {
		t.Errorf("pending request survived the failures: %v", left)
	}
	b, _ := json.Marshal(map[string]string{"Pin": f.Pin})
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", string(b), ""); code != http.StatusNotFound {
		t.Errorf("right pin after invalidation: %d, want 404", code)
	}
	if !loginOK(t, ts.URL, "alice", "pw") {
		t.Error("password changed by a refused redeem")
	}
}

func TestForgotPasswordPinSourceLockout(t *testing.T) {
	ts, st, dir := newForgotServer(t)
	createTestUser(t, st, "alice", "pw")
	// Failures with no pending request still count per source: a client
	// must not be able to probe freely and wait for a request to appear.
	for i := 0; i < pinMaxFailuresPerSource; i++ {
		rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", `{"Pin":"11-11-11-11"}`, "")
	}
	rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword", `{"EnteredUsername":"alice"}`, "")
	files, _ := filepath.Glob(filepath.Join(dir, "passwordreset", "*"))
	raw, _ := os.ReadFile(files[0])
	var f struct{ Pin string }
	_ = json.Unmarshal(raw, &f)
	b, _ := json.Marshal(map[string]string{"Pin": f.Pin})
	if code, _ := rawDo(t, ts.URL, http.MethodPost, "/Users/ForgotPassword/Pin", string(b), ""); code != http.StatusNotFound {
		t.Errorf("locked-out source redeemed: %d, want 404", code)
	}
}
