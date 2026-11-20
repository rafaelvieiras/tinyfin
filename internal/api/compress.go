// compress.go compresses the answers that are worth compressing.
//
// Measured on the production home screen before this existed: every JSON answer
// went out whole — /Items/Latest of one movie library is 246 KB of it, 49 KB
// once gzipped, and a home screen pulls a dozen of those. The same measurement
// over the static client showed no Content-Encoding on any of the 3.8 MB of
// JavaScript. Neither the server nor the proxy in front of it compresses, so
// this is the server's job.
//
// Only the types worth the CPU are touched: JSON and text. Video, pictures and
// everything else already compressed pass through byte for byte;
// text/event-stream is excluded because a stream must reach the client as it is
// written, not when a compressor decides to flush; and a range request is never
// compressed, because its answer is a byte range of the file and not a stream
// that can be re-encoded.
package api

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// withGzip compresses a JSON or text answer when the client says it takes gzip,
// and leaves every other answer alone.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestAcceptsGzip(r) || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		// Anything in front of this server, and any future CDN, has to keep the
		// compressed and plain answers apart.
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// requestAcceptsGzip reads Accept-Encoding, honoring an explicit "gzip;q=0"
// refusal.
func requestAcceptsGzip(r *http.Request) bool {
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

// gzipWriter decides once, when the status goes out: by then the handler has set
// Content-Type, and nothing has reached the client yet, so the header can still
// change.
type gzipWriter struct {
	http.ResponseWriter

	gz      *gzip.Writer
	decided bool
	skip    bool
}

func (w *gzipWriter) decide(status int) {
	w.decided = true
	h := w.Header()
	if status == http.StatusNoContent || status == http.StatusNotModified ||
		h.Get("Content-Range") != "" || !compressibleType(h.Get("Content-Type")) {
		w.skip = true
		return
	}
	// The answer is no longer the size the handler measured.
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	w.gz = gzip.NewWriter(w.ResponseWriter)
}

func (w *gzipWriter) WriteHeader(status int) {
	if !w.decided {
		w.decide(status)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipWriter) Write(p []byte) (int, error) {
	if !w.decided {
		w.decide(http.StatusOK)
	}
	if w.skip {
		return w.ResponseWriter.Write(p)
	}
	return w.gz.Write(p)
}

// Flush pushes the compressed bytes so far, so a streamed answer still streams.
func (w *gzipWriter) Flush() {
	if !w.decided {
		w.decide(http.StatusOK)
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// close finishes the compressed stream.
func (w *gzipWriter) close() {
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

// Unwrap keeps the writer transparent to capability checks — a websocket
// upgrade reads Hijack through this wrapper.
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// compressibleType reports whether a Content-Type is worth compressing.
func compressibleType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct == "" {
		// An answer that never set a type is not compressed: guessing would
		// risk corrupting a picture.
		return false
	}
	if strings.HasPrefix(ct, "text/event-stream") {
		return false
	}
	return strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/")
}
