package api

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
)

// TestSystemEndpointRequiresAuth regression-guards that GET /System/Endpoint
// needs a valid token — confirmed against a real Jellyfin server (staging,
// :8097): a request with no token comes back 401, unlike GET
// /System/Info/Public.
func TestSystemEndpointRequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/System/Endpoint")
	if err != nil {
		t.Fatalf("GET /System/Endpoint: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestSystemEndpointLoopback regression-guards the shape and the
// loopback-is-local rule — confirmed against a real Jellyfin server
// (staging, :8097): a request whose TCP peer is 127.0.0.1 comes back
// {"IsLocal":true,"IsInNetwork":true}. httptest.Server listens on
// 127.0.0.1, so a plain http.Client request against it already exercises
// exactly this path — no header spoofing needed (and X-Forwarded-For is
// confirmed ignored by the real server anyway, see handleSystemEndpoint's
// doc comment).
func TestSystemEndpointLoopback(t *testing.T) {
	ts, st := newTestServer(t)
	user := createTestUser(t, st, "dave", "davepass")
	token := loginToken(t, ts.URL, user.Name, "davepass")
	get := authedClient(t, ts.URL, token)

	resp := get(http.MethodGet, "/System/Endpoint")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got endpointInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.IsLocal {
		t.Errorf("IsLocal = false, want true (httptest.Server's peer is 127.0.0.1)")
	}
	if !got.IsInNetwork {
		t.Errorf("IsInNetwork = false, want true")
	}
}

// TestRemoteIPPrivateRanges unit-tests remoteIP/the private-range
// classification handleSystemEndpoint builds IsInNetwork from, independent
// of what httptest.Server happens to bind to.
func TestRemoteIPPrivateRanges(t *testing.T) {
	for _, tc := range []struct {
		remoteAddr string
		wantIP     string
	}{
		{"127.0.0.1:54321", "127.0.0.1"},
		{"[::1]:54321", "::1"},
		{"172.20.0.5:8096", "172.20.0.5"},
		{"not-an-address", ""},
	} {
		req, err := http.NewRequest(http.MethodGet, "http://example.invalid/System/Endpoint", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.RemoteAddr = tc.remoteAddr
		ip := remoteIP(req)
		got := ""
		if ip != nil {
			got = ip.String()
		}
		if got != tc.wantIP {
			t.Errorf("remoteIP(%q) = %q, want %q", tc.remoteAddr, got, tc.wantIP)
		}
	}

	loopback := net.ParseIP("127.0.0.1")
	if !loopback.IsLoopback() {
		t.Fatal("sanity check: 127.0.0.1 must be IsLoopback")
	}
	private := net.ParseIP("172.20.0.5")
	if !private.IsPrivate() {
		t.Fatal("sanity check: 172.20.0.5 (Docker's default bridge range) must be IsPrivate")
	}
	public := net.ParseIP("8.8.8.8")
	if public.IsPrivate() || public.IsLoopback() {
		t.Fatal("sanity check: 8.8.8.8 must be neither IsPrivate nor IsLoopback")
	}
}
