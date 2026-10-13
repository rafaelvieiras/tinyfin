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
	srv := NewServer(st, Config{ServerName: "T", DataDir: dir})
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
