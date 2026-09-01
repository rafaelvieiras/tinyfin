package api

import (
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
		ServerName:   s.cfg.ServerName,
		Version:      Version,
		ProductName:  ProductName,
		ID:           s.cfg.ServerID,
		// No startup wizard exists in Tinyfin (no plugin/first-run
		// configuration flow, docs/01), so "completed" is always true —
		// this keeps a client from redirecting the user into a wizard
		// screen that doesn't exist.
		StartupWizardCompleted: true,
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
		ServerName:             s.cfg.ServerName,
		Version:                Version,
		ProductName:            ProductName,
		OperatingSystem:        runtime.GOOS,
		ID:                     s.cfg.ServerID,
		StartupWizardCompleted: true,
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
// called unconditionally by jellyfin-web's login screen on mount. Tinyfin
// has no Quick Connect implementation, so this always answers false rather
// than 404ing: found missing by testing against a real jellyfin-web build
// (Android WebView shell) — an unhandled 404 here throws an uncaught
// promise rejection that leaves the login screen stuck on its loading
// spinner instead of rendering.
func (s *Server) handleQuickConnectEnabled(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, false)
}

// brandingOptions mirrors the OpenAPI schema's BrandingOptions. Tinyfin has
// no branding/customization system (out of scope per docs/01), but
// jellyfin-web's login screen fetches this unconditionally — left
// unregistered it fails the same way handleQuickConnectEnabled's doc
// comment describes, found the same way (real jellyfin-web build).
type brandingOptions struct {
	LoginDisclaimer     string `json:"LoginDisclaimer"`
	CustomCss           string `json:"CustomCss"`
	SplashscreenEnabled bool   `json:"SplashscreenEnabled"`
}

func (s *Server) handleBrandingConfiguration(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, brandingOptions{})
}

// handleSessionsCapabilitiesFull is POST /Sessions/Capabilities/Full —
// every official client (web and the Android/iOS apps' shared WebView
// shell) calls this unconditionally right after sign-in, to register its
// playback capabilities (supported codecs/containers, device profile) for
// the session. Tinyfin has no session/capability store yet (docs/00's O2
// device-profile decision relies on either an inline profile per request
// or the compiled-in fallback keyed by the request's Client header — see
// deviceprofile — not stored per-session state), so the body is accepted
// and discarded: a 204 is enough to satisfy the client, which treats this
// as a fire-and-forget registration call. Left unregistered, this 404s
// and throws an uncaught promise rejection right on landing on the home
// screen — found by testing against a real jellyfin-web build (Android
// WebView shell), where it manifested as the client's own
// "can't connect to server" error rather than a visibly scoped failure.
func (s *Server) handleSessionsCapabilitiesFull(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
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
