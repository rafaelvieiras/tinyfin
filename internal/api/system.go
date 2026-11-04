package api

import (
	"encoding/json"
	"net"
	"net/http"
	"runtime"
)

// publicSystemInfo mirrors the OpenAPI schema's PublicSystemInfo, trimmed
// to fields Tinyfin can meaningfully answer right now. OperatingSystem is
// deprecated on the reference server and omitted here (SystemInfo below
// carries the equivalent, non-deprecated-in-spirit runtime.GOOS-derived
// value instead).
type publicSystemInfo struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	ID                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

func (s *Server) handleSystemInfoPublic(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, publicSystemInfo{
		LocalAddress: localAddress(r),
		ServerName:   s.effectiveServerName(),
		Version:      Version,
		ProductName:  ProductName,
		ID:           s.cfg.ServerID,
		// startup.state.R1: false until POST /Startup/Complete.
		StartupWizardCompleted: s.setupComplete(r.Context()),
	})
}

// systemInfo mirrors the OpenAPI schema's SystemInfo, trimmed to fields
// this project can honestly answer without a plugin/installation system,
// a WebSocket session layer, or hardware/OS telemetry beyond what's
// trivial to read from the Go runtime (docs/09's memory-conscious spirit
// extends to "don't build telemetry plumbing this slice doesn't need").
// Deprecated-on-upstream fields describing filesystem paths
// (ProgramDataPath, WebPath, CachePath, ...) are left out entirely rather
// than fabricated — Tinyfin's layout doesn't mirror the reference
// server's.
type systemInfo struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	ID                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
	HasPendingRestart      bool   `json:"HasPendingRestart"`
	IsShuttingDown         bool   `json:"IsShuttingDown"`
	SupportsLibraryMonitor bool   `json:"SupportsLibraryMonitor"`
	SystemArchitecture     string `json:"SystemArchitecture"`
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, systemInfo{
		LocalAddress:           localAddress(r),
		ServerName:             s.effectiveServerName(),
		Version:                Version,
		ProductName:            ProductName,
		OperatingSystem:        runtime.GOOS,
		ID:                     s.cfg.ServerID,
		StartupWizardCompleted: s.setupComplete(r.Context()),
		// No filesystem watcher, restart supervisor, or plugin/install
		// pipeline exists yet, so these are honest defaults rather than
		// fabricated telemetry — not "unknown", they are actually false.
		HasPendingRestart:      false,
		IsShuttingDown:         false,
		SupportsLibraryMonitor: false,
		SystemArchitecture:     runtime.GOARCH,
	})
}

// handleQuickConnectEnabled is GET /QuickConnect/Enabled — unauthenticated,
// called unconditionally by jellyfin-web's login screen on mount (an
// unhandled 404 here leaves the login screen on its loading spinner).
// Reports whether the QuickConnect flow (quickconnect.go) is on.
func (s *Server) handleQuickConnectEnabled(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.quickConnectEnabled())
}

// handleBrandingConfiguration is GET /Branding/Configuration (anonymous):
// the branding section of the configuration (config_sections.go) that the
// login page reads; members never set are left out.
func (s *Server) handleBrandingConfiguration(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.brandingConfiguration(r.Context()))
}

// handleConfigurationPages is GET /web/ConfigurationPages — the admin
// Dashboard's plugin-supplied settings-page registry. Confirmed live
// against a real Jellyfin server (reference server): with no plugins
// installed there, the response is a bare empty array, not a 404 or an
// object — so an empty array is the actually-correct answer for a server
// with no plugin system at all (docs/01's out-of-scope list), not merely a
// convenient stub. Left unregistered, jellyfin-web's Dashboard overview
// page 404s on every load, one of the handful of calls that page fires
// unconditionally on mount.
func (s *Server) handleConfigurationPages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []struct{}{})
}

// handleSessionsCapabilitiesFull is POST /Sessions/Capabilities/Full —
// every official client (web and the Android/iOS apps' shared WebView
// shell) calls this unconditionally right after sign-in, to register its
// playback capabilities (supported codecs/containers, device profile) for
// the session. Tinyfin now has an in-process session registry
// (sessions.go's sessionRegistry, docs/06's default mode) to upsert into —
// docs/00's O2 device-profile decision (either an inline profile per
// request or the compiled-in fallback keyed by Client) is unaffected, this
// is a separate concern: what GET /Sessions reports back, not how a
// playback decision picks a device profile. The body is decoded
// best-effort (a malformed body just registers zero-value capabilities,
// same swallow-errors posture as sessions_playback.go's
// reportPlaybackPosition) and the caller's auth context supplies
// everything else the registry needs. A 204 is returned unconditionally,
// same as before this existed: left unregistered entirely, this 404s and
// throws an uncaught promise rejection right on landing on the home
// screen — found by testing against a real jellyfin-web build (Android
// WebView shell), where it manifested as the client's own
// "can't connect to server" error rather than a visibly scoped failure.
func (s *Server) handleSessionsCapabilitiesFull(w http.ResponseWriter, r *http.Request) {
	var caps sessionCapabilitiesDto
	_ = json.NewDecoder(r.Body).Decode(&caps)

	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil && ac.Authenticated {
		s.sessions.upsertCapabilities(r.Context(), ac.DeviceID, string(ac.User.ID), ac.User.Name, ac.Client, ac.Device, ac.Version, remoteEndPoint(r), caps)
		// Forced (docs/12): a capabilities change is one of the reference
		// server's own always-forced Sessions triggers, not a throttled
		// heartbeat.
		s.notifySessions(r.Context(), true)
	}

	w.WriteHeader(http.StatusNoContent)
}

// endpointInfo mirrors the OpenAPI schema's EndpointInfo — confirmed
// against a real Jellyfin server (reference server), not assumed: a plain
// GET with no request body came back exactly
// {"IsLocal":false,"IsInNetwork":true} when curled from outside the
// container (through the Docker port mapping, so the peer address the
// container actually sees is the Docker bridge gateway — a private-range
// address, not loopback) and {"IsLocal":true,"IsInNetwork":true} when
// curled from a shell inside that same container against 127.0.0.1.
// Requires auth — confirmed the same way (401 with no token) — unlike
// System/Info/Public.
type endpointInfo struct {
	IsLocal     bool `json:"IsLocal"`
	IsInNetwork bool `json:"IsInNetwork"`
}

// handleSystemEndpoint serves GET /System/Endpoint, called once per session
// right after sign-in by every official client — found 404ing, not just
// unpolished, by testing a real jellyfin-web build via Playwright.
//
// IsLocal/IsInNetwork are derived from the request's actual TCP peer
// address (net.Request.RemoteAddr), not any client-suppliable header:
// confirmed against the real server that X-Forwarded-For is ignored
// entirely — sending a public IP in that header while connecting from a
// private address still came back IsInNetwork: true, so this project
// doesn't need (and mustn't add) reverse-proxy trust configuration to
// match that. IsInNetwork also covers link-local addresses
// (net.IP.IsLinkLocalUnicast) in addition to RFC 1918 private ranges
// (net.IP.IsPrivate) and loopback — not separately confirmed against the
// real server (the test setup used had no way to originate a
// request from a link-local source), but consistent with what "in
// network" means everywhere else in networking, and strictly additive to
// what was confirmed (it only ever makes this true in a case the real
// server was never actually tested against, never contradicts a
// confirmed true/false pair above).
func (s *Server) handleSystemEndpoint(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	local := ip != nil && ip.IsLoopback()
	inNetwork := local || (ip != nil && s.isLocalAddress(ip))
	writeJSON(w, http.StatusOK, endpointInfo{IsLocal: local, IsInNetwork: inNetwork})
}

// remoteIP parses the TCP peer address off r.RemoteAddr (host:port, per
// net/http's own doc comment on that field), returning nil if it isn't a
// parseable IP — a defensive fallback for whatever a test harness's
// httptest.Request might set RemoteAddr to, since production requests
// always reach here through a real net.Listener that sets it to a genuine
// address.
func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// localAddress reports the address the client used to reach this server,
// derived from the request itself (Host header) — the only "local
// address" Tinyfin can honestly know without operator-supplied
// configuration this slice doesn't have. Scheme is a best-effort guess:
// TLS if the connection that reached this handler was itself TLS.
func localAddress(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
