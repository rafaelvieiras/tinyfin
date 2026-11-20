package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// webUIServer serves a directory the way the real binary does and returns the
// URL of one file in it.
func webUIServer(t *testing.T, files map[string]string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return httptest.NewServer(withWebUICache(http.FileServer(http.Dir(dir)))), dir
}

// TestWebUICacheHeaders: a bundle the build versioned is cached forever, and the
// entry point is always revalidated — otherwise a new build would never be seen.
func TestWebUICacheHeaders(t *testing.T) {
	srv, _ := webUIServer(t, map[string]string{
		"index.html":              "<html></html>",
		"main.jellyfin.bundle.js": strings.Repeat("var x = 1;\n", 200),
		"manifest.json":           "{}",
	})
	defer srv.Close()

	cases := []struct {
		path      string
		wantCache string
	}{
		{"/main.jellyfin.bundle.js?143c1a38cac69209db68", "public, max-age=31536000, immutable"},
		{"/index.html", "no-cache"},
		{"/", "no-cache"},
		{"/manifest.json", "no-cache"},
	}
	for _, c := range cases {
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Cache-Control"); got != c.wantCache {
			t.Errorf("%s: Cache-Control = %q, want %q", c.path, got, c.wantCache)
		}
	}
}

// TestWebUICompressesText: the bundle comes back gzipped and decodes to the
// file, and a range request is answered as a range.
func TestWebUICompressesText(t *testing.T) {
	body := strings.Repeat("var x = 1;\n", 500)
	srv, _ := webUIServer(t, map[string]string{"main.jellyfin.bundle.js": body})
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/main.jellyfin.bundle.js?v=1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(plain) != body {
		t.Fatalf("answer decodes to %d bytes, want %d", len(plain), len(body))
	}

	rangeReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/main.jellyfin.bundle.js?v=1", nil)
	rangeReq.Header.Set("Accept-Encoding", "gzip")
	rangeReq.Header.Set("Range", "bytes=0-9")
	rangeResp, err := http.DefaultTransport.RoundTrip(rangeReq)
	if err != nil {
		t.Fatalf("range request: %v", err)
	}
	defer rangeResp.Body.Close()
	if enc := rangeResp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("range answer is %q encoded, want plain bytes", enc)
	}
	if rangeResp.StatusCode != http.StatusPartialContent {
		t.Errorf("range status = %d, want 206", rangeResp.StatusCode)
	}
}

// TestWebUIPicturesKeepTheirBytes: a picture under /web/ is not compressed.
func TestWebUIPicturesKeepTheirBytes(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 400)
	srv, _ := webUIServer(t, map[string]string{"icon.png": png})
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/icon.png?v=1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q on a picture, want none", enc)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != png {
		t.Errorf("picture bytes changed: %d, want %d", len(got), len(png))
	}
}
