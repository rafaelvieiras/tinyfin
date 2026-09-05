package main

import (
	"net/http"
	"strings"
)

// withWebUI wraps api to also serve a built jellyfin-web (dist/) from dir
// at /web/, redirecting "/" there — this is what makes the *official*
// mobile apps usable at all: they're a WebView shell that loads the
// server's own bundled web client, not a fully native UI (confirmed via
// logcat against a real jellyfin-android build, docs/00-decision-log.md).
// jellyfin-web itself is a separate, GPL-2.0 project (github.com/jellyfin/
// jellyfin-web); this package never imports or links against it, it only
// serves whatever static files happen to be in dir — see the Dockerfile's
// optional WITH_WEB build stage for how dir gets populated.
//
// No SPA-fallback route is needed: jellyfin-web routes client-side via URL
// fragments (#/home, ...), which browsers never send to the server, so a
// plain static file server matches its actual request pattern exactly.
func withWebUI(api http.Handler, dir string) http.Handler {
	fileServer := http.StripPrefix("/web/", http.FileServer(http.Dir(dir)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/", r.URL.Path == "/web":
			http.Redirect(w, r, "/web/", http.StatusFound)
		// An API route that happens to live under the /web/ prefix on the
		// reference server (its plugin-settings-page registry) rather than
		// a static asset — must be checked before the generic /web/ prefix
		// case below, or the file server would shadow it and 404 trying to
		// serve a file named "ConfigurationPages" that doesn't exist.
		case r.URL.Path == "/web/ConfigurationPages":
			api.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/web/"):
			fileServer.ServeHTTP(w, r)
		default:
			api.ServeHTTP(w, r)
		}
	})
}
