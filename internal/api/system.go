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
