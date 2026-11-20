package main

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// withWebUICache gives the bundled client the two headers it was missing.
//
// Measured against the production server before this existed: none of the 3.8
// MB of JavaScript carried a Content-Encoding (the browser downloaded every
// byte whole, 5x what gzip costs), and none of it carried a Cache-Control — only
// the Last-Modified a plain http.FileServer sets, so a reload re-asked the
// server for 93 files and the first visit after every version paid the whole
// 3.8 MB again.
//
// The build puts a version query on every bundle it loads
// ("main.jellyfin.bundle.js?143c1a38…"), so a request that has one can be cached
// forever: that URL's bytes never change. index.html and the handful of root
// files carry no version and must be revalidated, or a new build would never be
// picked up.
func withWebUICache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if versionedAsset(r) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if !acceptsGzipStatic(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &staticGzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// versionedAsset reports whether the request names a file whose bytes are
// content-addressable: the client's build appends the build's own version as a
// query string to every bundle it loads. A navigation, a directory and the root
// files (index.html, manifest.json, the icons) have no query and are not
// versioned.
func versionedAsset(r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return false
	}
	base := strings.ToLower(pathBase(r.URL.Path))
	if base == "" || base == "index.html" || strings.HasSuffix(base, ".html") {
		return false
	}
	return true
}

func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// acceptsGzipStatic reads the Accept-Encoding header the way the API's
// middleware does, including an explicit "gzip;q=0" refusal.
func acceptsGzipStatic(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(part, ";")
		name := strings.TrimSpace(fields[0])
		if !strings.EqualFold(name, "gzip") && name != "*" {
			continue
		}
		for _, param := range fields[1:] {
			if q, ok := strings.CutPrefix(strings.TrimSpace(param), "q="); ok && strings.TrimSpace(q) == "0" {
				return false
			}
		}
		return true
	}
	return false
}

// staticCompressible lists the types the bundled client serves and that are
// worth compressing. Pictures, fonts and video keep their bytes.
func staticCompressible(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case strings.HasPrefix(ct, "application/json"),
		strings.HasPrefix(ct, "application/javascript"),
		strings.HasPrefix(ct, "application/manifest+json"),
		strings.HasPrefix(ct, "application/xml"),
		strings.HasPrefix(ct, "application/wasm"),
		strings.HasPrefix(ct, "image/svg+xml"):
		return true
	}
	return false
}

// staticGzipWriter compresses a static answer when its type is worth it. The
// decision is made once, when the status goes out, by which point
// http.FileServer has set Content-Type — and it is always the last chance,
// because that call sends the headers.
type staticGzipWriter struct {
	http.ResponseWriter

	gz      *gzip.Writer
	decided bool
	skip    bool
}

func (w *staticGzipWriter) decide(status int) {
	w.decided = true
	h := w.Header()
	// http.ServeContent answers a range request with Content-Range; that answer
	// is a slice of the file and cannot be re-encoded.
	if status == http.StatusNoContent || status == http.StatusNotModified ||
		h.Get("Content-Range") != "" || !staticCompressible(h.Get("Content-Type")) {
		w.skip = true
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	w.gz = gzip.NewWriter(w.ResponseWriter)
}

func (w *staticGzipWriter) WriteHeader(status int) {
	if !w.decided {
		w.decide(status)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *staticGzipWriter) Write(p []byte) (int, error) {
	if !w.decided {
		w.decide(http.StatusOK)
	}
	if w.skip {
		return w.ResponseWriter.Write(p)
	}
	return w.gz.Write(p)
}

func (w *staticGzipWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *staticGzipWriter) close() {
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

func (w *staticGzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
