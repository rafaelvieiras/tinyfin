# 09 — Memory and performance goals

## The failure mode this responds to

Documented directly by the reference implementation's own maintainers
(`jellyfin/jellyfin` discussion #10444): a server can start at roughly 400MB and grow to several
GB over normal browsing on a mid-sized library, because the library manager caches aggressively
in memory — including data the maintainers themselves describe as non-critical — instead of
querying the database per request. A separate, also-documented failure mode (`jellyfin/jellyfin`
issue #11588) is unbounded `ffprobe` subprocess spawning during a library scan causing
out-of-memory conditions, independent of the caching issue.

Neither of these is a .NET-runtime-overhead problem. Both are specific, nameable design
decisions. Tinyfin's memory goals exist to make the opposite decision in both cases, deliberately,
from the start — not to chase a number for its own sake.

## Concrete design rules

1. **No full-catalog cache, ever.** Every list/detail/search request served by the API/metadata
   role is a query against the active storage driver (`05`), using indexes (FTS5, generated
   columns) chosen so that "just query it" doesn't mean "make every request slow." This directly
   targets discussion #10444's root cause. See `AGENTS.md` principle 1 — this is not negotiable
   for "performance" reasons; if a query pattern is slow, the fix is a better index, not a cache
   that reintroduces the failure mode this project exists to avoid.
2. **Bounded scan-time subprocess concurrency.** `ffprobe` (and any other scan-time subprocess) is
   invoked with an explicit concurrency limit, not one-per-discovered-file. Directly targets issue
   #11588's root cause. See `08`.
3. **Idle baseline independent of library size.** Because rule 1 removes the size-proportional
   cache, idle memory usage should not meaningfully grow as the catalog grows — a library of a
   few hundred items and one of tens of thousands should have comparable idle footprints. This is
   the property being protected, more than any single absolute number.
4. **Transcode memory is out of this budget's scope.** Memory used by an active `jellyfin-ffmpeg`
   subprocess during a transcode is a function of the codec work being done, not of Tinyfin's own
   design — see `07`. The goals in this document are about the API/metadata role's own footprint
   independent of active transcoding sessions.

## What "near-zero idle" means in practice

Not literally zero — a Go binary with an open SQLite connection and an HTTP server has some
baseline. The goal is a baseline that stays roughly flat as the catalog and the number of known
(but currently inactive) sessions grow, in contrast to the reference implementation's documented
growth curve. A specific numeric target (e.g. an idle-RSS ceiling for a library of a given size)
belongs in this document once there's a working implementation to measure — recorded here as a
placeholder rather than a number invented ahead of any measurement.

## Verification

Once implementation exists, this document should be checked against actual measurements (idle
RSS at a few library sizes, RSS during a bounded scan, RSS during N concurrent Direct Play
sessions with zero active transcodes) rather than left as an aspiration. Until then, the rules
above are the commitment; the numbers come later.
