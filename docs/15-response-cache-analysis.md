# Response caching: what can be answered from disk, and why

**Status:** analysis only — nothing in this document is implemented.

**Last re-measured:** 21/nov/2026, on `v0.3.5` plus the two batching changes, after the query work in #54 and
the mount fix in #55. The verdict changed with those numbers, three times: the rows this document was written to
rescue went from 8.3 s to 0.9 s with the earlier work, from 640 ms to 139 ms with the grouping batching, and to
101 ms with the DTO batching described in
[Where the time went](#where-the-time-went-and-what-was-done-about-it). Read
[Does a file cache still pay?](#does-a-file-cache-still-pay) before building anything from here.

The document exists for two reasons: the home screen of a large library is the slowest thing the server
answers, and a future CDN or a second instance needs to know which answers are the same for every caller and
which are not.

Everything below is measured against production (`medias.pot2.top`, 15 libraries, 13,802 episodes, 86 series
in the biggest TV library), against the same binary on a copy of that database over loopback (server-side
cost, no transport), and against the public host (what a client feels).

## The shape of a home screen

One cold load of the web client, per endpoint. "Before" is 19/nov, before the N+1 work and before the
database was moved off the FUSE mount; "now (public)" is the median of 5 over `https://medias.pot2.top`;
"now (loopback)" is the same request against the same database file on a local disk, measured **before** the
grouping batching described in the next section (that section carries the after numbers for the TV rows).

| endpoint | before | now (public) | now (loopback) | answer |
|---|---|---|---|---|
| `GET /System/Info/Public` | 8 ms | 28.6 ms | 0.4 ms | 0.2 KB |
| `GET /System/Info` | 32 ms | 30.9 ms | 0.4 ms | 0.3 KB |
| `GET /Users/{id}` | 9 ms | 30.0 ms | 0.5 ms | 2.4 KB |
| `GET /DisplayPreferences/usersettings` | 9 ms | 30.0 ms | 0.6 ms | 1.1 KB |
| `POST /Sessions/Capabilities/Full` | 26 ms | (write, not measured) | — | — |
| `GET /UserViews` | 3 ms | 39.7 ms | 2.2 ms | 9.5 KB (2.0 KB gzip) |
| `GET /Shows/NextUp` | **4.4 s** | 29.8 ms | 0.7 ms | empty (see note) |
| `GET /UserItems/Resume` | — | 33.1 ms | 1.1 ms | empty |
| `GET /Items/Latest?parentId=<TV lib>` ×5 | **8.3 s** (biggest) | 615–964 ms | 399–649 ms | 4.4–80.5 KB each |
| `GET /Items/Latest?parentId=<other lib>` ×10 | 0.35 s (movies) | 37–186 ms | 3–106 ms | 12.9–232.9 KB each |
| `GET /Items/{id}/Images/Primary?fillWidth=…` ×75 | 13.6 MB | — | — | ~2 MB after #54 |

Two facts worth separating from the table:

- The floor of the public column is the transport, not the server: `/System/Info/Public` answers in 0.4 ms and
  a client sees 28.6 ms (TLS, the reverse proxy on `moon`, and the LAN). Everything above that floor is work.
- The fifteen "recently added" rows are **1066 KB decoded and 216 KB gzip** in total. Compression is the
  cheapest win already taken here; the payload itself is the next subject, not caching.

The empty `NextUp` row is a defect on this server, tracked in issue #56 and out of scope here. It makes the
row look fast for the wrong reason.

## Where the time went, and what was done about it

A TV library's row cost 0.4-0.65 s of server time, and it was not the query the previous round optimised:

- the ranking query (`QuerySeriesLatestActivity`) takes **16 ms**, and `EXPLAIN QUERY PLAN` shows it using
  `idx_items_library_type` with two temp b-trees for the group and the order;
- the row cost **592 ms** on `Séries` because `tvShowsLatestGrouped` then asked **four store reads per card**:
  that card's 24-hour window, the season's episode total, the series' season total, and (in the DTO builder)
  its child counts. A limit of 16 meant ~112 statements, at ~5 ms each. The pool size was **not** the
  multiplier — the loop stops as soon as it holds `limit` cards, so an 80-series pool and a 12-series pool cost
  the same row. That is why a cache keyed by anything about the pool would have bought nothing here.

Those four per-card reads are now **five reads for the whole row** (the ranking, the candidates' windows, their
watch states, and the two tallies) plus one fetch of the picked items. Measured on a
copy of the production database, same machine, median of 7, while both binaries were built from the same tree:

| request | before | after | |
|---|---|---|---|
| `Séries` (6,187 items, 86 series), `limit=16` | 640.7 ms | **139.5 ms** | 4.6x |
| `Séries`, `limit=4` | 256.8 ms | **86.9 ms** | 3.0x |
| `Documentários`, `limit=16` | 671.2 ms | **101.5 ms** | 6.6x |
| `Filmes` (movies, ungrouped), `limit=16` | 33.1 ms | 33.2 ms | unchanged, as expected |

### The DTO's per-card questions

The next measurement said the per-card share was not three count queries but **eight statements**: the item's
own picture refs (six single-instance kinds plus Backdrop), its playstate, a folder's leaf counts, the two
counts, and one lookup per ancestor — and an Episode card asked for its ancestors' picture refs three times
over, once per inherited field (logo, thumb, backdrop), so one episode card cost **49 picture lookups**.

On a listing page that scaled with the page, and it was the largest N+1 left in the server:

| request (production database copy) | before | after | |
|---|---|---|---|
| `GET /Items`, 100 episodes | 265.4 ms | **42.1 ms** | 6.3x |
| `GET /Items`, 300 episodes | 743.3 ms | **97.0 ms** | 7.7x |
| `GET /Items`, 600 episodes | 1444.0 ms | **164.2 ms** | 8.8x |
| `GET /Items`, 100 seasons | 271.0 ms | **47.6 ms** | 5.7x |
| `GET /Items`, 300 seasons | 813.3 ms | **100.8 ms** | 8.1x |
| `GET /Items/Latest`, TV library, `limit=16` | 131.3 ms | **101.3 ms** | 1.3x |
| `GET /Items/Latest`, other library, `limit=16` | 97.1 ms | **67.1 ms** | 1.4x |
| `GET /Items/Latest`, movies, `limit=16` | 32.0 ms | **28.1 ms** | 1.1x |

The statement counts say the same thing more plainly. A 100-episode page asked the store **5,901** times
(5,600 picture lookups, 200 ancestor lookups, 100 playstates and the listing query) and now asks **4**; a
100-season page asks 7. A grouped `limit=16` row asked 62 times and now asks 13. The answers are unchanged:
six routes, including the unscoped "recently added" row and `UserViews`, were compared field by field against
the previous build and are byte-identical.

What is left in a grouped row's ~101 ms is mostly **fixed work, not per card**: the ranking query (16 ms), the
episode tally behind the "fully watched" check (20 ms on this library), the windows, and serialising the
answer. Roughly 2 ms per card is left. The next measured step, if the row is still worth chasing, is that
episode tally: it counts every episode of every candidate series, and the row only needs to know whether a
series is fully watched.

### The test that pins the property

`internal/api/latest_grouping_query_scale_test.go` wraps the store in a counting decorator and asserts, for a
pool of 12 series and a pool of 60:

- the grouping path answers with a fixed number of reads (each batched query exactly once, `GetItem` never);
- the whole row's cost is **the same for both pool sizes**;
- the per-card share stays at the DTO's known three reads, so a future per-card call fails the test instead of
  sliding under a generous number.

It also pins the batched counts' parity with the per-parent `QueryItems` they replaced: extras are excluded from
both tallies, the way the catalog-facing queries exclude them.

`internal/api/dto_prefetch_test.go` pins the same property for the listing route, over a page of 20 and a page of
120 episodes: the page's statement count is identical, the per-card calls (`GetImageRefs`, `GetPlaystate`,
`GetItem`, `FolderLeafCounts`) are never made, and the page stays inside the row's budget. With the prefetch
disabled it reports what the code used to do — 5,600 picture lookups for 100 cards.

## Does a file cache still pay?

**No, and the case got weaker twice.** The argument for it in the first version of this document was that an 8.3 s
row made every other consideration secondary. That row is now 101 ms on the same database, the listing pages that
walk the catalog went from 265-1444 ms to 42-164 ms, and what remains in a row is fixed work rather than anything
a cache would skip. Three concrete reasons to keep fixing the path instead:

1. **Batching beats caching for every caller.** The pooled queries help any row that walks series; a cache only
   helps the one endpoint it wraps, and only the second time it is asked.
2. **A cache would hide the defect.** A stale-but-fast row is harder to notice than a slow one, and it would
   leave the per-card query pattern in place for good.
3. **Freshness has a cost this row does not justify.** A scan of a 13,800-episode library is a burst that lands
   between two requests; an answer one minute stale after that burst is exactly the bug users report as "the new
   episode did not appear". Paying that risk to save 100 ms is a bad trade while those 100 ms are mostly fixed
   work that a second batched query can shrink.

What would make it pay again, in order of likelihood:

- a row that is **still above ~50 ms** once the episode tally is batched — at that point the remaining time is
  reading the catalog itself, and an answer from disk beats computing it;
- a **second instance** behind a load balancer, which needs shared answers rather than per-process ones;
- a **CDN in front**, for the same reason it already helps `/web/**` and the picture routes.

The rest of this document is the design those three cases would need.

## What is worth caching, and what is not

The columns that matter are **who can see the answer** and **what changes it**. An answer that depends on
the user cannot be shared between users; one that depends on nothing but the catalog can be shared by
everyone, cached at the edge, and served without the server being asked at all.

| endpoint | depends on | changes when | cacheable in a file? |
|---|---|---|---|
| `GET /UserViews` | the user's policy (which libraries they may read) and their `OrderedViews` preference | a library is added/removed; the user saves their home-screen order | **per user**, yes. The catalog half is shared, the policy half is not |
| `GET /Users/{id}` | the user | their policy or preferences change | **per user**, yes, invalidated on any write to that user |
| `GET /DisplayPreferences/usersettings` | the user | they save display preferences | **per user**, yes |
| `GET /Items/Latest?parentId=<lib>` | the catalog, plus (a) the user's `LatestItemsExcludes`/`HidePlayedInLatest`, and (b) whether an episode is played when the series is fully watched | a scan adds or re-dates an item; a play state changes | **yes, keyed by library + a catalog generation counter**, with the played-state half per user |
| `GET /Shows/NextUp` | the user's play states, the catalog order of episodes | a play or a resume changes; a scan adds episodes | **per user and library**, and only after the write path invalidates it |
| `GET /UserItems/Resume` | the user's play states | any resume/progress report | **per user**; short TTL (seconds), it is the row that must feel live |
| `GET /Items/{id}/Images/Primary…` | the stored picture and the size/quality/format asked for | the picture file changes | **already cached on disk** (`imageCache`, keyed by item, kind, index, source tag, size, quality, format) |
| `GET /web/…` | nothing | a new build | **yes** — served `immutable` for a versioned bundle, `no-cache` for the entry point |

### The invalidation the catalog already has, and the one it needs

The scanner already records what it touched: every item carries a `DateCreated`, every library has a scan
signature, and `sessions`/`playstates` carry `touched_at` on every write. Two counters are missing for a
file cache to be safe:

1. **a catalog generation per library**, bumped at the end of every scan that changed that library
   (`scan_signatures` is the natural home), and
2. **a play-state generation per user**, bumped on any play, resume or mark-watched write.

With those two, the key of a cache file is `sha256(path + user generation + library generation + query
parameters that change the answer)`, and "invalidating" is just not finding the file again. The alternative —
`mtime`-based TTLs — is what a CDN would use, and it is wrong here for the same reason the home screen is
slow: a scan is a burst that lands between two requests, and a row that is 60 seconds stale after a scan is
exactly the bug users report as "the new episode did not appear".

### What must never be cached

- Anything with a per-request time in it: `/System/Info`, `/Sessions/*`, `/DisplayPreferences` writes.
- Anything authenticated by a token that is not a long-lived user identity (API keys with a scope narrower
  than the owner's).
- Streaming answers: video, HLS segments, trickplay tiles, chapter images, subtitles. They are already
  range-served and cached by the browser; wrapping them in a JSON-cache design would break `Range`.
- Anything from a library the caller may not read. A cache key that forgets the user's policy turns a
  per-user leak into a shared one.

## What this buys

- **A CDN in front**: `GET /web/**` is cacheable today (after the `Cache-Control` work in #54), and the
  picture routes are cacheable once the URL carries a real tag (they do). The JSON rows would need
  `Cache-Control: private, max-age=…` plus `Vary: Authorization` to be safe at the edge, or a shared cache
  keyed by user, which is another server's job.
- **Horizontal scaling**: with the answers on disk and the generations in the database, a second instance
  behind a load balancer answers the big reads without touching SQLite; the first instance's scan invalidates
  both by bumping the generation. Without the generation, two instances serve two different truths for as
  long as their TTLs disagree, which is worse than being slow.
- **Compression, already done**: 1066 KB of JSON for the fifteen "recently added" rows is 216 KB on the
  wire; the client JavaScript is 1.1 MB gzip. This was the cheapest win and it is taken.

## Order of work when this is picked up

1. ~~Batch the per-series work in `tvShowsLatestGrouped`.~~ Done: five reads for the row, 640.7 ms to 139.5 ms
   on the production database copy.
2. ~~Batch the DTO's per-card questions.~~ Done: pictures, playstates, leaf counts, the two counts and the
   ancestors are answered for the page, and a picture's tag set is computed once per request. A 100-episode
   page went from 5,901 statements to 4, and from 265 ms to 42 ms.
3. **Measure the "fully watched" episode tally** (`SeriesWatchStates`): it reads every episode of every
   candidate series (20 ms on an 86-series library) and the row only needs a yes/no per series.
4. Re-measure the fifteen rows. If the biggest is under ~50 ms, stop here: the file cache has no case.
5. Only if it is still slow: catalog generation per library and play-state generation per user (one column
   each, bumped in the write paths), then a file cache behind `GET /Items/Latest`.
6. `GET /Shows/NextUp` and `GET /UserViews` last, keyed by the same generations, and only once step 5 exists.
7. Only then reach for a CDN, which needs the `Cache-Control`/`Vary` work on `GET /web/**` that #54 landed.
