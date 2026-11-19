// Package imagefetch downloads remote artwork for a media library through a
// single guarded path. Both callers that fetch a URL the server did not
// choose itself go through it: the scanner, for the poster/backdrop URLs a
// metadata provider answers with, and the API, for the actor photo an NFO file
// names in <actor><thumb>.
//
// That second input is the reason this package exists. An NFO lives inside the
// media library — usually on a network share, usually writable by whoever
// maintains the library — so the URL in it is untrusted data that makes the
// server issue an HTTP request. Left unguarded that is a server-side request
// forgery primitive: it reaches loopback, the container network, cloud
// instance metadata and anything else the server can route to. The reference
// server performs exactly that request (recorded as a divergence, docs/13),
// so this is deliberately stricter than the thing it reimplements.
//
// The guard has four parts, and a fetch is refused when any of them says no:
//
//   - the URL scheme must be http or https, so file:, gopher: and friends
//     never reach a transport;
//   - every address the connection is actually made to must be a public one,
//     unless the operator allowlisted it. This is checked on the socket, in
//     dialer.Control, and not only on the URL's hostname, so a name that
//     resolves to a private address — or resolves to a public one first and a
//     private one later, the DNS-rebinding case — is refused too;
//   - the body is bounded, so a hostile or broken URL cannot fill the disk or
//     the heap (a scanner that trusted io.ReadAll on a provider URL could be
//     made to read gigabytes);
//   - the body must sniff as an image, so a URL that answers with JSON, HTML or
//     an error string is not written out as artwork.
//
// The allowlist is what keeps a legitimate deployment working: a library whose
// artwork is served by something on the local network — a NAS, a reverse proxy,
// a self-hosted metadata cache — names it in TINYFIN_IMAGE_HOSTS and the
// address check passes for that host only.
package imagefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// DefaultMaxBytes is the ceiling on a downloaded image. Posters and actor
// photos are tens to hundreds of kilobytes; 8 MiB leaves room for an
// uncompressed provider original while keeping one hostile URL from being
// able to exhaust the heap or the image directory.
const DefaultMaxBytes int64 = 8 << 20

// DefaultTimeout bounds one download, redirects included.
const DefaultTimeout = 15 * time.Second

// maxRedirects bounds a redirect chain; each hop is re-checked against the
// same rules, since a public URL may redirect to a private one.
const maxRedirects = 5

// Errors callers distinguish. Everything else a fetch can return (a dial
// error, a timeout, a TLS failure) is an ordinary error and is treated the
// same way: the image is missing, not the request.
var (
	// ErrScheme reports a URL whose scheme is not http or https.
	ErrScheme = errors.New("imagefetch: scheme must be http or https")
	// ErrBlockedAddr reports a target the allowlist does not cover: a
	// loopback, private, link-local, CGNAT or otherwise non-public address.
	ErrBlockedAddr = errors.New("imagefetch: address is not public and is not allowed")
	// ErrTooLarge reports a body over the size ceiling.
	ErrTooLarge = errors.New("imagefetch: image exceeds the size ceiling")
	// ErrNotImage reports a body that does not sniff as an image.
	ErrNotImage = errors.New("imagefetch: body is not an image")
	// ErrStatus reports a non-2xx answer.
	ErrStatus = errors.New("imagefetch: unexpected status")
)

// Image is a downloaded body and what it is.
type Image struct {
	// Bytes is the body, at most the fetcher's ceiling.
	Bytes []byte
	// MIME is the sniffed media type, always an image/... value.
	MIME string
	// URL is the URL that served the body: the original one unless a
	// redirect was followed.
	URL string
}

// Fetcher downloads images behind the guard. It is safe for concurrent use:
// the underlying transport pools connections and the guard is read-only.
type Fetcher struct {
	guard    *Guard
	client   *http.Client
	maxBytes int64
}

// Option adjusts a Fetcher; the defaults are what production wants.
type Option func(*Fetcher)

// WithMaxBytes overrides the body ceiling (tests use a small one).
func WithMaxBytes(n int64) Option { return func(f *Fetcher) { f.maxBytes = n } }

// WithTimeout overrides the whole-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(f *Fetcher) { f.client.Timeout = d }
}

// New returns a Fetcher that refuses every non-public address except the ones
// allow admits. An allowlist entry that does not parse as an IP or a CIDR is
// treated as a host name; a bad entry is reported by GuardOf.
func New(allow []string, opts ...Option) *Fetcher {
	guard := GuardOf(allow)
	f := &Fetcher{guard: guard, maxBytes: DefaultMaxBytes}
	f.client = &http.Client{
		Timeout:   DefaultTimeout,
		Transport: guard.transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("imagefetch: more than %d redirects", maxRedirects)
			}
			// The address check runs again for the hop's own connection
			// (dialer.Control); the scheme is checked here because a
			// redirect is the only way a request's own URL changes.
			if !isHTTP(req.URL.Scheme) {
				return fmt.Errorf("%w: %s", ErrScheme, req.URL)
			}
			return nil
		},
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Guard exposes the fetcher's allowlist, so a caller that needs to ask "would
// these rules admit this host" does not keep a second copy of them.
func (f *Fetcher) Guard() *Guard { return f.guard }

// Fetch downloads rawURL. The returned error wraps ErrScheme, ErrBlockedAddr,
// ErrTooLarge, ErrNotImage or ErrStatus when one of those is the cause, so a
// caller can distinguish "this URL will never work" from "the network is
// down" for a negative cache.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Image, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Image{}, fmt.Errorf("imagefetch: parse %q: %w", rawURL, err)
	}
	if !isHTTP(u.Scheme) {
		return Image{}, fmt.Errorf("%w: %q", ErrScheme, u.Scheme)
	}
	if u.Host == "" {
		return Image{}, fmt.Errorf("imagefetch: %q has no host", rawURL)
	}
	// A literal address in the URL is refused before a connection is even
	// attempted, so the common "point it at 127.0.0.1" attempt costs one
	// comparison; every other case is caught on the socket below.
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && f.guard.blocked(addr) {
		return Image{}, fmt.Errorf("%w: %s", ErrBlockedAddr, u.Hostname())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Image{}, err
	}
	// Some CDNs answer a request with no User-Agent with 403.
	req.Header.Set("User-Agent", "tinyfin")
	resp, err := f.client.Do(req)
	if err != nil {
		return Image{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Image{}, fmt.Errorf("%w: %d for %s", ErrStatus, resp.StatusCode, u)
	}

	limit := f.maxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Image{}, err
	}
	if int64(len(body)) > limit {
		return Image{}, fmt.Errorf("%w: %d bytes", ErrTooLarge, limit)
	}
	if len(body) == 0 {
		return Image{}, fmt.Errorf("%w: empty body", ErrNotImage)
	}
	mime := http.DetectContentType(body)
	if !strings.HasPrefix(mime, "image/") {
		return Image{}, fmt.Errorf("%w: %s", ErrNotImage, mime)
	}
	final := u.String()
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return Image{Bytes: body, MIME: mime, URL: final}, nil
}

// isHTTP reports whether scheme is one this package is willing to speak.
func isHTTP(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

// Guard decides which addresses a fetch may connect to. The zero value allows
// nothing but public addresses, which is the production default; build one
// with GuardOf.
type Guard struct {
	// hosts are allowlisted host names, matched case-insensitively against
	// the URL's host.
	hosts map[string]bool
	// addrs are allowlisted single addresses.
	addrs map[netip.Addr]bool
	// nets are allowlisted ranges.
	nets []netip.Prefix
}

// GuardOf parses an allowlist. Each entry is a host name (nas.example.com), an
// IP (192.0.2.10) or a CIDR (192.0.2.0/24, fd00::/8).
func GuardOf(allow []string) *Guard {
	g := &Guard{hosts: map[string]bool{}, addrs: map[netip.Addr]bool{}}
	for _, raw := range allow {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			g.nets = append(g.nets, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			g.addrs[addr.Unmap()] = true
			continue
		}
		g.hosts[strings.ToLower(entry)] = true
	}
	return g
}

// AllowsHost reports whether host is named in the allowlist.
func (g *Guard) AllowsHost(host string) bool {
	if g == nil {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return g.hosts[host]
}

// blocked reports whether addr must not be connected to: either it is
// non-public and no allowlist entry covers it, or it is non-public at all.
func (g *Guard) blocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if g != nil {
		if g.addrs[addr] {
			return false
		}
		for _, prefix := range g.nets {
			if prefix.Contains(addr) {
				return false
			}
		}
	}
	return nonPublic(addr)
}

// nonPublic reports whether addr is an address a server must not be made to
// fetch on someone else's behalf. The list is the union of what Go's netip
// classifiers call local or private plus the ranges those classifiers miss but
// that are still not public endpoints: carrier-grade NAT, benchmarking,
// reserved and broadcast space.
func nonPublic(addr netip.Addr) bool {
	switch {
	case !addr.IsValid(),
		addr.IsUnspecified(),
		addr.IsLoopback(),
		addr.IsPrivate(),
		addr.IsLinkLocalUnicast(),
		addr.IsLinkLocalMulticast(),
		addr.IsInterfaceLocalMulticast(),
		addr.IsMulticast():
		return true
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// reservedPrefixes are the non-public ranges netip has no classifier for.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes broadcast
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// transport returns an HTTP transport whose dialer refuses a non-public
// address at connect time. Checking the socket rather than the URL is what
// makes the guard cover DNS rebinding: the name was public when it was
// resolved for the allowlist check, and private by the time the connection is
// made.
//
// A host named in the allowlist is dialled without the address check, because
// the operator has already said that name's target is fine — a NAS whose
// address changes, a reverse proxy on a private range. Everything else is
// checked against the address the connection is actually made to.
func (g *Guard) transport() *http.Transport {
	strict := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if g.blocked(addr) {
				return fmt.Errorf("%w: %s", ErrBlockedAddr, addr)
			}
			return nil
		},
	}
	open := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if g.AllowsHost(host) {
				return open.DialContext(ctx, network, address)
			}
			return strict.DialContext(ctx, network, address)
		},
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// A proxy would make the connection go somewhere else than the
		// checked address, defeating the guard; these downloads connect
		// directly.
		Proxy: nil,
	}
}
