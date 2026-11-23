package main

import (
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// webIconServer serves a directory through the real handler chain and returns
// the URL of the /web/ root. The server closes with the test.
func webIconServer(t *testing.T, files map[string]string) string {
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
	srv := httptest.NewServer(withWebUI(http.NotFoundHandler(), dir))
	t.Cleanup(srv.Close)
	return srv.URL + "/web/"
}

// TestWebServessTheMascotAsTheFavicon: the path the bundle asks for answers
// with our icon, an ETag, and a revalidating cache policy — the picture is
// fingerprinted, but the fingerprint comes from the client's build, not from
// our bytes, so it must not be cached as immutable.
func TestWebServesTheMascotAsTheFavicon(t *testing.T) {
	base := webIconServer(t, map[string]string{"index.html": "<html></html>"})

	resp, err := http.Get(base + "favicon.bc8d51405ec040305a87.ico")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/x-icon" {
		t.Errorf("content type = %q, want image/x-icon", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("cache control = %q, want no-cache", got)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag: a revalidating reload would download the icon again")
	}
	if len(body) < 6 || binary.LittleEndian.Uint16(body[2:4]) != 1 {
		t.Fatalf("body is not an icon (%d bytes)", len(body))
	}

	// The validator is what makes the reload cheap.
	req, _ := http.NewRequest(http.MethodGet, base+"favicon.bc8d51405ec040305a87.ico", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("revalidated status = %d, want 304", resp2.StatusCode)
	}
}

// TestWebKeepsTheWordmarkPicture: the banners pair the mark with the "Jellyfin"
// name, so they must still come from the bundle's own file.
func TestWebKeepsTheWordmarkPicture(t *testing.T) {
	const banner = "PNG-WORDMARK-BYTES"
	base := webIconServer(t, map[string]string{
		"banner-light.b113d4d1c6c07fcb73f0.png": banner,
	})

	resp, err := http.Get(base + "banner-light.b113d4d1c6c07fcb73f0.png")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != banner {
		t.Errorf("banner body = %q, want the bundle's own file", body)
	}
}

// TestWebIconHeadHasNoBody: the client and every proxy may ask with HEAD.
func TestWebIconHeadHasNoBody(t *testing.T) {
	base := webIconServer(t, map[string]string{"index.html": "<html></html>"})

	resp, err := http.Head(base + "favicons/touchicon512.png")
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Errorf("HEAD body = %d bytes, want 0", len(body))
	}
	if got := resp.Header.Get("Content-Length"); got != "0" && got == "" {
		t.Errorf("HEAD carried no Content-Length")
	}
}

// TestWebUnknownPathStillFallsThrough: everything this server does not own is
// the file server's business, including its 404.
func TestWebUnknownPathStillFallsThrough(t *testing.T) {
	base := webIconServer(t, map[string]string{"index.html": "<html></html>"})

	resp, err := http.Get(base + "does-not-exist.js")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
