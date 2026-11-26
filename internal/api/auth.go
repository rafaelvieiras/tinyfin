package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
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
		if s.remoteAccessGated(r) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
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
				// users.token.R4: a disabled account's tokens are rejected.
				if u, err := s.store.GetUser(r.Context(), at.UserID); err == nil && !u.IsDisabled {
					ac.Authenticated = true
					ac.AuthToken = at
					ac.User = u
					// A legacy per-user DeviceId suffix is dropped before
					// anything keys off it, so the same install cannot arrive
					// as two devices (deviceid.go, devices.R11).
					ac.DeviceID = canonicalDeviceID(ac.DeviceID, string(u.ID))
					// Name and version sent with a request refresh the
					// user's device entry (admin.devices.R5) — the token's
					// device when the header carries no DeviceId of its own.
					if ac.Device != "" || ac.Version != "" {
						touchID := ac.DeviceID
						if touchID == "" {
							touchID = canonicalDeviceID(string(at.DeviceID), string(u.ID))
						}
						_ = s.store.TouchDeviceClient(r.Context(), storage.DeviceID(touchID), u.ID, ac.Device, ac.Version)
					}
					s.inheritDeviceFields(r.Context(), ac)
					ac.DeviceID = canonicalDeviceID(ac.DeviceID, string(u.ID))
				}
			} else if k, err := s.store.LookupAPIKey(r.Context(), ac.Token); err == nil {
				// An API key authenticates as an administrator-level
				// caller that belongs to no user (users.me.R4,
				// users.list.R8).
				ac.Authenticated = true
				ac.IsAPIKey = true
				ac.AuthToken = storage.AuthToken{Token: k.Token, IssuedAt: k.DateCreated}
				ac.User = storage.User{IsAdministrator: true}
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
// Each value is decoded with url.QueryUnescape semantics before use
// (users.auth.R11): "%20" and "+" both decode to a space; a literal plus
// is only available as "%2B". Percent-decoding is confirmed necessary, not optional hardening, by testing
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
	// The scheme name is matched case-insensitively and must be followed
	// by whitespace (or nothing).
	if len(fields) < len(scheme) || !strings.EqualFold(fields[:len(scheme)], scheme) {
		return "", "", "", "", ""
	}
	fields = fields[len(scheme):]
	if fields != "" && fields[0] != ' ' && fields[0] != '\t' {
		return "", "", "", "", ""
	}

	for _, part := range splitAuthPairs(fields) {
		key, val, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		if decoded, err := url.QueryUnescape(val); err == nil {
			val = decoded
		}
		// Keys are case-sensitive; a repeated key's last value wins.
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

// inheritDeviceFields implements playstate.auth.R2: a header that carries
// only the token (no client/device fields) takes the omitted fields from the
// device the token was issued to. Fields the header did send are kept.
func (s *Server) inheritDeviceFields(ctx context.Context, ac *AuthContext) {
	if ac.DeviceID == "" {
		ac.DeviceID = string(ac.AuthToken.DeviceID)
	}
	if ac.Client != "" && ac.Device != "" && ac.Version != "" {
		return
	}
	d, err := s.store.GetDevice(ctx, storage.DeviceID(ac.DeviceID))
	if err != nil {
		return
	}
	if ac.Client == "" {
		ac.Client = d.AppName
	}
	if ac.Device == "" {
		ac.Device = d.Name
	}
	if ac.Version == "" {
		ac.Version = d.AppVersion
	}
}

// splitAuthPairs splits the parameter list of the Authorization header on
// commas that are not inside a double-quoted value.
func splitAuthPairs(s string) []string {
	var parts []string
	inQuotes := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
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
		// users.me.R5, users.list.R7: a non-administrator outside an
		// access-schedule window is refused. Reading the own profile by id
		// is exempt (users.get.R7).
		if !ac.User.IsAdministrator && !scheduleAllows(storedUserPolicy(ac.User), time.Now()) &&
			!(r.Method == http.MethodGet && strings.EqualFold(r.URL.Path, "/Users/"+string(ac.User.ID))) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// streamFileRequests reports whether the request's {file} segment names the
// media stream itself — "stream", or "stream.<container>". Shared, because
// both the video and the audio route reach their segments through the same
// kind of wildcard.
func streamFileRequests(r *http.Request) bool {
	name := r.PathValue("file")
	if name == "stream" {
		return true
	}
	ext, ok := strings.CutPrefix(name, "stream.")
	return ok && ext != ""
}

// requireAuthOrStream guards the two wildcard routes whose last segment is
// both the media stream and the HLS segment files — GET /Videos/{itemId}/{file}
// and GET /Audio/{itemId}/{file} (server.go). The stream itself answers
// without a credential; everything else the wildcard reaches keeps the
// requireAuth rules.
//
// Measured on the reference, because this looked like a hole rather than a
// decision: /Videos/{itemId}/stream answered 206 with no credential, 206 again
// with a token that does not exist, and 400 (not 401) for an item that does
// not exist — so no auth check runs on that path at all. Its neighbours did
// answer 401: master.m3u8, hls1/main/0.ts, the trickplay sheets,
// AdditionalParts, /Items/{id}/Download and /Audio/{id}/universal.
//
// The reason it cannot be tightened: the official Android TV client direct
// plays through a media URL carrying no credential. Its player builds the URL
// from the SDK (getVideoStreamUrl adds no key) and its HTTP client comes from
// OkHttpFactory.createClient, which installs no auth interceptor either — by
// the header it sends nothing at all. Requiring a token here answered every
// direct-play attempt with 401 (a real evening's log: four requests, four
// 401s, playback stopped in under a second each time), while transcode
// playback kept working because that URL carries ApiKey.
func requireAuthOrStream(next http.HandlerFunc) http.HandlerFunc {
	guarded := requireAuth(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if streamFileRequests(r) {
			next(w, r)
			return
		}
		guarded(w, r)
	}
}

// scheduleAllows reports whether the policy's access schedules permit the
// given local time (users.auth.R18): no schedule means no restriction,
// otherwise the time must fall in some entry's day and
// StartHour <= hour <= EndHour, where hour includes the fraction of the
// current hour. DayOfWeek also accepts Everyday, Weekday and Weekend.
func scheduleAllows(p userPolicyDto, now time.Time) bool {
	if len(p.AccessSchedules) == 0 {
		return true
	}
	hour := float64(now.Hour()) + float64(now.Minute())/60 + float64(now.Second())/3600
	for _, sc := range p.AccessSchedules {
		if !scheduleDayMatches(sc.DayOfWeek, now.Weekday()) {
			continue
		}
		if sc.StartHour <= hour && hour <= sc.EndHour {
			return true
		}
	}
	return false
}

func scheduleDayMatches(day string, wd time.Weekday) bool {
	switch strings.ToLower(day) {
	case "everyday":
		return true
	case "weekday":
		return wd != time.Saturday && wd != time.Sunday
	case "weekend":
		return wd == time.Saturday || wd == time.Sunday
	}
	return strings.EqualFold(day, wd.String())
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
