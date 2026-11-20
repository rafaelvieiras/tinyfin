# Response caching: what can be answered from disk, and why

**Status:** analysis only — nothing in this document is implemented yet. It exists because the home screen
of a large library is slow for a reason the server can fix without touching the client, and because a
future CDN or a second server instance needs to know which answers are the same for every caller and which
are not.

Everything below is measured against production (`medias.pot2.top`, 14 libraries, 13,802 episodes, 86
series in the biggest TV library) and against a local server running on a copy of that same database.

## The shape of a home screen

One cold load of the web client asked for the following, in this order:

| endpoint | answer | measured before | measured after the N+1 work |
|---|---|---|---|
| `GET /System/Info/Public` | server identity | 8 ms | 8 ms |
| `GET /System/Info` | server capabilities | 32 ms | 32 ms |
| `GET /Users/{id}` | the user and their policy | 9 ms | 9 ms |
| `GET /DisplayPreferences/usersettings?userId=…&client=emby` | the user's display settings | 9 ms | 9 ms |
| `POST /Sessions/Capabilities/Full` | session registration (write) | 26 ms | 26 ms |
| `GET /UserViews?userId=…` | the 14 libraries | 3 ms | 3 ms |
| `GET /Shows/NextUp?userId=…&limit=24` | the "Next Up" row | **4.4 s** | 1.6 ms |
| `GET /Items/Latest?…&parentId=<lib>&limit=16` × 14 | one "recently added" row per library | 0.35 s (movies) / **8.3 s** (TV) | 0.03 s / 0.56 s |
| `GET /Items/{id}/Images/Primary?fillWidth=…&quality=96` × 75 | the cards' pictures | 13.6 MB | ~2 MB |

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
slow: a scan of a 13,000-episode library is a burst that lands between two requests, and a row that is
60 seconds stale after a scan is exactly the bug users report as "the new episode did not appear".

### What must never be cached

- Anything with a per-request time in it: `/System/Info`, `/Sessions/*`, `/DisplayPreferences` writes.
- Anything authenticated by a token that is not a long-lived user identity (API keys with a scope narrower
  than the owner's).
- Streaming answers: video, HLS segments, trickplay tiles, chapter images, subtitles. They are already
  range-served and cached by the browser; wrapping them in a JSON-cache design would break `Range`.
- Anything from a library the caller may not read. A cache key that forgets the user's policy turns a
  per-user leak into a shared one.

## What this buys

- **A CDN in front**: `GET /web/**` is cacheable today (after the `Cache-Control` work in this change), and the
  picture routes are cacheable once the URL carries a real tag (they do). The JSON rows would need
  `Cache-Control: private, max-age=…` plus `Vary: Authorization` to be safe at the edge, or a shared cache
  keyed by user, which is another server's job.
- **Horizontal scaling**: with the answers on disk and the generations in the database, a second instance
  behind a load balancer answers the big reads without touching SQLite; the first instance's scan invalidates
  both by bumping the generation. Without the generation, two instances serve two different truths for as
  long as their TTLs disagree, which is worse than being slow.
- **The cheapest win of all, already done here**: compressing the answers. 246 KB of JSON for one library's
  "recently added" is 49 KB on the wire; the 3.8 MB of client JavaScript is 1.1 MB.

## Order of work when this is picked up

1. Catalog generation per library and play-state generation per user (one column each, bumped in the write
   paths).
2. A file cache behind `GET /Items/Latest` (the row with the most traffic and the clearest invalidation).
3. `GET /Shows/NextUp` and `GET /UserViews`, both keyed by the same generations.
4. Only then reach for a CDN, which needs the `Cache-Control`/`Vary` work on `GET /web/**` that this change
   landed.
