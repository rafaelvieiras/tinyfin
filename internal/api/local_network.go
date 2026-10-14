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

type networkConfigurationDto struct {
	LocalNetworkSubnets []string `json:"LocalNetworkSubnets"`
	// EnableRemoteAccess is stored by POST /Startup/RemoteAccess and the
	// network section (default true). When false, a caller outside the
	// local network is answered 503 on every route except the web client's
	// static files (wire.network.R1).
	EnableRemoteAccess bool `json:"EnableRemoteAccess"`
	// EnableUPnP is always false: automatic port mapping is not implemented
	// (startup.remote.R2).
	EnableUPnP bool `json:"EnableUPnP"`
}

// handleNetworkConfigurationGet is GET /System/Configuration/network
// (administrator only): the part of the reference's network configuration
// section Tinyfin honors.
func (s *Server) handleNetworkConfigurationGet(w http.ResponseWriter, r *http.Request) {
	out := networkConfigurationDto{LocalNetworkSubnets: []string{}, EnableRemoteAccess: true}
	if v, ok, err := s.store.GetSetting(r.Context(), settingRemoteAccess); err == nil && ok {
		out.EnableRemoteAccess = v == "true"
	}
	if ln := s.localNet.Load(); ln != nil {
		out.LocalNetworkSubnets = ln.raw
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNetworkConfigurationPost is POST /System/Configuration/network
// (administrator only). Absent and unknown members are left alone; an
// unparseable subnet answers 400 and changes nothing.
func (s *Server) handleNetworkConfigurationPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LocalNetworkSubnets *[]string `json:"LocalNetworkSubnets"`
		EnableRemoteAccess  *bool     `json:"EnableRemoteAccess"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	if body.EnableRemoteAccess != nil {
		if err := s.store.SetSetting(r.Context(), settingRemoteAccess, strconv.FormatBool(*body.EnableRemoteAccess)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.remoteAccessOff.Store(!*body.EnableRemoteAccess)
	}
	if body.LocalNetworkSubnets != nil {
		ln, ok := parseSubnets(*body.LocalNetworkSubnets)
		if !ok {
			writeInvalidBody(w)
			return
		}
		raw, _ := json.Marshal(ln.raw)
		if err := s.store.SetSetting(r.Context(), settingLocalNetworkSubnets, string(raw)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.localNet.Store(ln)
	}
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
