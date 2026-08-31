package api

import (
	"net/http"
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

		header := r.Header.Get("X-Emby-Authorization")
		if header == "" {
			header = r.Header.Get("Authorization")
		}
		if header != "" {
			ac.Client, ac.Device, ac.DeviceID, ac.Version, ac.Token = parseEmbyAuth(header)
		}
		// Fallback: a bare token with no MediaBrowser wrapper (docs/04).
		// Only takes effect if the header above didn't already supply one.
		if ac.Token == "" {
			if t := r.Header.Get("X-Emby-Token"); t != "" {
				ac.Token = t
			}
		}
		// Fallback: a query-string token. Confirmed necessary, not just a
		// reference-server nicety, by testing against a real jellyfin-web
		// build: a plain <video src=...>/<audio src=...> element can't
		// attach any header at all (same root cause as handleItemImage's
		// public-route fix in library.go), so jellyfin-web puts the token
		// in the stream URL itself instead — as "ApiKey" (confirmed) and,
		// per the wider Jellyfin ecosystem's convention for the same
		// mechanism, its lowercase-with-underscore spelling "api_key".
		// Lowest precedence: only checked once every header-based source
		// above has come up empty.
		if ac.Token == "" {
			q := r.URL.Query()
			if t := q.Get("ApiKey"); t != "" {
				ac.Token = t
			} else if t := q.Get("api_key"); t != "" {
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
// by both X-Emby-Authorization and a bare Authorization header (docs/04):
//
//	MediaBrowser Client="...", Device="...", DeviceId="...", Version="...", Token="..."
//
// Fields may appear in any order and any may be absent; unrecognized keys
// are ignored so a client sending an extra field doesn't break parsing. A
// header that doesn't start with the MediaBrowser scheme yields all-empty
// results rather than an error — callers treat "nothing parsed" the same
// as "no header sent".
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
