package api

import (
	"net/http"
	"net/url"
	"strings"
)

// withAuthContext is the single, always-applied middleware that parses the
// client-identification header and, if it carries a token, resolves it —
// attaching the result to the request's context as an *AuthContext (see
// context.go) regardless of outcome. It never itself rejects a request.
//
// This is a deliberate choice over N per-route "require auth" middlewares
// wrapping only the protected routes: several routes in this package's
// scope need the parsed Client/Device/DeviceId/Version fields even when no
// valid token is present yet (AuthenticateByName reads DeviceId to know
// which device to attach the freshly-minted token to, before that token
// exists). Making parsing unconditional and universal means every handler
// gets the same, single way to read identity off a request — AuthFromContext
// — instead of some handlers parsing the header themselves. Enforcement
// (401 on missing/invalid auth) is then a separate, explicit per-route
// concern via requireAuth, applied only to the routes that need it (see
// Server.routes in server.go).
func (s *Server) withAuthContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := &AuthContext{}

		// Only two credential channels are honored (decision D14,
		// docs/contract/README.md scope rule 6): an `Authorization:
		// MediaBrowser ...` header, and the `ApiKey` query parameter —
		// needed because a plain <video>/<audio> element or an HLS player
		// resolving a stream URL cannot attach any header. Legacy
		// channels (X-Emby-Authorization, X-Emby-Token,
		// X-MediaBrowser-Token, the api_key query parameter and the Emby
		// scheme) are deliberately ignored: the request is simply
		// unauthenticated.
		if header := r.Header.Get("Authorization"); header != "" {
			ac.Client, ac.Device, ac.DeviceID, ac.Version, ac.Token = parseEmbyAuth(header)
		}
		if ac.Token == "" {
			if t := r.URL.Query().Get("ApiKey"); t != "" {
				ac.Token = t
			}
		}

		if ac.Token != "" {
			if at, err := s.store.LookupToken(r.Context(), ac.Token); err == nil {
				if u, err := s.store.GetUser(r.Context(), at.UserID); err == nil {
					ac.Authenticated = true
					ac.AuthToken = at
					ac.User = u
				}
			}
		}

		next.ServeHTTP(w, r.WithContext(contextWithAuth(r.Context(), ac)))
	})
}

// parseEmbyAuth parses the MediaBrowser client-identification header used
// by the Authorization header (docs/04):
//
//	MediaBrowser Client="...", Device="...", DeviceId="...", Version="...", Token="..."
//
// Fields may appear in any order and any may be absent; unrecognized keys
// are ignored so a client sending an extra field doesn't break parsing. A
// header that doesn't start with the MediaBrowser scheme yields all-empty
// results rather than an error — callers treat "nothing parsed" the same
// as "no header sent".
//
// Each value is percent-decoded (url.PathUnescape, not QueryUnescape — a
// literal "+" in a device/client name must stay a "+", not become a space)
// before use. Confirmed necessary, not optional hardening, by testing
// against a real jellyfin-web build: it sends this header's Client field as
// literally `Client="Jellyfin%20Web"` (its own encodeURIComponent-style
// escaping of the space), and a real Jellyfin server accepts that and
// reports the field back decoded — found because sessions.go's GET
// /Sessions is the first route in this project to ever echo Client back
// verbatim; every earlier consumer of AuthContext.Client either discarded
// it or only used it for equality/logging, where the un-decoded value
// happened to never matter. A value that fails to decode (malformed
// percent-escapes) falls back to the raw string rather than dropping the
// field entirely.
func parseEmbyAuth(header string) (client, device, deviceID, version, token string) {
	const scheme = "MediaBrowser"

	fields := strings.TrimSpace(header)
	if !strings.HasPrefix(fields, scheme) {
		return "", "", "", "", ""
	}
	fields = strings.TrimSpace(fields[len(scheme):])

	for _, part := range strings.Split(fields, ",") {
		key, val, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		if decoded, err := url.PathUnescape(val); err == nil {
			val = decoded
		}
		switch strings.TrimSpace(key) {
		case "Client":
			client = val
		case "Device":
			device = val
		case "DeviceId":
			deviceID = val
		case "Version":
			version = val
		case "Token":
			token = val
		}
	}
	return client, device, deviceID, version, token
}

// requireAuth wraps a handler that must not run unless the request resolved
// to a valid token (AuthContext.Authenticated). Applied per-route in
// Server.routes, not globally — see withAuthContext's doc comment for why
// parsing and enforcement are split.
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ac, ok := AuthFromContext(r.Context())
		if !ok || !ac.Authenticated {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// requireAdmin wraps a handler that must not run unless the request
// resolved to a valid, administrator-privileged token — library management
// (library_admin.go's VirtualFolders/Refresh endpoints, matching the
// reference server's own admin-only scoping for library structure changes)
// is the first thing in this project that needs a stricter check than
// requireAuth's "any authenticated user." A non-admin authenticated user
// gets 403, not 401 — the credential itself is valid, it just isn't
// sufficient.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ac, _ := AuthFromContext(r.Context())
		if !ac.User.IsAdministrator {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next(w, r)
	})
}
