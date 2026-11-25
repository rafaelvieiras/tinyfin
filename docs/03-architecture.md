# 03 — Architecture

## Roles, not necessarily machines

Tinyfin is defined in terms of three roles. In the default, single-node deployment, all three run
as one Go binary on one machine. Nothing about the design requires that — the point of defining
them as separate roles from the start is that splitting them across machines later is a
deployment/configuration change, not a rearchitecture.

```
                        ┌─────────────────────────┐
   Jellyfin-compatible  │      API / metadata      │        embedded SQLite (default)
   clients (unmodified) │──────────────────────────│──────  or scale-mode storage driver
                        │  routing, auth, library,  │        (see docs/05)
                        │  users, sessions, images  │
                        └──────────────┬───────────┘
                                       │ playback decision
                                       │ (docs/07)
                                       ▼
                        ┌─────────────────────────┐
                        │     Transcode worker      │────►  jellyfin-ffmpeg (subprocess)
                        │  stateless per request     │
                        └─────────────────────────┘
                                       │
                                       ▼
                              shared media storage
                          (local disk, mounted network
                           share, or object storage)
```

A fourth concern — session/realtime state (`docs/06`) — is not a role with its own process; it's
a store the API/metadata role reads and writes, implemented in-process by default and externally
(Redis/Valkey) in scale mode, independently of which storage driver is active.

## API / metadata role

- Owns the HTTP and WebSocket surface described in `04`.
- Owns the storage connection, through the interface described in `05`. Never holds a full-catalog
  cache in memory — every list/detail/search request is a query (see `09` for why this is a hard
  requirement, not a preference).
- Decides Direct Play vs. Remux vs. Direct Stream vs. Transcode (`07`), but does not execute the
  transcode itself — it dispatches to a transcode worker (in-process function call in single-node
  mode, a network call in a split deployment) and streams the result back to the client.
- Reads/writes session and realtime state (`06`) — active sessions, SyncPlay groups, live
  progress — through its own pluggable interface, independent of the storage driver choice.

## Transcode worker role

- Given an input file reference and a target profile, invokes `jellyfin-ffmpeg` as a subprocess
  and produces output (typically HLS segments), nothing else.
- Deliberately stateless across requests: nothing about a given transcode job depends on any
  other job's state, and nothing needs to be synchronized between worker instances. This is what
  makes horizontal scaling of *this specific role* close to free — the same property every
  community Plex-clustering shim (`kube-plex`, `ClusterPlex`, `Plex-Remote-Transcoder`,
  `UnicornTranscoder`) exploits by retrofitting exactly this split years into those projects'
  life. Tinyfin has it from the first commit instead.
- Requires access to the same media files the API/metadata role can see. In a single-node
  deployment this is trivially true (same filesystem). In a split deployment, it means shared
  network storage or object storage — not a Tinyfin-specific requirement, the same constraint
  every distributed transcoding setup for any media server has.

## Storage layer

See [`docs/05-storage-drivers.md`](05-storage-drivers.md) for the full treatment. In short:
embedded SQLite by default, an operator-selected scale-mode driver (single write leader plus
streaming read replicas, following Litestream's model) when more than one API/metadata node is
running.

## Session/realtime state layer

See [`docs/06-session-and-realtime-state.md`](06-session-and-realtime-state.md). Kept as an
interface separate from storage specifically because changing the database engine does not, by
itself, make in-process session state shareable across nodes — conflating the two was the gap
that left PocketBase permanently single-node even in the hypothetical where it added a
client-server database.

## What ties it together: the API contract

None of the above is visible to a client. A client only ever sees the HTTP/WebSocket surface
described in `04` — whether a given deployment is single-node with embedded SQLite, or split
across a handful of API nodes and transcode workers behind a scale-mode storage driver, is
entirely an operator-side decision that the wire protocol does not expose or depend on.

## The access log

Every request produces one line on the server's standard logger, and it names the client that
made it:

```
tinyfin: GET /Items?Recursive=true&Limit=200 200 1.620586s client="Jellyfin Android TV" version="0.19.10" device="TV da sala"
```

The three client fields come from the `MediaBrowser Client="…", Device="…", Version="…"` header
that every official client already sends, so naming them costs one header parse and no protocol
change. They are quoted and length-bounded: the values are the client's own bytes, and a log line
one request can break in two is a log line one request can forge. A request with no such header —
an API key, a discovery probe, an anonymous call — logs exactly as it did before, with no suffix.

This was added after a real triage on a production server. The `/Items` sweep above ran every
fifteen minutes, moved about 130 MB, and the log could not say which of the four clients on that
network was asking: the path is identical for all of them, and only the header tells them apart.
The middleware sits outermost on purpose — it has to log the requests the chain below rejects,
and those never reach the auth context.
