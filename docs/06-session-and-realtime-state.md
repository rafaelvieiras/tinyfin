# 06 — Session and realtime state

## Why this is a separate interface from storage

It would be tempting to treat "where does data live" as a single question and let the storage
driver (`05`) answer all of it. That's the exact simplification PocketBase's own maintainer
warned is insufficient: asked why adding Postgres support wouldn't make PocketBase horizontally
scalable, the answer was that there'd be no synchronization mechanism for the application's
in-process event hooks and realtime subscriptions — a problem the database engine choice doesn't
touch at all.

Tinyfin has the same category of in-process state a media server specifically needs:

- Which sessions are currently active, and what each is playing.
- SyncPlay group membership and synchronized playback position.
- Live progress pushed to clients over WebSocket.

None of this is naturally at home in a durable SQL store — it's short-lived, high-churn, and read
by the same process that writes it in the common case. But it *is* exactly the kind of state that
breaks the moment there's more than one API/metadata node and a client's requests land on
different nodes across its session lifetime, unless it's made shareable on purpose.

## The interface

- **In-process mode** (default): an in-memory structure inside the single API/metadata process.
  Zero additional cost, zero additional process — appropriate for the single-node deployment that
  is the primary target in `01`.
- **Scale mode**: externalized to Redis/Valkey, using pub/sub for fan-out (a SyncPlay event or a
  progress update needs to reach every node with a relevant connected client, not just the node
  that produced the event) and simple key storage for session/group membership lookups.

This interface is independent of which storage driver (`05`) is active. A deployment can run
scale-mode storage with in-process session state (if it only ever routes a given client's
requests to one node — sticky routing) or embedded storage with scale-mode session state, though
the second combination is unlikely to be useful in practice. The point of keeping them separate
is that each gets turned on for its own reason, not that every combination is expected to be
common.

## Sticky routing as a cheaper partial alternative

Before reaching for Redis/Valkey, a load balancer that pins a given client connection to the same
API/metadata node for the lifetime of its session removes most of the need for shared session
state — at the cost of losing even load distribution and complicating failover. This is a valid
operator choice for a small scale-mode deployment (a handful of nodes, not a large fleet) and
should be documented as one when `05`/`06` configuration is written up for operators — not a
requirement of the architecture, but a legitimate way to defer the Redis/Valkey dependency.

## What this does not cover

Durable playstate (resume position, watched status) is **not** session/realtime state — once a
user pauses or finishes something, that fact needs to survive a restart and belongs in the
storage layer (`05`), not here. The distinction is lifetime: if it needs to outlive the
connection that produced it, it's storage; if it only matters while the connection is live, it's
session state.
