package api

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// This file covers client discovery (platform.autodiscovery, docs/16): the
// UDP responder that lets a Jellyfin client find this server without the user
// typing an address. It is a socket-level protocol, so the conformance runner
// (an HTTP client) cannot assert it: these tests are the coverage, and the
// spec's rules carry no scenario (docs/contract/specs/platform.md).

const testServerID = "0123456789abcdef0123456789abcdef"

// newDiscoveryTestServer starts a server whose discovery responder is bound to
// an ephemeral loopback port and waits until it is listening.
func newDiscoveryTestServer(t *testing.T) (*Server, *sqlite.Store, string) {
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
	srv := startTestServer(t, st, Config{
		ServerName:       "TestServer",
		ServerID:         testServerID,
		DiscoveryUDPAddr: "127.0.0.1:0",
	})
	deadline := time.Now().Add(3 * time.Second)
	for srv.DiscoveryAddr() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.DiscoveryAddr() == "" {
		t.Fatal("discovery responder never bound its UDP port")
	}
	return srv, st, srv.DiscoveryAddr()
}

// askDiscovery sends payload to addr and returns the reply, or nil when the
// server stayed silent until the deadline.
func askDiscovery(t *testing.T, addr, payload string, wait time.Duration) []byte {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil
		}
		t.Fatalf("read: %v", err)
	}
	return buf[:n]
}

// TestDiscoveryAnswersTheRequest is platform.discovery.R1/R2: the documented
// request is answered with the four members the official client SDK reads, the
// server's own Id and name, and EndpointAddress explicitly null.
func TestDiscoveryAnswersTheRequest(t *testing.T) {
	srv, _, addr := newDiscoveryTestServer(t)

	reply := askDiscovery(t, addr, "who is JellyfinServer?", 2*time.Second)
	if reply == nil {
		t.Fatal("no reply to the discovery request")
	}
	var got map[string]any
	if err := json.Unmarshal(reply, &got); err != nil {
		t.Fatalf("reply is not JSON: %v (%q)", err, reply)
	}
	if got["Id"] != testServerID {
		t.Errorf("Id = %v, want %s", got["Id"], testServerID)
	}
	if got["Name"] != "TestServer" {
		t.Errorf("Name = %v, want TestServer", got["Name"])
	}
	endpoint, ok := got["EndpointAddress"]
	if !ok {
		t.Error("EndpointAddress is missing; the reference always sends it, null")
	} else if endpoint != nil {
		t.Errorf("EndpointAddress = %v, want null", endpoint)
	}
	address, _ := got["Address"].(string)
	u, err := url.Parse(address)
	if err != nil {
		t.Fatalf("Address %q is not a URL: %v", address, err)
	}
	if u.Scheme != "http" {
		t.Errorf("Address scheme = %q, want http", u.Scheme)
	}
	if got := u.Port(); got != "8096" {
		t.Errorf("Address port = %q, want the default InternalHttpPort 8096", got)
	}
	// The advertised host must be one of this machine's own addresses, not a
	// container-internal one and not the port the test bound.
	if ip := net.ParseIP(u.Hostname()); ip == nil || ip.IsLoopback() {
		t.Errorf("Address host = %q, want a non-loopback address of this host", u.Hostname())
	}
	_ = srv
}

// TestDiscoveryRequestMatching is platform.discovery.R1: surrounding
// whitespace and case are accepted, everything else is not — including the
// Emby spelling, which the reference does not answer either.
func TestDiscoveryRequestMatching(t *testing.T) {
	accepted := []string{
		"who is JellyfinServer?",
		"who is JellyfinServer?\n",
		"  who is JellyfinServer?  ",
		"WHO IS JELLYFINSERVER?",
	}
	for _, payload := range accepted {
		if !discoveryRequestMatches(payload) {
			t.Errorf("%q should be accepted", payload)
		}
	}
	rejected := []string{
		"",
		"who is EmbyServer?",
		"hello",
		"who is JellyfinServer",
		"who is JellyfinServer?? ",
		"{\"who is JellyfinServer?\"}",
	}
	for _, payload := range rejected {
		if discoveryRequestMatches(payload) {
			t.Errorf("%q should be ignored", payload)
		}
	}
}

// TestDiscoveryIgnoresUnknownPayloads is the socket half of the rule above:
// the responder must stay silent rather than answer something it does not
// recognize.
func TestDiscoveryIgnoresUnknownPayloads(t *testing.T) {
	_, _, addr := newDiscoveryTestServer(t)
	for _, payload := range []string{"who is EmbyServer?", "hello", ""} {
		if reply := askDiscovery(t, addr, payload, 300*time.Millisecond); reply != nil {
			t.Errorf("answered %q with %q", payload, reply)
		}
	}
}

// TestDiscoveryIsGatedByAutoDiscovery is platform.discovery.R3: turning the
// Networking page's switch off stops the answers without a restart, and
// turning it back on resumes them.
func TestDiscoveryIsGatedByAutoDiscovery(t *testing.T) {
	srv, st, addr := newDiscoveryTestServer(t)

	setNetworkSection(t, st, networkConfigurationDto{
		InternalHttpPort: 8096, PublicHttpPort: 8096, PublicHttpsPort: 8920,
		EnableIPv4: true, EnableRemoteAccess: true,
		AutoDiscovery: false, IgnoreVirtualInterfaces: true,
		VirtualInterfaceNames: []string{"veth"},
	})
	if reply := askDiscovery(t, addr, "who is JellyfinServer?", 300*time.Millisecond); reply != nil {
		t.Fatalf("AutoDiscovery=false still answered: %q", reply)
	}

	enable := srv.networkConfiguration(context.Background())
	enable.AutoDiscovery = true
	setNetworkSection(t, st, enable)
	if reply := askDiscovery(t, addr, "who is JellyfinServer?", 2*time.Second); reply == nil {
		t.Fatal("AutoDiscovery=true did not answer")
	}
}

// TestDiscoveryAnnouncesTheListeningPort is DIV-38: the reply carries the port
// this server actually serves HTTP on, so a deployment whose port was set on
// the command line (not in the dashboard) is still reachable, where the
// reference would announce its own setting.
func TestDiscoveryAnnouncesTheListeningPort(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := startTestServer(t, st, Config{
		ServerName:       "TestServer",
		ServerID:         testServerID,
		ListenPort:       9123,
		DiscoveryUDPAddr: "127.0.0.1:0",
	})
	deadline := time.Now().Add(3 * time.Second)
	for srv.DiscoveryAddr() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	setNetworkSection(t, st, networkConfigurationDto{InternalHttpPort: 8096, EnableIPv4: true, AutoDiscovery: true})
	reply := askDiscovery(t, srv.DiscoveryAddr(), "who is JellyfinServer?", 2*time.Second)
	if reply == nil {
		t.Fatal("no reply")
	}
	var got discoveryReply
	if err := json.Unmarshal(reply, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	u, err := url.Parse(got.Address)
	if err != nil {
		t.Fatalf("Address %q: %v", got.Address, err)
	}
	if u.Port() != "9123" {
		t.Errorf("Address port = %q, want the listening port 9123", u.Port())
	}
}

// TestDiscoveryAddressUsesInternalPortAndBaseURL is platform.discovery.R4: the
// reference announces http and InternalHttpPort (not PublicHttpPort, and not
// https while HTTPS is enabled) and appends BaseUrl as a path prefix.
func TestDiscoveryAddressUsesInternalPortAndBaseURL(t *testing.T) {
	srv, st, addr := newDiscoveryTestServer(t)

	cfg := networkConfigurationDto{
		BaseUrl:            "/jf",
		InternalHttpPort:   9000,
		PublicHttpPort:     9100,
		PublicHttpsPort:    9200,
		EnableHttps:        true,
		AutoDiscovery:      true,
		EnableIPv4:         true,
		EnableRemoteAccess: true,
	}
	setNetworkSection(t, st, cfg)
	reply := askDiscovery(t, addr, "who is JellyfinServer?", 2*time.Second)
	if reply == nil {
		t.Fatal("no reply")
	}
	var got discoveryReply
	if err := json.Unmarshal(reply, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	u, err := url.Parse(got.Address)
	if err != nil {
		t.Fatalf("Address %q: %v", got.Address, err)
	}
	if u.Scheme != "http" || u.Port() != "9000" || u.Path != "/jf" {
		t.Errorf("Address = %q, want http on the InternalHttpPort with /jf appended", got.Address)
	}
	_ = srv
}

// TestDiscoveryCandidates covers the address switches of the network section:
// loopback and link-local are never advertised, the address family switches
// are honored, and IgnoreVirtualInterfaces drops the interfaces the operator
// named (prefix match, case-insensitive).
func TestDiscoveryCandidates(t *testing.T) {
	all := []localAddr{
		{name: "lo", ip: net.ParseIP("127.0.0.1"), mask: net.CIDRMask(8, 32)},
		{name: "veth0d1a2b3", ip: net.ParseIP("172.17.0.2"), mask: net.CIDRMask(16, 32)},
		{name: "eth0", ip: net.ParseIP("192.0.2.10"), mask: net.CIDRMask(24, 32)},
		{name: "eth0", ip: net.ParseIP("fe80::1"), mask: net.CIDRMask(64, 128)},
		{name: "eth0", ip: net.ParseIP("2001:db8::10"), mask: net.CIDRMask(64, 128)},
	}

	got := discoveryCandidates(all, networkConfigurationDto{
		EnableIPv4: true, IgnoreVirtualInterfaces: true, VirtualInterfaceNames: []string{"VETH"},
	})
	if len(got) != 1 || got[0].ip.String() != "192.0.2.10" {
		t.Fatalf("candidates = %v, want just the IPv4 eth0 address", got)
	}

	got = discoveryCandidates(all, networkConfigurationDto{
		EnableIPv4: true, EnableIPv6: true, IgnoreVirtualInterfaces: false,
	})
	if len(got) != 3 {
		t.Fatalf("candidates = %v, want the global v4, the virtual v4 and the global v6", got)
	}

	got = discoveryCandidates(all, networkConfigurationDto{EnableIPv6: true})
	if len(got) != 1 || got[0].ip.String() != "2001:db8::10" {
		t.Fatalf("candidates = %v, want just the global IPv6 address", got)
	}
}

// TestPickDiscoveryAddr is platform.discovery.R5: the address on the asking
// client's own subnet wins, because any other one may be unroutable from that
// client; with no subnet match the first candidate is used.
func TestPickDiscoveryAddr(t *testing.T) {
	cands := []localAddr{
		{name: "eth0", ip: net.ParseIP("10.1.2.3"), mask: net.CIDRMask(24, 32)},
		{name: "eth1", ip: net.ParseIP("192.0.2.10"), mask: net.CIDRMask(24, 32)},
	}
	if ip, ok := pickDiscoveryAddr(cands, net.ParseIP("192.0.2.77")); !ok || ip.String() != "192.0.2.10" {
		t.Fatalf("pick = %v (%v), want the peer's own subnet", ip, ok)
	}
	if ip, ok := pickDiscoveryAddr(cands, net.ParseIP("198.51.100.4")); !ok || ip.String() != "10.1.2.3" {
		t.Fatalf("pick = %v (%v), want the first candidate", ip, ok)
	}
	if _, ok := pickDiscoveryAddr(nil, net.ParseIP("192.0.2.77")); ok {
		t.Fatal("picked an address from an empty candidate list")
	}
}

// setNetworkSection stores the network configuration section the responder
// reads per request.
func setNetworkSection(t *testing.T, st *sqlite.Store, cfg networkConfigurationDto) {
	t.Helper()
	cfg.nonNil()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := st.SetSetting(context.Background(), settingNetworkConfig, string(raw)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}
