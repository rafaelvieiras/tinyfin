package imagefetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// pngBytes is the smallest body that sniffs as an image: a GIF header plus a
// filler byte. DetectContentType keys on the magic number, not on a decoder,
// so this is enough to exercise the "is it artwork" half of the guard.
var pngBytes = []byte("GIF89a" + strings.Repeat("\x00", 32))

// serve starts a test server, which is a loopback address: every test that
// wants to reach it therefore also exercises the allowlist, the same way a
// deployment whose artwork lives on a NAS does.
func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// allowLoopback admits the test server's own address.
func allowLoopback(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	return []string{hostOf(t, srv.URL)}
}

func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	i := strings.LastIndex(rawURL, "//")
	if i < 0 {
		t.Fatalf("no // in %q", rawURL)
	}
	return strings.SplitN(rawURL[i+2:], ":", 2)[0]
}

func TestFetchRefusesAnAddressThatIsNotAllowlisted(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
	})
	// The default allowlist admits nothing on loopback, and the test server
	// is on loopback.
	f := New(nil)
	_, err := f.Fetch(context.Background(), srv.URL+"/actor.png")
	if !errors.Is(err, ErrBlockedAddr) {
		t.Fatalf("fetch of a loopback URL without an allowlist entry: got %v, want ErrBlockedAddr", err)
	}
}

func TestFetchAllowsAnAllowlistedHost(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
	})
	f := New(allowLoopback(t, srv))
	img, err := f.Fetch(context.Background(), srv.URL+"/actor.png")
	if err != nil {
		t.Fatalf("fetch with the host allowlisted: %v", err)
	}
	if string(img.Bytes) != string(pngBytes) {
		t.Fatalf("body: got %d bytes, want %d", len(img.Bytes), len(pngBytes))
	}
	if img.MIME != "image/gif" {
		t.Fatalf("MIME: got %q, want image/gif", img.MIME)
	}
}

func TestFetchAllowsAnAllowlistedRange(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
	})
	// 127.0.0.0/8 covers the test server whatever address it picked.
	f := New([]string{"127.0.0.0/8"})
	if _, err := f.Fetch(context.Background(), srv.URL+"/actor.png"); err != nil {
		t.Fatalf("fetch with the range allowlisted: %v", err)
	}
}

func TestFetchRefusesANonHTTPScheme(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com/x", "ftp://example.com/x"} {
		f := New([]string{"example.com"})
		if _, err := f.Fetch(context.Background(), raw); !errors.Is(err, ErrScheme) {
			t.Fatalf("%s: got %v, want ErrScheme", raw, err)
		}
	}
}

func TestFetchRefusesANonImageBody(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg") // a lie; the body decides
		w.Write([]byte(`{"error":"not an image"}`))
	})
	f := New(allowLoopback(t, srv))
	_, err := f.Fetch(context.Background(), srv.URL+"/actor.png")
	if !errors.Is(err, ErrNotImage) {
		t.Fatalf("got %v, want ErrNotImage", err)
	}
}

func TestFetchRefusesAnEmptyBody(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	f := New(allowLoopback(t, srv))
	if _, err := f.Fetch(context.Background(), srv.URL+"/actor.png"); !errors.Is(err, ErrNotImage) {
		t.Fatalf("got %v, want ErrNotImage", err)
	}
}

func TestFetchRefusesABodyOverTheCeiling(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
		w.Write(make([]byte, 4096))
	})
	f := New(allowLoopback(t, srv), WithMaxBytes(64))
	_, err := f.Fetch(context.Background(), srv.URL+"/actor.png")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestFetchReportsANon2xxStatus(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	f := New(allowLoopback(t, srv))
	if _, err := f.Fetch(context.Background(), srv.URL+"/actor.png"); !errors.Is(err, ErrStatus) {
		t.Fatalf("got %v, want ErrStatus", err)
	}
}

func TestFetchRechecksEachRedirect(t *testing.T) {
	// A public URL that redirects to a non-public one is the classic way
	// round a check that only looks at the URL the caller passed.
	redirector := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	// Only the redirector is allowlisted, so its hop passes and the hop it
	// points at does not.
	f := New(allowLoopback(t, redirector))
	_, err := f.Fetch(context.Background(), redirector.URL+"/actor.png")
	if !errors.Is(err, ErrBlockedAddr) {
		t.Fatalf("got %v, want ErrBlockedAddr for the redirect target", err)
	}
}

func TestFetchAllowsAnAllowlistedHostName(t *testing.T) {
	// A name entry is honoured even though the address behind it is
	// loopback: the operator allowed that host, and the socket check must
	// not overrule the name. This is the shape a home deployment has when
	// artwork lives on a NAS.
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes)
	})
	f := New([]string{"localhost"})
	raw := "http://localhost:" + srv.URL[strings.LastIndex(srv.URL, ":")+1:] + "/actor.png"
	if _, err := f.Fetch(context.Background(), raw); err != nil {
		t.Fatalf("fetch through an allowlisted host name: %v", err)
	}
}

func TestFetchRefusesAFileRedirect(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "file:///etc/passwd")
		w.WriteHeader(http.StatusFound)
	})
	f := New(allowLoopback(t, srv))
	if _, err := f.Fetch(context.Background(), srv.URL+"/actor.png"); !errors.Is(err, ErrScheme) {
		t.Fatalf("got %v, want ErrScheme", err)
	}
}

func TestFetchHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	f := New(allowLoopback(t, srv))
	if _, err := f.Fetch(ctx, srv.URL+"/actor.png"); err == nil {
		t.Fatal("a cancelled fetch returned no error")
	}
}

func TestGuardOfParsesEachEntryKind(t *testing.T) {
	g := GuardOf([]string{" NAS.example.com ", "192.0.2.10", "10.0.0.0/8", "", "192.0.2.11/32"})
	if !g.AllowsHost("nas.example.com") {
		t.Error("host name entry not matched (entries are case-insensitive)")
	}
	if g.AllowsHost("other.example.com") {
		t.Error("unrelated host matched")
	}
	for _, tc := range []struct {
		addr    string
		blocked bool
	}{
		{"192.0.2.10", false},     // single-address entry
		{"192.0.2.11", false},     // /32 entry
		{"10.1.2.3", false},       // covered by the /8
		{"11.1.2.3", false},       // public, and no entry covers it
		{"127.0.0.1", true},       // loopback, no entry
		{"169.254.169.254", true}, // link-local: the metadata endpoint
	} {
		addr := mustAddr(t, tc.addr)
		if got := g.blocked(addr); got != tc.blocked {
			t.Errorf("%s: blocked=%v, want %v", tc.addr, got, tc.blocked)
		}
	}
}

func TestNonPublicCoversTheRangesNetipDoesNot(t *testing.T) {
	// Addresses netip's own classifiers already report are checked by
	// construction; these are the ones it does not, and each one is a
	// plausible target for a forged URL.
	for _, raw := range []string{
		"100.64.0.1",      // carrier-grade NAT
		"198.18.0.1",      // benchmarking
		"240.0.0.1",       // reserved
		"255.255.255.255", // broadcast
		"192.0.0.1",       // IETF protocol assignments
		"2001:db8::1",     // documentation
	} {
		if !nonPublic(mustAddr(t, raw)) {
			t.Errorf("%s reported as public", raw)
		}
	}
	// And a genuinely public address stays public, so the guard is not just
	// refusing everything.
	for _, raw := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		if nonPublic(mustAddr(t, raw)) {
			t.Errorf("%s reported as non-public", raw)
		}
	}
}

func mustAddr(t *testing.T, raw string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return addr
}
