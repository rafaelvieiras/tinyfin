package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// doMe issues GET /Users/Me with the given headers and query string and
// returns the status code.
func doMe(t *testing.T, base string, headers map[string]string, query string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/Users/Me"+query, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /Users/Me: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestLegacyAuthChannelsNotHonored pins docs/00-decision-log.md D14 and
// docs/contract/README.md scope rule 6: only `Authorization: MediaBrowser
// ...` and the ApiKey query parameter carry credentials; the legacy
// channels behave as no credential at all.
func TestLegacyAuthChannelsNotHonored(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "alice", "hunter2")

	resp := authenticate(t, ts.URL, user.Name, "hunter2")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var ar authenticationResult
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		t.Fatalf("decoding auth result: %v", err)
	}
	tok := ar.AccessToken

	cases := []struct {
		name    string
		headers map[string]string
		query   string
		want    int
	}{
		{"modern header", map[string]string{"Authorization": `MediaBrowser Client="c", Device="d", DeviceId="x", Version="1", Token="` + tok + `"`}, "", http.StatusOK},
		{"ApiKey query", nil, "?ApiKey=" + tok, http.StatusOK},
		{"X-Emby-Authorization", map[string]string{"X-Emby-Authorization": `MediaBrowser Client="c", Device="d", DeviceId="x", Version="1", Token="` + tok + `"`}, "", http.StatusUnauthorized},
		{"X-Emby-Token", map[string]string{"X-Emby-Token": tok}, "", http.StatusUnauthorized},
		{"X-MediaBrowser-Token", map[string]string{"X-MediaBrowser-Token": tok}, "", http.StatusUnauthorized},
		{"api_key query", nil, "?api_key=" + tok, http.StatusUnauthorized},
		{"Emby scheme", map[string]string{"Authorization": `Emby Client="c", Device="d", DeviceId="x", Version="1", Token="` + tok + `"`}, "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := doMe(t, ts.URL, c.headers, c.query); got != c.want {
				t.Errorf("status = %d, want %d", got, c.want)
			}
		})
	}
}

// TestLoginRequiresAuthorizationHeader: client identification arriving only
// through X-Emby-Authorization is not usable, so login answers 400.
func TestLoginRequiresAuthorizationHeader(t *testing.T) {
	ts, st := newTestServer(t)
	createTestUser(t, st, "alice", "hunter2")

	body, _ := json.Marshal(authenticateUserByName{Username: "alice", Pw: "hunter2"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/Users/AuthenticateByName", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="c", Device="d", DeviceId="x", Version="1"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
