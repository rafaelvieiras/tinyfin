package api

import "net/http"

// withCORS lets a web client hosted on a different origin than this server
// talk to it — the normal topology for a Jellyfin-compatible web client
// (docs/04): jellyfin-web is commonly served from its own host/port and
// points at whichever server address the user enters, so without this the
// browser's own CORS enforcement blocks every request before it reaches
// this server's auth middleware. Reflects the request's Origin (rather
// than a literal "*") so it still works for a client that sends
// credentials; token auth here is header-based (X-Emby-Authorization /
// X-Emby-Token), not cookies, so there's no CSRF exposure from allowing any
// origin.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		// Reflect back whatever headers this specific preflight asked for,
		// rather than maintaining a fixed allow-list: real clients attach
		// headers this project has no reason to enumerate up front (e.g.
		// jellyfin-web sends Cache-Control on some requests, which an
		// earlier fixed list here didn't include and the browser silently
		// blocked before the request ever reached this server).
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Emby-Authorization, X-Emby-Token, X-Emby-Client, Range")
		}
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
