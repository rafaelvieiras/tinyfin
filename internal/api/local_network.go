package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const settingLocalNetworkSubnets = "LocalNetworkSubnets"

// localNetwork is the operator-configured definition of "the local network"
// (the LocalNetworkSubnets member of the network configuration section).
// Loopback is always local. With no subnets configured the private and
// link-local ranges are local; once subnets are configured only they are,
// so a private address outside them is a remote caller (confirmed against
// the reference server).
type localNetwork struct {
	subnets []*net.IPNet
	raw     []string // as configured, for GET
}

func parseSubnets(list []string) (*localNetwork, bool) {
	ln := &localNetwork{raw: []string{}}
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil, false
			}
			bits := 128
			if v4 := ip.To4(); v4 != nil {
				ip, bits = v4, 32
			}
			ln.subnets = append(ln.subnets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			ln.raw = append(ln.raw, s)
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, false
		}
		ln.subnets = append(ln.subnets, n)
		ln.raw = append(ln.raw, s)
	}
	return ln, true
}

// isLocalAddress reports whether ip belongs to the local network. A nil
// address (unparseable peer) counts as local, as it always has.
func (s *Server) isLocalAddress(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() {
		return true
	}
	if ln := s.localNet.Load(); ln != nil && len(ln.subnets) > 0 {
		for _, n := range ln.subnets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func (s *Server) fromLocalNetwork(r *http.Request) bool {
	return s.isLocalAddress(remoteIP(r))
}

func (s *Server) loadLocalNetwork() {
	if v, ok, err := s.store.GetSetting(context.Background(), settingRemoteAccess); err == nil && ok {
		s.remoteAccessOff.Store(v == "false")
	}
	v, ok, err := s.store.GetSetting(context.Background(), settingLocalNetworkSubnets)
	if err != nil || !ok {
		return
	}
	var list []string
	if json.Unmarshal([]byte(v), &list) != nil {
		return
	}
	if ln, ok := parseSubnets(list); ok {
		s.localNet.Store(ln)
	}
}

// networkConfigurationDto is the network section with the reference's
// defaults. Tinyfin acts on EnableRemoteAccess and LocalNetworkSubnets; the
// other members are stored and served back so the Networking page round-trips
// (they do not move the listener: BaseUrl, the ports, HTTPS and the address
// family switches are not applied, admin.config.R4).
type networkConfigurationDto struct {
	BaseUrl                           string   `json:"BaseUrl"`
	EnableHttps                       bool     `json:"EnableHttps"`
	RequireHttps                      bool     `json:"RequireHttps"`
	CertificatePath                   string   `json:"CertificatePath"`
	CertificatePassword               string   `json:"CertificatePassword"`
	InternalHttpPort                  int      `json:"InternalHttpPort"`
	InternalHttpsPort                 int      `json:"InternalHttpsPort"`
	PublicHttpPort                    int      `json:"PublicHttpPort"`
	PublicHttpsPort                   int      `json:"PublicHttpsPort"`
	AutoDiscovery                     bool     `json:"AutoDiscovery"`
	EnableUPnP                        bool     `json:"EnableUPnP"`
	EnableIPv4                        bool     `json:"EnableIPv4"`
	EnableIPv6                        bool     `json:"EnableIPv6"`
	EnableRemoteAccess                bool     `json:"EnableRemoteAccess"`
	LocalNetworkSubnets               []string `json:"LocalNetworkSubnets"`
	LocalNetworkAddresses             []string `json:"LocalNetworkAddresses"`
	KnownProxies                      []string `json:"KnownProxies"`
	IgnoreVirtualInterfaces           bool     `json:"IgnoreVirtualInterfaces"`
	VirtualInterfaceNames             []string `json:"VirtualInterfaceNames"`
	EnablePublishedServerUriByRequest bool     `json:"EnablePublishedServerUriByRequest"`
	PublishedServerUriBySubnet        []string `json:"PublishedServerUriBySubnet"`
	RemoteIPFilter                    []string `json:"RemoteIPFilter"`
	IsRemoteIPFilterBlacklist         bool     `json:"IsRemoteIPFilterBlacklist"`
}

const settingNetworkConfig = "NetworkConfiguration"

func defaultNetworkConfiguration() networkConfigurationDto {
	return networkConfigurationDto{
		InternalHttpPort: 8096, InternalHttpsPort: 8920, PublicHttpPort: 8096, PublicHttpsPort: 8920,
		AutoDiscovery: true, EnableIPv4: true, EnableRemoteAccess: true,
		LocalNetworkSubnets: []string{}, LocalNetworkAddresses: []string{}, KnownProxies: []string{},
		IgnoreVirtualInterfaces: true, VirtualInterfaceNames: []string{"veth"},
		PublishedServerUriBySubnet: []string{}, RemoteIPFilter: []string{},
	}
}

func (n *networkConfigurationDto) nonNil() {
	for _, p := range []*[]string{&n.LocalNetworkSubnets, &n.LocalNetworkAddresses, &n.KnownProxies,
		&n.VirtualInterfaceNames, &n.PublishedServerUriBySubnet, &n.RemoteIPFilter} {
		if *p == nil {
			*p = []string{}
		}
	}
}

// handleNetworkConfigurationGet is GET /System/Configuration/network (any
// signed-in user): the stored section, with the two members Tinyfin acts on
// read from their live state.
func (s *Server) handleNetworkConfigurationGet(w http.ResponseWriter, r *http.Request) {
	out := defaultNetworkConfiguration()
	s.loadSection(r.Context(), settingNetworkConfig, &out)
	out.EnableUPnP = false
	out.EnableRemoteAccess = true
	if v, ok, err := s.store.GetSetting(r.Context(), settingRemoteAccess); err == nil && ok {
		out.EnableRemoteAccess = v == "true"
	}
	out.LocalNetworkSubnets = []string{}
	if ln := s.localNet.Load(); ln != nil {
		out.LocalNetworkSubnets = ln.raw
	}
	out.nonNil()
	writeJSON(w, http.StatusOK, out)
}

// handleNetworkConfigurationPost is POST /System/Configuration/network
// (administrator only). The section is replaced as a whole: members left
// out fall back to their defaults. An unparseable subnet answers 400 and
// changes nothing.
func (s *Server) handleNetworkConfigurationPost(w http.ResponseWriter, r *http.Request) {
	body := defaultNetworkConfiguration()
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	body.nonNil()
	ln, ok := parseSubnets(body.LocalNetworkSubnets)
	if !ok {
		writeInvalidBody(w)
		return
	}
	body.EnableUPnP = false
	raw, _ := json.Marshal(body)
	if err := s.store.SetSetting(r.Context(), settingNetworkConfig, string(raw)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := s.store.SetSetting(r.Context(), settingRemoteAccess, strconv.FormatBool(body.EnableRemoteAccess)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.remoteAccessOff.Store(!body.EnableRemoteAccess)
	subnets, _ := json.Marshal(ln.raw)
	if err := s.store.SetSetting(r.Context(), settingLocalNetworkSubnets, string(subnets)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.localNet.Store(ln)
	w.WriteHeader(http.StatusNoContent)
}

// remoteAccessGated reports whether r must be refused because remote access
// is off and the caller is outside the local network. The static web client
// ("/", "/web/...", "/robots.txt") stays reachable.
func (s *Server) remoteAccessGated(r *http.Request) bool {
	if !s.remoteAccessOff.Load() || s.fromLocalNetwork(r) {
		return false
	}
	p := r.URL.Path
	return !(p == "/" || p == "/robots.txt" || p == "/web" || strings.HasPrefix(p, "/web/"))
}
