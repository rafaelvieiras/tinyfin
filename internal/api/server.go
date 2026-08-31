// Package api implements the HTTP surface described in
// docs/04-api-compatibility.md, on top of the storage.Store interface
// (internal/storage). It is built entirely on stdlib net/http, using Go
// 1.22+ method+pattern routing on http.ServeMux — no third-party router,
// per AGENTS.md principle 2 ("single binary, zero required dependencies by
// default").
//
// Scope for this package right now: authentication (X-Emby-Authorization
// parsing and token lookup) plus the System/Users endpoints needed to prove
// the auth flow end to end. Library/playback/session endpoints are later
// slices (see the package-level TODO markers, none — deliberately not
// stubbed here at all, to avoid dead routes).
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Version is the wire-visible server version reported in PublicSystemInfo
// and SystemInfo. Tinyfin has no release-tagging/version-tracking system
// yet (that's outside this slice's scope) so this is a hardcoded
// placeholder until one exists.
const Version = "0.1.0-dev"

// ProductName is the wire-visible product name (PublicSystemInfo.ProductName
// / SystemInfo.ProductName).
const ProductName = "Tinyfin"

// Config carries the small set of server-identity knobs main assembles
// before constructing a Server. Everything here has a sensible default so
// a caller can pass a zero Config.
type Config struct {
	// ServerName is the human-facing name reported to clients
	// (PublicSystemInfo.ServerName / SystemInfo.ServerName). Defaults to
	// ProductName.
	ServerName string

	// ServerID is the stable identifier reported as PublicSystemInfo.Id,
	// SystemInfo.Id, AuthenticationResult.ServerId, and UserDto.ServerId.
	// There is no SystemStore/server-identity table yet (only the stores
	// storage.Store composes today), so there is nowhere to persist one —
	// if left empty, NewServer mints a random one for the life of the
	// process. Known simplification: this value is not stable across
	// restarts unless a caller supplies one explicitly.
	ServerID string
}

// Server is the API/metadata role's HTTP surface (docs/03). The zero value
// is not usable — construct with NewServer.
type Server struct {
	store storage.Store
	cfg   Config
	mux   *http.ServeMux
}

// NewServer builds a Server backed by store, applying cfg defaults where
// left zero. Call Handler to get the http.Handler to pass to
// http.ListenAndServe (or http.Server.Handler).
func NewServer(store storage.Store, cfg Config) *Server {
	if cfg.ServerName == "" {
		cfg.ServerName = ProductName
	}
	if cfg.ServerID == "" {
		cfg.ServerID = randomID()
	}

	s := &Server{store: store, cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the complete http.Handler for the server: route dispatch
// wrapped in the auth-parsing middleware (see auth.go for why this is a
// single always-applied wrapper rather than N per-route wrappers).
func (s *Server) Handler() http.Handler {
	return s.withAuthContext(s.mux)
}

func (s *Server) routes() {
	// Public — no token required, either because it's the login route
	// itself or because the reference server exposes it unauthenticated.
	s.mux.HandleFunc("GET /System/Info/Public", s.handleSystemInfoPublic)
	s.mux.HandleFunc("POST /Users/AuthenticateByName", s.handleAuthenticateByName)

	// Everything else in this slice's scope requires a valid token.
	s.mux.HandleFunc("GET /System/Info", requireAuth(s.handleSystemInfo))
	s.mux.HandleFunc("GET /Users/Me", requireAuth(s.handleUsersMe))
	// Admin-only in the reference server (UserController's [Authorize]
	// policy requires elevation for the list/create/delete routes below).
	// Tinyfin has no permissions system yet, so this only checks for a
	// valid token — anyone logged in can list/create/delete users right
	// now. Known simplification, not a silent gap: tighten this once a
	// permissions model exists.
	s.mux.HandleFunc("GET /Users", requireAuth(s.handleUsersList))
	s.mux.HandleFunc("POST /Users/New", requireAuth(s.handleUsersCreate))
	s.mux.HandleFunc("DELETE /Users/{userId}", requireAuth(s.handleUsersDelete))

	// Library and item navigation (library.go) — docs/04's highest-priority
	// group.
	s.mux.HandleFunc("GET /UserViews", requireAuth(s.handleUserViews))
	s.mux.HandleFunc("GET /Items", requireAuth(s.handleItemsList))
	s.mux.HandleFunc("GET /Items/{itemId}", requireAuth(s.handleItemGet))
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", requireAuth(s.handleItemImage))
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{imageIndex}", requireAuth(s.handleItemImage))

	// Direct Play negotiation and streaming (playback.go). "GET" patterns
	// also match HEAD per net/http.ServeMux's documented behavior, so the
	// stream routes don't need a separate HEAD registration.
	s.mux.HandleFunc("GET /Items/{itemId}/PlaybackInfo", requireAuth(s.handlePlaybackInfo))
	s.mux.HandleFunc("POST /Items/{itemId}/PlaybackInfo", requireAuth(s.handlePlaybackInfo))
	// http.ServeMux wildcards must occupy a whole path segment — "stream.
	// {container}" mixes a literal suffix with a wildcard in one segment,
	// which ServeMux rejects at registration time. A single {file}
	// wildcard covers both /stream and /stream.{container}; handleVideoStream
	// itself validates the segment starts with "stream" (see playback.go).
	s.mux.HandleFunc("GET /Videos/{itemId}/{file}", requireAuth(s.handleVideoStream))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// randomID mints an opaque hex identifier. Stdlib-only (crypto/rand), same
// approach as internal/storage/sqlite's newID/newToken, kept as a separate
// copy here rather than an import since this package must not depend on
// sqlite specifically (it depends only on the storage interface).
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("api: reading random bytes for ID generation: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
