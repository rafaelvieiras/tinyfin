# The Networking page: what each control does, and client discovery

The dashboard's Networking page (`#/dashboard/networking`) is a form over one
object: `GET /System/Configuration/network` fills it, `POST
/System/Configuration/network` replaces the whole section. The page also loads
`GET /ConfigurationPages?enableInMainMenu=true` for its sidebar entry. Every
control on it writes exactly one member of that object; the save sends the
object back complete.

This document maps that object member by member onto behavior — what the
reference server does when the value changes, what Tinyfin does today, and what
is left. It also records the client-discovery protocol (the one member of the
page that is not HTTP at all), which Tinyfin now implements.

Everything below was measured, not inferred from the reference's source: a
reference server run as a throwaway container, its `/System/Info/Public` and
UDP replies captured, a broadcast sent on a real LAN, and the official client
SDK read for the client half. Where a value's effect could not be measured, the
row says so instead of guessing.

## The object

| Member | Reference: does it move anything? | Tinyfin today | Status |
|---|---|---|---|
| `BaseUrl` | Yes. HTTP is served under that path: a request without the prefix is answered `302` to the prefixed path, and the discovery reply appends it. | Stored, echoed. Not enforced on HTTP. | Echo only |
| `EnableHttps` | Yes, with a certificate: an HTTPS listener appears on `InternalHttpsPort`. Without one it stays down (measured: HTTPS never answered, HTTP kept serving). | Stored, echoed. | Echo only |
| `RequireHttps` | With HTTPS up, redirects HTTP to it. | Stored, echoed. | Echo only |
| `CertificatePath` / `CertificatePassword` | Loads a PKCS#12 the operator supplies. | Stored, echoed. | Echo only |
| `InternalHttpPort` | Yes: the HTTP listener moves (measured: set `9300`, the server bound `9300`), and the discovery reply announces it. | Stored, echoed. The listener comes from `-addr`. | Echo only |
| `InternalHttpsPort` | The HTTPS listener's port, once a certificate is configured. | Stored, echoed. | Echo only |
| `PublicHttpPort` / `PublicHttpsPort` | The port published to the outside, used by the published-URI and UPnP paths. Not what discovery announces (measured: `PublicHttpPort` changed nothing in the reply). | Stored, echoed. | Echo only |
| `AutoDiscovery` | Yes: with `false` the UDP responder stops answering (measured with a control run: `true` again resumes). | **Acts on it now** — the UDP responder reads it per datagram. | Works |
| `EnableUPnP` | Maps the published port through UPnP/IGD. | Forced `false`. | Not implemented |
| `EnableIPv4` / `EnableIPv6` | Which address families the server binds and advertises. | Stored and echoed; the discovery responder honors both when choosing the address to advertise. | Partial |
| `EnableRemoteAccess` | `false` refuses callers outside the local network with `503`, administrators included (`wire.network.R1`). | **Acts on it.** | Works |
| `LocalNetworkSubnets` | Defines "local network"; with any entry configured, private addresses outside them are remote (`wire.network.R1`). | **Acts on it.** | Works |
| `LocalNetworkAddresses` | Additional addresses treated as local, on top of the subnets. | Stored, echoed. | Echo only |
| `KnownProxies` | Addresses whose `X-Forwarded-For`/`X-Forwarded-Proto` headers are trusted, which is how the real client address survives a reverse proxy. | Stored, echoed. | Echo only |
| `IgnoreVirtualInterfaces` / `VirtualInterfaceNames` | Excludes interfaces (container bridges, VPN tunnels) from the addresses the server advertises. | **Acts on it** for the discovery address: a matching name is skipped. Matching is a case-insensitive prefix test, which is Tinyfin's rule — the reference's was not measured. | Partial |
| `EnablePublishedServerUriByRequest` | Uses `X-Forwarded-Host`/`-Proto` from a known proxy as the server's published address. | Stored, echoed. | Echo only |
| `PublishedServerUriBySubnet` | Per-subnet override of the published address (`<cidr>=<uri>` entries). | Stored, echoed. | Echo only |
| `RemoteIPFilter` / `IsRemoteIPFilterBlacklist` | Callers whose address matches the list are the only ones allowed (whitelist) or the only ones refused (blacklist). | Stored, echoed. | Echo only |

"Echo only" is not nothing: the page round-trips, so the form never loses a
value and a client reading the section back sees what it wrote. It is also not
the same as working — the value changes no behavior. `admin.config.R4` records
the round-trip contract; this document records what each member is supposed to
move.

## Client discovery (implemented)

Without discovery, every Jellyfin client asks the user to type the server's
address. With it, the client broadcasts one datagram on the local network and
offers whatever answers, with the server's name.

Measured against a reference server:

- **Port 7359/UDP**, request/response only. The server never announces itself
  on its own, and there is no mDNS record for it (a service browse of the same
  LAN showed none).
- **Request**: the 21 bytes `who is JellyfinServer?`. Surrounding whitespace and
  case are tolerated. The Emby spelling (`who is EmbyServer?`), an empty packet
  and anything else get **no reply**.
- **Reply**, one datagram sent from the same port:

  ```json
  {"Address":"http://<host>:<port>/<baseUrl>","Id":"<server id>","Name":"<server name>","EndpointAddress":null}
  ```

  `Address` is built from `InternalHttpPort` and `BaseUrl` (measured: set the
  internal port to `9300` with `BaseUrl /jf` and the reply became
  `http://<host>:9300/jf`). The scheme stays `http` even with `EnableHttps`
  on — that run had no certificate, so the HTTPS case is unverified rather
  than settled. `EndpointAddress` is always `null`; a client overwrites it with
  the address the reply came from.
- **`Name`** is the server name from the dashboard, or the machine's hostname
  before the wizard sets one.
- **`AutoDiscovery`** gates the answers.

The official client half (`jellyfin-sdk-kotlin`'s local discovery) sends one
packet to `255.255.255.255`, reads with a 500 ms socket timeout, takes at most
15 servers, drops duplicates by `Id`, and treats the datagram's source address
as the server's address.

### What Tinyfin does

`internal/api/discovery.go` binds one UDP socket (`-discovery-addr`, default
`:7359`) and answers the request with the same four members. Details that are
decisions, not accidents:

- The advertised address is this host's own address on **the asking client's
  subnet**, falling back to the first usable one, because a host with several
  interfaces would otherwise hand out an address the client cannot route to.
- Loopback and link-local addresses are never advertised; `EnableIPv4`,
  `EnableIPv6`, `IgnoreVirtualInterfaces` and `VirtualInterfaceNames` filter
  the candidates.
- The port announced is the one this process actually serves HTTP on, taken
  from `-addr`; only when the caller does not know it does the section's
  `InternalHttpPort` apply. The reference announces its `InternalHttpPort`
  setting unconditionally, which is the same value there because the setting
  moves the listener. Here it does not (the port is a flag), so announcing the
  setting could send a client to a port nothing answers on — `DIV-38` in
  [`13`](13-divergences.md).
- `AutoDiscovery` is read per datagram, so turning it off applies immediately.
- A bind failure is logged once and disables the responder; it never stops the
  HTTP server. A second instance on one host therefore loses discovery, not
  its API.
- The socket family is IPv4, which is what clients broadcast on. Advertising an
  IPv6 address is still possible (`EnableIPv6`) for a client that reaches this
  server over IPv6 by other means, but nothing broadcasts over IPv6.

## What is left, and why it is not in this change

Each of these is a decision with its own blast radius, and conflating them with
discovery would make the change un-reviewable:

1. **HTTPS and certificates** (`EnableHttps`, `RequireHttps`,
   `CertificatePath`, `CertificatePassword`): a second listener, PKCS#12
   loading, and a redirect rule. The page's control set assumes all four move
   together.
2. **Moving the listener from the section** (`InternalHttpPort`,
   `InternalHttpsPort`): today the port comes from `-addr`, so the section
   cannot restart the listener without a re-bind path and an operator-visible
   failure mode.
3. **`BaseUrl` enforcement**: a path prefix on every route, plus the `302`
   redirect for a request that omits it.
4. **Proxy trust** (`KnownProxies`, `EnablePublishedServerUriByRequest`): the
   caller's real address behind a reverse proxy. This changes what
   `EnableRemoteAccess`, `LocalNetworkSubnets` and the remote-address filter
   see, so it needs its own scenarios.
5. **`RemoteIPFilter`/`IsRemoteIPFilterBlacklist`**: an allow/deny gate in
   front of every route.
6. **`LocalNetworkAddresses`**: extra local addresses.
7. **`EnableUPnP`**: a UPnP/IGD client; the reference maps the published port
   with it. Tinyfin keeps answering `false`.
8. **`PublicHttpPort`/`PublicHttpsPort` and `PublishedServerUriBySubnet`**: the
   published-address path, which only matters once (4) exists.

## Verifying

```bash
# The responder, without a Jellyfin client:
printf 'who is JellyfinServer?' | timeout 2 nc -u -w1 <server> 7359

# The whole flow: a broadcast on the LAN, answered by name.
python3 - <<'PY'
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1); s.settimeout(2)
s.sendto(b"who is JellyfinServer?", ("255.255.255.255", 7359))
print(s.recvfrom(2048)[0].decode())
PY
```

Discovery is UDP, not HTTP, so the conformance runner cannot assert it: the
rules live in `docs/contract/specs/platform.md` and the tests in
`internal/api/discovery_test.go`.
