package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gzipHandlerServer wraps one handler in the middleware, standing in for a real
// route.
func gzipHandlerServer(h http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(withGzip(h))
}

// TestGzipCompressesJSON: a JSON answer is compressed when the client takes
// gzip, and carries the headers a cache in front of it needs.
func TestGzipCompressesJSON(t *testing.T) {
	body := `{"Overview":"` + strings.Repeat("uma sinopse bem longa ", 200) + `"}`
	srv := gzipHandlerServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1")
		writeJSON(w, http.StatusOK, map[string]string{"Overview": body})
	})
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	// Content-Length may come back: net/http measures the compressed body
	// itself when the answer fits its buffer. What must not survive is the
	// handler's own length, which described the uncompressed bytes.
	if cl := resp.Header.Get("Content-Length"); cl != "" && cl == "1" {
		t.Errorf("Content-Length = %q, the handler's own length survived compression", cl)
	}
	if vary := resp.Header.Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", vary)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(plain) <= len(body) {
		t.Fatalf("compressed answer is %d bytes, want more than %d", len(plain), len(body))
	}
}

// TestGzipLeavesBinaryAlone: pictures, video and streams are answered byte for
// byte even when the client takes gzip.
func TestGzipLeavesBinaryAlone(t *testing.T) {
	for _, contentType := range []string{"image/jpeg", "video/mp4", "text/event-stream", "", "application/octet-stream"} {
		t.Run(contentType, func(t *testing.T) {
			payload := []byte(strings.Repeat("x", 1000))
			srv := gzipHandlerServer(func(w http.ResponseWriter, r *http.Request) {
				if contentType != "" {
					w.Header().Set("Content-Type", contentType)
				}
				w.Write(payload)
			})
			defer srv.Close()
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			resp, err := http.DefaultTransport.RoundTrip(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if enc := resp.Header.Get("Content-Encoding"); enc != "" {
				t.Fatalf("Content-Encoding = %q, want none", enc)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(got) != string(payload) {
				t.Fatalf("body changed: %d bytes, want %d", len(got), len(payload))
			}
		})
	}
}

// TestGzipHonorsRefusalAndRange: a client that refuses gzip, and a range
// request, get the plain answer.
func TestGzipHonorsRefusalAndRange(t *testing.T) {
	srv := gzipHandlerServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"a":"` + strings.Repeat("b", 600) + `"}`))
	})
	defer srv.Close()

	for _, c := range []struct {
		name    string
		headers map[string]string
	}{
		{"no header", nil},
		{"explicit refusal", map[string]string{"Accept-Encoding": "gzip;q=0"}},
		{"range request", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-10"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultTransport.RoundTrip(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if enc := resp.Header.Get("Content-Encoding"); enc != "" {
				t.Fatalf("Content-Encoding = %q, want none", enc)
			}
		})
	}
}
