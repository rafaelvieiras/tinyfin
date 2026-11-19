package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// fromAddr serves one request through srv's handler as if its TCP peer were
// addr (httptest.Server can only produce loopback peers).
func fromAddr(srv *Server, addr, method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = addr + ":40000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", `MediaBrowser Client="t", Device="t", DeviceId="d-`+addr+`", Version="1"`)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestLocalNetworkSubnets covers users.auth.R21 with the configured local
// network: with no subnets the private ranges are local; once subnets are
// set only they (and loopback) are.
func TestLocalNetworkSubnets(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := startTestServer(t, st, Config{ServerName: "T"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	createTestAdminUser(t, st, "root", "pw")
	u := createTestUser(t, st, "homebody", "pw")
	u.Policy = `{"EnableRemoteAccess":false,"EnableAllDevices":true}`
	if err := st.UpdateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	tok := loginToken(t, ts.URL, "root", "pw")
	login := func(addr string) int {
		code, _ := fromAddr(srv, addr, http.MethodPost, "/Users/AuthenticateByName", `{"Username":"homebody","Pw":"pw"}`)
		return code
	}

	if c := login("172.17.0.1"); c != http.StatusOK {
		t.Fatalf("private peer, default config: %d, want 200", c)
	}
	if c := login("8.8.8.8"); c != http.StatusForbidden {
		t.Fatalf("public peer: %d, want 403", c)
	}

	// Non-admins cannot read or write the section.
	if c, _ := rawDo(t, ts.URL, http.MethodGet, "/System/Configuration/network", "", ""); c != http.StatusUnauthorized {
		t.Errorf("anonymous GET: %d, want 401", c)
	}
	auth := `MediaBrowser Token="` + tok + `"`
	code, body := rawDo(t, ts.URL, http.MethodGet, "/System/Configuration/network", "", auth)
	if code != http.StatusOK {
		t.Fatalf("GET network: %d %s", code, body)
	}
	var got struct{ LocalNetworkSubnets []string }
	if json.Unmarshal(body, &got) != nil || got.LocalNetworkSubnets == nil || len(got.LocalNetworkSubnets) != 0 {
		t.Fatalf("default network config %s", body)
	}
	if c, _ := rawDo(t, ts.URL, http.MethodPost, "/System/Configuration/network", `{"LocalNetworkSubnets":["not-a-subnet"]}`, auth); c != http.StatusBadRequest {
		t.Errorf("invalid subnet: %d, want 400", c)
	}
	if c, b := rawDo(t, ts.URL, http.MethodPost, "/System/Configuration/network", `{"LocalNetworkSubnets":["192.0.2.0/24","10.1.2.3"]}`, auth); c != http.StatusNoContent {
		t.Fatalf("POST network: %d %s", c, b)
	}
	if c := login("172.17.0.1"); c != http.StatusForbidden {
		t.Errorf("private peer outside the configured subnets: %d, want 403", c)
	}
	for _, addr := range []string{"192.0.2.77", "10.1.2.3", "127.0.0.1"} {
		if c := login(addr); c != http.StatusOK {
			t.Errorf("peer %s inside the local network: %d, want 200", addr, c)
		}
	}
	if srv.isLocalAddress(parseIP(t, "172.17.0.1")) || !srv.isLocalAddress(parseIP(t, "192.0.2.1")) {
		t.Error("isLocalAddress disagrees with the configured subnets")
	}

	// Persisted: a restarted server over the same store keeps the subnets.
	srv2 := startTestServer(t, st, Config{ServerName: "T"})
	if srv2.isLocalAddress(parseIP(t, "172.17.0.1")) {
		t.Error("subnets not persisted across restart")
	}
	// Clearing restores the private ranges.
	if c, _ := rawDo(t, ts.URL, http.MethodPost, "/System/Configuration/network", `{"LocalNetworkSubnets":[]}`, auth); c != http.StatusNoContent {
		t.Fatalf("clear: %d", c)
	}
	if c := login("172.17.0.1"); c != http.StatusOK {
		t.Errorf("after clearing: %d, want 200", c)
	}
}

func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad ip %q", s)
	}
	return ip
}

// TestNetworkRemoteAccessGate covers wire.network.R1: with the network
// EnableRemoteAccess off, a caller outside the local network gets 503 on
// every route but the web client's static files; local callers are served.
func TestNetworkRemoteAccessGate(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := startTestServer(t, st, Config{ServerName: "T"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	createTestAdminUser(t, st, "root", "pw")
	tok := loginToken(t, ts.URL, "root", "pw")
	auth := `MediaBrowser Token="` + tok + `"`

	if c, b := rawDo(t, ts.URL, http.MethodPost, "/System/Configuration/network", `{"EnableRemoteAccess":false,"LocalNetworkSubnets":["192.0.2.0/24"]}`, auth); c != http.StatusNoContent {
		t.Fatalf("POST network: %d %s", c, b)
	}
	if c, _ := fromAddr(srv, "8.8.8.8", http.MethodGet, "/System/Info/Public", ""); c != http.StatusServiceUnavailable {
		t.Errorf("remote /System/Info/Public: %d, want 503", c)
	}
	if c, _ := fromAddr(srv, "172.17.0.1", http.MethodGet, "/Users/Public", ""); c != http.StatusServiceUnavailable {
		t.Errorf("remote (private, outside subnets) /Users/Public: %d, want 503", c)
	}
	if c, _ := fromAddr(srv, "192.0.2.7", http.MethodGet, "/System/Info/Public", ""); c != http.StatusOK {
		t.Errorf("local /System/Info/Public: %d, want 200", c)
	}
	if c, _ := fromAddr(srv, "127.0.0.1", http.MethodGet, "/System/Info/Public", ""); c != http.StatusOK {
		t.Errorf("loopback /System/Info/Public: %d, want 200", c)
	}
	if c, _ := fromAddr(srv, "8.8.8.8", http.MethodGet, "/web/index.html", ""); c == http.StatusServiceUnavailable {
		t.Errorf("remote /web/index.html is gated: %d", c)
	}
	// Turning it back on (from a local caller) reopens the server.
	if c, b := rawDo(t, ts.URL, http.MethodPost, "/System/Configuration/network", `{"EnableRemoteAccess":true}`, auth); c != http.StatusNoContent {
		t.Fatalf("POST network: %d %s", c, b)
	}
	if c, _ := fromAddr(srv, "8.8.8.8", http.MethodGet, "/System/Info/Public", ""); c != http.StatusOK {
		t.Errorf("remote after re-enable: %d, want 200", c)
	}
}
