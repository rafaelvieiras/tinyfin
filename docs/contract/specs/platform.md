# Spec: platform subsystems that are not HTTP routes

Wire-level behavior of the parts of a Jellyfin server that a client uses
without talking HTTP to it, verified black-box against a running reference
server and, for the client half, against the official client SDK. Every rule
has a stable ID (`platform.<topic>.R<n>`) that scenarios in
`../scenarios/` reference through `covers` — or, where the conformance runner
cannot express the behavior at all, the rule line carries an `[untestable]`
tag and the Go test that covers it is named. Scope follows `../README.md`.

The rule IDs in this file are `platform.discovery.*`; the page that configures
this behavior is mapped in [`docs/16`](../../16-networking-page.md).

## Discovery

A client that does not know the server's address broadcasts a datagram on the
local network; the server answers it with its own address, and the client
offers the server in its list. The protocol is request/response: the server
never announces itself unprompted, and it publishes no mDNS record.

- **platform.discovery.R1** [untestable: UDP, not an HTTP request — the conformance runner speaks HTTP] The responder listens on UDP port 7359 and answers exactly one request payload, the 21 bytes `who is JellyfinServer?`. Surrounding whitespace and letter case are tolerated; the Emby spelling (`who is EmbyServer?`), an empty datagram and any other payload are ignored — no reply is sent at all. Covered by `TestDiscoveryAnswersTheRequest`, `TestDiscoveryRequestMatching` and `TestDiscoveryIgnoresUnknownPayloads`.
- **platform.discovery.R2** [untestable: UDP, not an HTTP request] The reply is one JSON object sent back to the asking address from the same port, with four members: `Address` (a URL), `Id` (the server id, the same one `PublicSystemInfo.Id` reports), `Name` (the configured server name) and `EndpointAddress`, which is always `null` — a client replaces it with the address the reply arrived from. Covered by `TestDiscoveryAnswersTheRequest`.
- **platform.discovery.R3** [untestable: UDP, not an HTTP request] The network section's `AutoDiscovery` gates the answers and is read per datagram: `false` stops them without a restart, `true` resumes them. A responder that cannot bind its port disables itself and logs; it never stops the HTTP server. Covered by `TestDiscoveryIsGatedByAutoDiscovery`.
- **platform.discovery.R4** [untestable: UDP, not an HTTP request] `Address` is `http://<host>:<InternalHttpPort><BaseUrl>`. The port is the network section's `InternalHttpPort`, not `PublicHttpPort`, and the scheme stays `http` even while `EnableHttps` is on (the reference's HTTPS case is unverified — no certificate was configured when it was measured). `BaseUrl` is appended as a path prefix. Covered by `TestDiscoveryAddressUsesInternalPortAndBaseURL`.
- **platform.discovery.R5** [untestable: UDP, not an HTTP request] The advertised host is an address of this machine that the asking client can reach: an address on the client's own subnet wins, otherwise the first usable one. Loopback and link-local addresses are never advertised, `EnableIPv4`/`EnableIPv6` select the address family, and `IgnoreVirtualInterfaces` with `VirtualInterfaceNames` drops the interfaces named there (case-insensitive prefix match; the reference's own matching rule was not measured, so this is Tinyfin's). Covered by `TestDiscoveryCandidates` and `TestPickDiscoveryAddr`.

Scenario index:

| Rule | Scenario |
|---|---|
| `platform.discovery.R1` | — (untestable, see the rule) |
| `platform.discovery.R2` | — (untestable, see the rule) |
| `platform.discovery.R3` | — (untestable, see the rule) |
| `platform.discovery.R4` | — (untestable, see the rule) |
| `platform.discovery.R5` | — (untestable, see the rule) |

## Known differences

- **The port is a command-line flag, not a setting.** The reference fixes port
  7359 and offers no control for it; Tinyfin's `-discovery-addr` (and
  `TINYFIN_DISCOVERY_ADDR`) exists so a second instance on one host can move
  it, and an empty value turns the responder off. Both are operating concerns,
  not wire behavior: a client only ever dials 7359.
- **A bind failure is survivable.** One fixed port means one instance per host
  can hold it; the instance that loses the race keeps serving HTTP and says so
  in the log.
- **`RequireHttps` is not consulted**, because Tinyfin serves no HTTPS
  listener yet; see `docs/16`'s list of what the Networking page still only
  stores.
