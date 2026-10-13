package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAuthKeysListingShape covers users.keys.R1-R6.
func TestAuthKeysListingShape(t *testing.T) {
	ts, st := newTestServer(t)
	createTestAdminUser(t, st, "admin", "adminpass")
	createTestUser(t, st, "alice", "pw")
	adminTok := policyLogin(t, ts.URL, "admin", "adminpass")
	aliceTok := policyLogin(t, ts.URL, "alice", "pw")

	list := func() []map[string]any {
		t.Helper()
		code, body := policyDo(t, ts.URL, adminTok, http.MethodGet, "/Auth/Keys", "")
		if code != http.StatusOK {
			t.Fatalf("list: %d", code)
		}
		var res struct {
			Items            []map[string]any
			TotalRecordCount int
			StartIndex       int
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatal(err)
		}
		if res.TotalRecordCount != len(res.Items) || res.StartIndex != 0 {
			t.Errorf("counts: %+v", res)
		}
		return res.Items
	}
	if n := len(list()); n != 0 {
		t.Fatalf("fresh listing has %d items", n)
	}

	for _, q := range []string{"", "?app=", "?app=%20%20", "?foo=1"} {
		if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Auth/Keys"+q, ""); code != http.StatusBadRequest {
			t.Errorf("POST /Auth/Keys%s: %d, want 400", q, code)
		}
	}
	for _, name := range []string{" padded ", "a b", "x&y"} {
		if code, _ := policyDo(t, ts.URL, adminTok, http.MethodPost, "/Auth/Keys?App="+url.QueryEscape(name), ""); code != http.StatusNoContent {
			t.Fatalf("create %q: %d", name, code)
		}
	}
	items := list()
	if len(items) != 3 || items[0]["AppName"] != " padded " || items[2]["AppName"] != "x&y" {
		t.Fatalf("items: %v", items)
	}
	for _, it := range items {
		if v, ok := it["Id"].(float64); !ok || v != 0 {
			t.Errorf("Id = %v, want 0", it["Id"])
		}
		if it["IsActive"] != false {
			t.Errorf("IsActive = %v, want false", it["IsActive"])
		}
		if _, ok := it["DateRevoked"]; ok {
			t.Errorf("DateRevoked present: %v", it["DateRevoked"])
		}
		for _, k := range []string{"DeviceId", "AppVersion", "DeviceName"} {
			if it[k] != "" {
				t.Errorf("%s = %v, want empty", k, it[k])
			}
		}
		if it["UserId"] != "00000000000000000000000000000000" || it["DateLastActivity"] != "0001-01-01T00:00:00.0000000Z" {
			t.Errorf("UserId/DateLastActivity: %v %v", it["UserId"], it["DateLastActivity"])
		}
	}

	// Using a key changes nothing in the listing.
	key := items[0]["AccessToken"].(string)
	if code, _ := apiKeyDo(t, ts.URL, key, http.MethodGet, "/System/Info"); code != http.StatusOK {
		t.Fatalf("use key: %d", code)
	}
	if got := list()[0]; got["DateLastActivity"] != "0001-01-01T00:00:00.0000000Z" || got["IsActive"] != false {
		t.Errorf("after use: %v", got)
	}

	// Non-administrators are refused everywhere; nothing is revoked.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/Auth/Keys"}, {http.MethodPost, "/Auth/Keys?app=x"}, {http.MethodDelete, "/Auth/Keys/" + key},
	} {
		if code, _ := policyDo(t, ts.URL, aliceTok, c.method, c.path, ""); code != http.StatusForbidden {
			t.Errorf("%s %s as user: %d, want 403", c.method, c.path, code)
		}
		if code, _ := policyDo(t, ts.URL, "", c.method, c.path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: %d, want 401", c.method, c.path, code)
		}
	}

	// Revocation: the key leaves the listing; every other token is a no-op.
	del := func(tok string) int {
		code, _ := policyDo(t, ts.URL, adminTok, http.MethodDelete, "/Auth/Keys/"+tok, "")
		return code
	}
	for _, tok := range []string{strings.ToUpper(key), "0000", aliceTok, adminTok} {
		if code := del(tok); code != http.StatusNoContent {
			t.Errorf("DELETE %q: %d, want 204", tok, code)
		}
		if n := len(list()); n != 3 {
			t.Errorf("after DELETE %q the listing has %d keys", tok, n)
		}
	}
	if len(list()) != 3 {
		t.Errorf("a no-op revocation changed the listing")
	}
	if code, _ := policyDo(t, ts.URL, aliceTok, http.MethodGet, "/Users/Me", ""); code != http.StatusOK {
		t.Errorf("a user token was revoked through /Auth/Keys: %d", code)
	}
	if code := del(key); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if code := del(key); code != http.StatusNoContent {
		t.Errorf("revoke twice: %d", code)
	}
	if n := len(list()); n != 2 {
		t.Errorf("listing has %d keys after revoke, want 2", n)
	}
	if code, _ := apiKeyDo(t, ts.URL, key, http.MethodGet, "/System/Info"); code != http.StatusUnauthorized {
		t.Errorf("revoked key: %d, want 401", code)
	}
}
