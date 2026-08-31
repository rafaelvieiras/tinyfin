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
// yet (that's outside this slice's scope; see open item O1) so this is a
// hardcoded placeholder until one exists. Two constraints on its value,
// both found by testing against a real jellyfin-web build rather than
// something docs/04 could have predicted from the OpenAPI schema alone:
//
//   - Must be a plain numeric-only semver (no "-dev"/pre-release suffix)
//     — the reference client SDK's compareVersions rejects anything else.
//   - Must be >= @jellyfin/sdk's compiled-in MINIMUM_VERSION, or the
//     client refuses to connect at all ("this server needs to be
//     updated"). That constant is 10.10.0 as of the jellyfin-web build
//     this was verified against; report the same value here as the
//     compatibility baseline this project targets — the same number O1
//     still needs to pin formally, this is not a substitute for that
//     decision, just what unblocks manual testing today.
const Version = "10.10.0"

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

// Handler returns the complete http.Handler for the server: case-insensitive
// path normalization (routing.go), then route dispatch wrapped in the
// auth-parsing middleware (see auth.go for why this is a single
// always-applied wrapper rather than N per-route wrappers), all wrapped in
// CORS handling (cors.go) so a browser-hosted client on another origin can
// reach any route, auth-preflight included.
func (s *Server) Handler() http.Handler {
	return withCORS(s.withAuthContext(withCaseInsensitivePaths(s.mux)))
}

// withCaseInsensitivePaths rewrites r.URL.Path to a registered route's exact
// casing before it reaches the mux, if one matches case-insensitively — see
// normalizePathCase for why this is necessary.
func withCaseInsensitivePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = normalizePathCase(r.URL.Path)
		next.ServeHTTP(w, r)
	})
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
	s.mux.HandleFunc("GET /Users/{userId}", requireAuth(s.handleUsersGet))
	s.mux.HandleFunc("DELETE /Users/{userId}", requireAuth(s.handleUsersDelete))

	// Library and item navigation (library.go) — docs/04's highest-priority
	// group.
	s.mux.HandleFunc("GET /UserViews", requireAuth(s.handleUserViews))
	s.mux.HandleFunc("GET /Items", requireAuth(s.handleItemsList))
	s.mux.HandleFunc("GET /Items/{itemId}", requireAuth(s.handleItemGet))
	// Not in the current OpenAPI spec (docs/04's verification source), but
	// still sent by a real client: jellyfin-web's item-details page uses
	// the legacy jellyfin-apiclient package (same one behind the
	// lowercase Users/authenticatebyname call — see routing.go) to fetch
	// the item it's displaying, and that package calls this older
	// per-user form rather than plain GET /Items/{itemId}. Found by
	// testing against a real jellyfin-web build: left 404, it aborts the
	// whole details page instead of falling back. userId is accepted but
	// ignored, same as handleItemGet's plain form — no per-user
	// aggregate (PlayedPercentage, ...) exists yet to key off it.
	s.mux.HandleFunc("GET /Users/{userId}/Items/{itemId}", requireAuth(s.handleItemGet))
	// Public, unlike every other route in this slice — see handleItemImage's
	// doc comment in library.go for why: a browser's plain <img src=...>
	// can't attach the X-Emby-Token header this project's other routes
	// require, and testing against a real jellyfin-web build confirmed the
	// image URLs it renders never carry a token or api_key query param
	// either, so requiring auth here makes every image 401 silently
	// (broken poster/backdrop, no console-visible crash).
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", s.handleItemImage)
	s.mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{imageIndex}", s.handleItemImage)

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

	// Per-user, per-client UI settings (users.go's UserStore doc comment) —
	// requested by every jellyfin-web session immediately after login, and
	// left unhandled it fails hard enough to block the post-login redirect
	// entirely (found by testing against a real jellyfin-web build).
	s.mux.HandleFunc("GET /DisplayPreferences/{displayPreferencesId}", requireAuth(s.handleDisplayPreferencesGet))
	s.mux.HandleFunc("POST /DisplayPreferences/{displayPreferencesId}", requireAuth(s.handleDisplayPreferencesUpdate))
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
