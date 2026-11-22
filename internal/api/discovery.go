package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"strconv"
	"strings"
)

// Client discovery (docs/16-networking-page.md, platform.autodiscovery). A
// client that does not know the server's address broadcasts a fixed request
// to UDP port 7359 on the local network; the server answers that datagram
// with its own address, and the client offers the server in its list instead
// of asking the user to type a URL. It is a request/response protocol — the
// server never announces itself on its own, and there is no mDNS service. Both
// halves were measured against a reference server and against the official
// client SDK; the packet shapes below are those measurements, not a guess.
const (
	// discoveryRequest is the whole request payload, byte for byte. The
	// reference accepts it with surrounding whitespace and in any case; it
	// does NOT answer the Emby spelling ("who is EmbyServer?"), nor an
	// empty or unrecognized packet.
	discoveryRequest = "who is JellyfinServer?"

	// defaultDiscoveryPort is the port the reference binds, and the only
	// one its clients broadcast to. It is not a setting there either.
	defaultDiscoveryPort = 7359

	// discoveryReadLimit bounds what one datagram may carry. The request is
	// 21 bytes and a reply is under 200; the limit exists so a hostile
	// packet cannot grow the allocation.
	discoveryReadLimit = 1024
)

// discoveryReply is the answer the reference sends: one compact JSON object,
// keys in this order, EndpointAddress always null (a client fills it in with
// the replier's address). Clients decode it with unknown keys ignored, but the
// four names here are the ones the official SDK reads, so they are the wire
// contract.
type discoveryReply struct {
	Address         string  `json:"Address"`
	ID              string  `json:"Id"`
	Name            string  `json:"Name"`
	EndpointAddress *string `json:"EndpointAddress"`
}

// localAddr is one address this host could advertise, together with the
// interface it belongs to (the network section can exclude interfaces by name)
// and its mask (the reply prefers an address on the asking client's own subnet).
type localAddr struct {
	name string
	ip   net.IP
	mask net.IPMask
}

// interfaceAddrs lists every address of every interface that is up, in the
// order the operating system reports them.
func interfaceAddrs() []localAddr {
	ifis, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]localAddr, 0, len(ifis))
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			var mask net.IPMask
			switch v := a.(type) {
			case *net.IPNet:
				ip, mask = v.IP, v.Mask
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if ip == nil {
				continue
			}
			out = append(out, localAddr{name: ifi.Name, ip: ip, mask: mask})
		}
	}
	return out
}

// isVirtualInterface reports whether an interface name is one the operator
// asked to ignore. Matching is a case-insensitive prefix test, so the default
// list ("veth") also covers veth0d1a2b3, which is the shape a container
// runtime gives them. The reference's own matching rule was not measured, so
// this is Tinyfin's rule and it says so (docs/16).
func isVirtualInterface(name string, ignored []string) bool {
	name = strings.ToLower(name)
	for _, p := range ignored {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// discoveryCandidates keeps the addresses a reply may advertise: up,
// non-loopback, non-link-local, in an address family the operator enabled, on
// an interface the operator did not exclude.
func discoveryCandidates(all []localAddr, cfg networkConfigurationDto) []localAddr {
	out := make([]localAddr, 0, len(all))
	for _, a := range all {
		if a.ip == nil || a.ip.IsLoopback() || a.ip.IsLinkLocalUnicast() || a.ip.IsUnspecified() {
			continue
		}
		if a.ip.To4() != nil {
			if !cfg.EnableIPv4 {
				continue
			}
		} else if !cfg.EnableIPv6 {
			continue
		}
		if cfg.IgnoreVirtualInterfaces && isVirtualInterface(a.name, cfg.VirtualInterfaceNames) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// pickDiscoveryAddr chooses the address to advertise to peer: the first
// candidate on peer's own subnet, since that is the one the client can reach;
// otherwise the first candidate. A host with several interfaces would
// otherwise hand out an address the client has no route to.
func pickDiscoveryAddr(addrs []localAddr, peer net.IP) (net.IP, bool) {
	for _, a := range addrs {
		if a.mask == nil || peer == nil {
			continue
		}
		if a.ip.To4() != nil && peer.To4() == nil {
			continue
		}
		if a.ip.To4() == nil && peer.To4() != nil {
			continue
		}
		if (&net.IPNet{IP: a.ip.Mask(a.mask), Mask: a.mask}).Contains(peer) {
			return a.ip, true
		}
	}
	if len(addrs) > 0 {
		return addrs[0].ip, true
	}
	return nil, false
}

// discoveryAddress builds the reply's Address: scheme://host:port plus the
// configured BaseUrl as a path prefix. The scheme is http, which is what the
// reference announces, including while HTTPS is enabled (measured; no
// certificate was configured in that run, so the HTTPS case is unverified
// rather than decided, docs/16).
//
// The port is the one this server actually listens on. In the reference the
// setting and the listener are the same thing, because the setting moves the
// listener; here the listener comes from the command line, so announcing the
// setting alone could advertise a port nothing answers on (a deployment whose
// API is reached on a port the dashboard never configured is exactly that
// case). listenPort is 0 when the caller does not know it, and then the
// setting applies — divergence DIV-38.
func discoveryAddress(cfg networkConfigurationDto, host net.IP, listenPort int) string {
	port := listenPort
	if port <= 0 {
		port = cfg.InternalHttpPort
	}
	if port <= 0 {
		port = 8096
	}
	addr := "http://" + net.JoinHostPort(host.String(), strconv.Itoa(port))
	if base := strings.TrimSpace(cfg.BaseUrl); base != "" {
		if !strings.HasPrefix(base, "/") {
			base = "/" + base
		}
		addr += strings.TrimSuffix(base, "/")
	}
	return addr
}

// networkConfiguration reads the stored network section, defaults included.
func (s *Server) networkConfiguration(ctx context.Context) networkConfigurationDto {
	cfg := defaultNetworkConfiguration()
	s.loadSection(ctx, settingNetworkConfig, &cfg)
	cfg.nonNil()
	return cfg
}

// autoDiscoveryEnabled is the network section's AutoDiscovery switch, read
// per request so turning it off in the dashboard stops the answers without a
// restart — the reference does the same (measured).
func (s *Server) autoDiscoveryEnabled(ctx context.Context) bool {
	return s.networkConfiguration(ctx).AutoDiscovery
}

// discoveryAddressFor answers "which URL would this server give peer?".
func (s *Server) discoveryAddressFor(peer net.IP) string {
	cfg := s.networkConfiguration(context.Background())
	host, ok := pickDiscoveryAddr(discoveryCandidates(interfaceAddrs(), cfg), peer)
	if !ok {
		return ""
	}
	return discoveryAddress(cfg, host, s.cfg.ListenPort)
}

// DiscoveryAddr reports the address the discovery responder is bound to, or ""
// when it is not listening. Tests use it to find the ephemeral port.
func (s *Server) DiscoveryAddr() string {
	if p := s.discoveryAddr.Load(); p != nil {
		return *p
	}
	return ""
}

// closeDiscovery unbinds the discovery responder so its read loop returns.
func (s *Server) closeDiscovery() {
	if c := s.discoveryConn.Load(); c != nil {
		_ = c.Close()
	}
}

// runDiscovery answers client discovery broadcasts until ctx ends.
//
// A bind failure is logged and disables discovery: the port is a fixed one, so
// a second instance on the same host, or another server already holding it,
// must not stop the rest of this process from serving. The reference has the
// same shape (one fixed port, no setting).
func (s *Server) runDiscovery(ctx context.Context) {
	addr := s.cfg.DiscoveryUDPAddr
	conn, err := net.ListenPacket("udp4", addr)
	if err != nil {
		log.Printf("tinyfin: client discovery disabled, cannot bind %s: %v", addr, err)
		return
	}
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		log.Printf("tinyfin: client discovery disabled, %s is not UDP", addr)
		return
	}
	s.discoveryConn.Store(udp)
	bound := udp.LocalAddr().String()
	s.discoveryAddr.Store(&bound)
	defer func() {
		s.discoveryConn.Store(nil)
		s.discoveryAddr.Store(nil)
		_ = udp.Close()
	}()
	log.Printf("tinyfin: answering client discovery broadcasts on %s (udp)", bound)

	buf := make([]byte, discoveryReadLimit)
	for {
		n, peer, err := udp.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// A closed socket (Shutdown) or an ICMP-induced error must not
			// end the loop.
			continue
		}
		if n == 0 || n > len(buf) {
			continue
		}
		if !discoveryRequestMatches(string(buf[:n])) {
			continue
		}
		if !s.autoDiscoveryEnabled(ctx) {
			continue
		}
		address := s.discoveryAddressFor(peer.IP)
		if address == "" {
			continue
		}
		payload, err := json.Marshal(discoveryReply{
			Address: address,
			ID:      s.cfg.ServerID,
			Name:    s.effectiveServerName(),
		})
		if err != nil {
			continue
		}
		_, _ = udp.WriteToUDP(payload, peer)
	}
}

// discoveryRequestMatches reports whether payload is the discovery request.
// Surrounding whitespace and case are ignored (both measured as accepted by
// the reference); nothing else is.
func discoveryRequestMatches(payload string) bool {
	return strings.EqualFold(strings.TrimSpace(payload), discoveryRequest)
}
