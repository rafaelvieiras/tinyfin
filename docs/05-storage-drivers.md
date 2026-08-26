# 05 — Storage drivers

## The interface

One storage interface, two implementations. Every read and write the API/metadata role performs
goes through this interface — nothing in the rest of the codebase is aware of which
implementation is active.

- **Embedded mode** (default): a single SQLite database file, no separate process.
- **Scale mode** (opt-in): a networked implementation an operator turns on when running more than
  one API/metadata node.

The exact Go interface signature is open item **O3** in `00-decision-log.md` — this document
covers the conceptual boundary and the reasoning behind the scale-mode design, not the final API.

## Why embedded mode is SQLite, not something else

This is the default and the target for the primary use case in `01`: single machine, near-zero
idle memory, zero operational overhead. SQLite embedded, WAL mode, with well-designed indexes
(FTS5 for search, generated columns where useful) serves this directly. No separate database
process to run, monitor, or account for in a memory budget.

## Why scale mode isn't "just switch to Postgres"

Jellyfin itself is limited by exactly this: the entire state (library, users, sessions,
playstate) lives in one SQLite file, and SQLite is single-writer — two Jellyfin processes cannot
write to the same state at once, which is why the reference implementation has no real multi-node
story today. Postgres support has been on Jellyfin's own roadmap for a 12.0+ release without a
committed date.

PocketBase, a comparable single-binary/embedded-SQLite Go project, made the same observation and
went further: its maintainer has stated that switching PocketBase's storage to Postgres *still
wouldn't* make it horizontally scalable, because there is no synchronization mechanism for
in-process application state (hooks, realtime subscriptions) independent of the database engine.
PocketBase's answer to this is to not pursue horizontal scaling at all — a legitimate choice for
its use case, and evidence the problem is genuinely hard, not evidence it's unsolvable.

Tinyfin's answer is to unbundle the two things PocketBase's finding shows are actually separate:
the **storage engine** (this document) and **session/realtime state** (`06`). Fixing storage alone
would repeat PocketBase's finding almost exactly; fixing both is the actual requirement.

## Why scale mode is a write-leader with streaming read replicas, not a Raft cluster, by default

A media server's workload is read-heavy by nature: browsing the library, fetching images,
requesting `PlaybackInfo`, checking playstate. Writes are comparatively rare and small: playstate
updates, scan results, admin changes. That shape doesn't call for a fully symmetric multi-writer
database — it calls for scaling reads.

The scale-mode default follows the model Litestream uses for live read replication: one write
leader receives all writes; one or more read replicas stay in sync by streaming the SQLite
write-ahead log and serve reads locally, at SQLite-embedded latency, from whichever node a client
happens to hit. This keeps the "SQLite in spirit" property even in scale mode — same schema, same
query patterns, no new SQL dialect to support — while removing the single-node ceiling for the
read traffic that dominates the workload. Writes still funnel through one leader, which is not a
practical bottleneck for the write volume this workload actually produces.

`rqlite` (a Go-native, Raft-based distributed SQL layer over SQLite, with local reads on any node
and writes replicated via consensus) is the fallback if a genuinely write-heavy, symmetric
multi-writer scenario ever shows up — heavier operationally, and not needed for the target use
case as currently understood. Kept explicitly out rather than built preemptively; see open item
**O5**.

`dqlite` (Canonical's C library, also Raft-based) was considered and set aside: it's a library to
integrate into your own software rather than a runnable service, which is a heavier lift than
either of the two options above for no clear benefit here.

## Operational shape

- **Embedded mode**: `tinyfin` with a data directory. One process, one file. No configuration
  beyond a path.
- **Scale mode**: one node configured as the write leader; additional nodes configured to stream
  from it and serve reads locally. Turning this on is a configuration change on top of the same
  binary — not a data migration to a different database product, and not a code path that only
  exists in a "scale build."

Open item **O4** (`00-decision-log.md`) — whether the replication mechanism is built directly, or
Tinyfin depends on Litestream itself as a library/sidecar rather than reimplementing WAL
streaming — is not yet settled.
