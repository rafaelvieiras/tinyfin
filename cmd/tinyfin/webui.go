package main

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/webicons"
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
		// serve a file named "ConfigurationPages" that doesn't exist. Like
		// every API route it also answers with a trailing slash and in any
		// letter case (wire.path.R1, R3).
		case strings.EqualFold(strings.TrimSuffix(r.URL.Path, "/"), "/web/ConfigurationPages"):
			api.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/web/"):
			// The cache headers and the compression the plain file server
			// does not do (webui_cache.go).
			withWebUICache(webAssets(fileServer)).ServeHTTP(w, r)
		default:
			api.ServeHTTP(w, r)
		}
	})
}

// webAssets answers the icon-only pictures of the bundle with the Tinyfin
// mascot and lets every other request fall through to the files themselves.
//
// The set of pictures is small and fixed (internal/webicons owns it), and they
// sit on paths the bundle fingerprints, so a rebuild of the client does not
// break the match — see that package for why the wordmark stays upstream.
func webAssets(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveWebIcon(w, r) {
			return
		}
		files.ServeHTTP(w, r)
	})
}

// serveWebIcon writes the mascot for a path webicons owns. The zero modtime
// keeps Last-Modified off the answer; the asset's ETag is the validator, which
// is what turns a revalidating reload into a 304.
func serveWebIcon(w http.ResponseWriter, r *http.Request) bool {
	asset, ok := webicons.Lookup(r.URL.Path)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("ETag", asset.ETag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(asset.Data))
	return true
}
