# 12 — Clean-room behavior specs

## What this is

Functional specs extracted from reading reference-server source under the clean-room process
defined in `AGENTS.md`'s "Clean-room requirement" section (see D12 in
[`docs/00-decision-log.md`](00-decision-log.md) for the full reasoning). Each section below exists
because black-box observation alone (real client traffic, a running reference server) could not
resolve some specific behavior with enough confidence, so the reference source was read — but only
to extract *behavior*: inputs, outputs, data flow, algorithm, state transitions. Nothing here
carries over the reference implementation's own file names, class/method names, comments, or code
structure; it is a description of what happens, not how the reference server happens to organize
the code that makes it happen.

This is a spec-only document. Implementation from any section here is a separate task, done from
the spec alone plus black-box verification against a running reference server — never with the
reference source open in the same session. See `AGENTS.md` for the full two-phase rule.

Sections are added here over time as new gaps need this treatment; each section names the gap it
resolves and, where useful, the endpoint/doc it feeds back into.

---

## Items/Latest — series grouping

### The gap this resolves

`GET /Items/Latest` applied to a TV-shows library. Tinyfin currently returns a flat,
newest-first list of every recently-scanned `Episode`/`Season` row. The reference server instead
returns at most one card per series for the same request, which is what real client testing
against `jellyfin-real-staging:8097` showed and what `docs/11-jellyfin-web-route-coverage.md`'s
`GET /Items/Latest` entry flags as a confirmed, unresolved divergence. This section is that
resolution.

### Behavior, in prose

For a request scoped to a single TV-shows library (or to the user's whole home view, when no
specific library is requested), "recently added" is computed **per series, not per raw item**:

1. Look at everything recently added across the scope (recursively — episodes and seasons, not
   just top-level series folders), grouped by which series each item belongs to. For each series,
   note the most recent addition timestamp in that series.
2. Rank series by that timestamp, newest first, and keep only as many series as the request's
   `limit` asks for. **`limit` counts series/groups, not raw episodes** — a series that had 12
   episodes added still consumes exactly one slot in the result.
3. For each of those series, decide what to show as its card:
   - Look only at items in that series added within a trailing window of the series' own most
     recent addition (a 24-hour lookback from that series' latest timestamp — items added earlier
     than that, even if otherwise "recent" in absolute terms, are outside this series' current
     "recent addition event" and are ignored for this decision).
   - If everything in that window belongs to a single season:
     - Treat it as a "bulk addition" if more than one episode landed in the window, **or** if the
       count landed in the window happens to equal that season's total episode count (i.e. even a
       single episode counts as bulk when it's the entirety of a short/complete season).
     - On a bulk addition, in a series that has more than one season total, the card represents
       **the season** (not the series, not the episode).
     - On a bulk addition in a series that only has one season total, the card represents **the
       series** instead (there's no useful "season" distinction to show for a single-season
       show).
     - Otherwise (exactly one episode landed in the window, and it isn't the season's only
       episode) the card represents **that single episode** directly.
   - If the window's items span more than one season, the card represents **the series** — a
     multi-season burst of activity is summarized at the series level, not per season.
4. Whatever entity was chosen (season, series, or episode) has to itself be visible to the
   requesting user under normal parental/access restrictions — a restricted season or series
   cannot be "unlocked" for display just because one of its episodes matched. If the chosen
   entity fails that check, the card falls back to showing the single most recent episode instead
   (which is itself still subject to normal item-level visibility).
5. The final result is one card per series, ordered by each series' most-recent-addition
   timestamp (newest first, with a stable tie-break so ordering doesn't jitter between identical
   requests), truncated to the request's `limit`.
6. A card that represents a season or a series is a normal item of that type in the response —
   it is not a synthetic wrapper, and this endpoint does not stamp any special "N new episodes"
   count onto it. (See "Child-count field" below — in practice, for a TV-shows request, no
   episodes ever end up needing to be summarized under a season/series card *after* the
   per-series selection above, because that selection already reduced each series down to exactly
   one representative entity before any "how many items does this card stand for" accounting
   would apply. Don't design an implementation around expecting to merge multiple raw rows per
   card here — by the time cards are assembled, there's only ever one row per series left to
   place.)

### Child-count field

`GET /Items/Latest`'s response items carry a "how many things does this card represent" count,
but it is populated by a **more general** rule that also covers non-TV cases (e.g. a "latest
albums" row where a card can legitimately stand in for several newly-added tracks): the count is
set only when a card is shown *because it was chosen to represent more than one underlying newly
added item*, and is left unset/zero otherwise. Per the walkthrough above, the TV-shows
per-series selection already collapses to one entity per series before that accounting happens,
so this field does not end up populated for TV cards in practice — a season or series card's own
normal item metadata (its own episode/season counts, populated the same way as any other
season/series detail response) is the only place per-season/per-series counts show up for TV.
Do not special-case this field for the TV branch; implement the general "more than one
represented item -> set the count" rule so the movies/music/TV branches share it, and let the
TV branch's own upstream dedup be what naturally keeps it at zero there.

### Movies — confirmed, no grouping

A movies-library request has no series/season concept and is **not grouped** at all: each
distinct movie is its own card. The only reduction that happens is straightforward de-duplication
by the movie's own stable identity (guarding against a movie that has multiple stored
versions/copies showing up more than once) — the newest copy of a given movie wins, older
duplicates of the *same* movie are dropped, but two different movies are never merged into one
card. `limit` here counts movies directly, one slot per distinct movie, with no season/series-style
grouping logic applied at all. This confirms what `docs/11`'s note already suspected ("the
movies-library case ... has no such grouping in the reference server either").

### Music — noted for completeness, not this gap's focus

A music-library request groups by album instead of by series: the card is always the album (never
a bare track), one card per album that has a recently-added track, newest-album-addition first.
This is a different, narrower rule than the TV-shows one (no season/episode-style tiering — an
album is always the representative) and is not the gap this section was written to close, but is
worth recording here so a future implementer doesn't have to re-derive it: `limit` counts albums,
and — unlike the TV case — a music card's represented-item count *is* meaningfully non-trivial
(an album can stand in for several newly added tracks), so the general child-count rule above does
apply and matters for this branch, even though it's a no-op for TV.

### Mixed / uncategorized libraries

A library (or the whole home scope) with no single declared content type applies **both** the
movies rule and the TV-shows rule independently — standalone movie-like items dedupe per the
movies rule, series-like items group per the TV-shows rule — and merges the two result sets back
together by the same newest-first ordering, trimmed to `limit`. Not a focus of this gap either;
recorded for completeness in case a future mixed-library test surfaces a related issue.

### What determines "grouped at all"

All of the above only applies when the request is asking for grouped results in the first place
(the request has a boolean knob for this, defaulting to "grouped" — real client traffic observed
so far always requests grouped results, per the existing `docs/11` note that Tinyfin's own bundle
sends this endpoint's params consistent with that). When a caller explicitly asks for the
ungrouped form, none of the above applies and the response is the flat newest-first item list
Tinyfin already produces today — that existing behavior is correct for that case and should stay.

### Scope note for the implementer

This section is deliberately self-contained: an implementation can be written from it plus normal
black-box verification against a running reference server (confirming the 24-hour window, the
single-vs-multi-season branching, and the parental-restriction fallback behave as described here)
without re-reading the reference source. If black-box testing surfaces a case this section doesn't
predict correctly, that's a signal to extend this section (re-extract, write down the correction)
rather than to open the reference source again mid-implementation.

### Implementation findings (Phase 2, black-box against `jellyfin-real-staging:8097`)

The grouping toggle mentioned above is the `groupItems` query param (any capitalization),
confirmed live: omitted or anything but exactly `false` groups; `groupItems=false` returns the
flat list. `docs/11`'s prior note that jellyfin-web's own bundle always requests grouped results
still holds — `groupItems` was not observed absent from a real jellyfin-web call.

### Fully watched series are excluded (implemented)

Confirmed live against `jellyfin-real-staging:8097`, black-box only (no reference source read for
this refinement — this file's earlier preliminary note, made while implementing series grouping
itself, already described the observable behavior precisely enough to implement and verify against
a running server without reopening the reference source): **a series every one of whose episodes
is played (`PlayCount > 0`) for the requesting user never appears in the grouped `GET
/Items/Latest` result, at all, regardless of how recent its own addition is** — it does not rank
into the response even at a `limit` far larger than the number of series that do appear, and,
critically, it does not consume one of the requested `limit` slots by being excluded: a series
ranked immediately behind it by recency, with even one unwatched episode, takes its place rather
than the slot count shrinking by one.

What was confirmed, precisely, by testing rather than assumed from the original citation below:

- **The check is over the whole series**, every episode the series has, not just the
  Episode/Season rows that happen to have landed in the grouping candidate pool (a bounded, recent
  window — see `latestSeriesGroupingPoolSize` above). Reproduced live: marking every episode of a
  103-episode series played via the reference server's own recursive
  `POST /Users/{userId}/PlayedItems/{seriesId}` (confirmed this cascades: the response came back
  `UnplayedItemCount: 0`, and a spot-checked episode's own `UserData.PlayCount` was `1`) made that
  series disappear from the grouped result entirely.
- **"Watched" is `PlayCount > 0`**, the same criterion `nextUpCandidateForSeries` (`nextup.go`)
  already uses for the analogous question on `GET /Shows/NextUp` — not the `Played` bool
  specifically (they agree in every case actually exercised here, since marking an episode played
  always sets both together) and not something requiring a distinct "no playstate row at all"
  case: `GetPlaystate`'s own zero-value convention already reads a never-touched episode back as
  `PlayCount 0`, so "never watched" and "explicitly unplayed" both naturally fail the check with no
  special-casing.
- **A partially watched series (at least one unwatched episode remaining) is unaffected** — it
  still appears normally, card selection unchanged, confirmed live by leaving one episode of a
  series unplayed and one played.
- **A never-watched series (no playstate rows for the user at all) is unaffected** — confirmed live
  and also the default state of every other fixture in this section's other tests; this exclusion
  is not "hide anything without watch history," only "hide a series with nothing left unwatched."
- The single-Episode-card case (docs/12 step 3's "no season key" fallback and the plain
  single-episode branch) was not separately exercised live: a card that resolves to one specific
  Episode is, by construction, the item whose addition is being reported, and per this project's
  scan/import model a newly-scanned episode has no playstate yet — so this exclusion's precondition
  (every episode of the series played) can't co-occur with "the card is this brand-new episode" in
  practice. Implemented uniformly anyway (checked once per series group, before card selection,
  regardless of what type of card that group would resolve to) rather than carved out as a special
  case, since nothing in the confirmed rule above suggests the card's own type should matter.

Implemented in `internal/api/latest_grouping.go`'s `isSeriesFullyWatched`, called from
`tvShowsLatestGrouped` once per ranked series group, skipping (not counting toward `limit`) any
group it reports as fully watched, before that group's card is resolved.

One more thing surfaced during live comparison that is real, reproducible reference-server
behavior but is **not** implemented by Tinyfin's `internal/api/latest_grouping.go`, logged here
rather than chased further per the scope note above:

- **A series whose only recent-addition burst is roughly a month or older than the request time
  sometimes still resolves to a single-episode card**, even though its own items, read directly,
  clearly meet this section's stated 24-hour-trailing-window/bulk/multi-season criteria for a
  season or series card. Reproduced consistently (stable across `limit` values from 3 to 300) on
  one real 4-season series in the staging catalog, so it isn't request-limit-dependent the way the
  next finding is. Nothing in black-box testing pinned down the actual extra condition (an
  absolute-age cutoff on top of the relative 24-hour window is the leading guess, given a
  same-shape series only 5 days "younger" grouped correctly) — flagged as unresolved rather than
  guessed at further. Tinyfin's implementation does not reproduce this: it applies the
  window/bulk/multi-season rule exactly as stated above regardless of how old the burst is.
- Separately, and unlike the finding above, the reference server's card selection for a given
  series can flip between a single-episode card and a correctly-detected bulk (season/series) card
  purely as a function of the request's own small `limit` values (observed thresholds: `limit=1`
  vs `2` for one series, `2` vs `3` for another, stabilizing by `limit=3` in both cases and staying
  stable through `limit=300`) — a query-budget artifact of however the reference server internally
  bounds its own per-series lookback, not a real product rule. Tinyfin's implementation is
  deliberately not limit-dependent this way: `tvShowsLatestGrouped` always draws its candidate pool
  from a size floor well above realistic `limit` values (`latestSeriesGroupingPoolMin`), so its
  card selection for a given series' data is the same regardless of the requested `limit` — matches
  the reference server at the `limit` values real clients actually use (`defaultLatestLimit = 20`
  in `library.go`, the same default jellyfin-web's own bundle sends per `docs/11`) and is, if
  anything, a more consistent reading of this section's own stated rule at smaller `limit` values
  than the reference server itself produces.

---

## Movie detail page — image tags for Backdrop and Logo

### The gap this resolves

A revalidation pass against the movie detail page (comparing `GET /Users/{userId}/Items/{itemId}`
between Tinyfin and the reference server for the same title) found that Tinyfin's scanner already
records a `Backdrop` image reference for a movie's `fanart.jpg`/`backdrop.jpg` — `GET
/Items/{itemId}/Images/Backdrop/0` correctly serves it — but the item's own detail response never
tells a client that image exists, so no client ever requests it. `ImageTags`/`BackdropImageTags`
were the only two image-related response fields checked in code, and only `ImageTags["Primary"]`
was ever populated.

### Behavior, in prose

This isn't a reference-server algorithm to reverse-engineer — it's a documented wire contract
(source: the reference server's own published OpenAPI schema, fetched live from a running instance
at `/api-docs/openapi.json`, not the C# source) that Tinyfin's response-building code simply wasn't
using fully:

- `ImageTags` is a string-keyed map, one entry per *single-instance* image kind the item actually
  has (`"Primary"`, `"Logo"`, `"Banner"`, `"Thumb"`, `"Disc"`, and a handful of rarer kinds) — the
  map key is the kind name, the value is an opaque tag (a cache-busting version string; Tinyfin
  already generates one of these correctly for `Primary`, the same generation should apply to every
  kind that has an image).
- `BackdropImageTags` is a separate top-level field specifically because an item can have *more
  than one* backdrop — it's an array of tags (parallel to backdrop index 0, 1, 2, ... which
  `GET /Items/{itemId}/Images/Backdrop/{index}` already addresses by index in Tinyfin), never
  folded into the `ImageTags` map.
- A client only ever requests an image URL for a kind whose tag is actually present in the item
  response — this is why the existing Backdrop image was silently unused: it was fully servable but
  never advertised.
- There is no field named `LogoImageTag` — a Logo image is just another entry in the `ImageTags`
  map (key `"Logo"`), exactly like Primary.

### Fix, described behaviorally (not an algorithm to extract — just wiring)

The response-building code needs to check every image kind Tinyfin's scanner already records for an
item (not only `Primary`) and populate `ImageTags`/`BackdropImageTags` accordingly, using the same
tag-generation Tinyfin already applies to `Primary`. This closes the gap for `Backdrop` immediately
(the scanner already records it, per above); see the next section for the separate gap in what the
scanner records for `Logo`.

### Implementation findings

Implemented as `dto.go`'s `imageTagsFor` (replacing the old `Primary`-only `primaryImage`) — one
`GetImageRefs` call per single-instance kind (`Primary`, `Logo`, `Banner`, `Thumb`, `Disc`) plus one
for `Backdrop`, folding the former into `ImageTags` and the latter into `BackdropImageTags` in the
order `GetImageRefs` already returns (`ORDER BY idx ASC`). Confirmed live against
`jellyfin-real-staging:8097` for the same title (`A Arte da Guerra`, 2000): after a rescan, Tinyfin's
response showed `ImageTags: {"Primary":..., "Logo":..., "Banner":...}` and `BackdropImageTags:
["1"]` — matching the reference server's own response shape for that field set.

One real gap found *during* this live comparison, out of scope for this task and not fixed here: the
reference server's response for the same title also carries `ImageTags.Thumb` and `ImageTags.Disc`
(from a `<video-basename>-thumb.jpg` and a `disc.png` sitting in the same folder) — Tinyfin's
`imageTagsFor` would already surface these correctly *if* the scanner recorded them, but
`internal/scan/scan.go`'s `seedFolderImages` has no filename recognition for `Thumb`/`Disc` at all
(only `Primary`/`Backdrop`/`Logo`/`Banner`, the four kinds this task's Logo/Banner section covers).
Worth a small follow-up pass, same shape as the Logo/Banner section below.

---

## Movie detail page — local Logo/clearlogo artwork recognition

### The gap this resolves

Beyond the DTO-wiring gap above, `GET /Items/{itemId}/Images/Logo` returns 404 even when a movie's
own folder has a `logo.png` file sitting right next to its `poster.jpg`/`fanart.jpg` — the scanner
never recorded a `Logo` image reference for it at all, unlike `Primary`/`Backdrop` which the scanner
already resolves from `poster.jpg`/`fanart.jpg` etc. (`internal/scan/scan.go`, sourced from
kodi.wiki's "Artwork" guide per that code's own existing comment).

### Behavior, in prose

Source: Jellyfin's own published user documentation (`jellyfin.org`'s movie library naming guide),
not the C# source — this is a filename convention, the same kind of publicly documented convention
the existing `Primary`/`Backdrop` recognition in `internal/scan/scan.go` was already sourced from,
extended to the two image kinds that guide documents but Tinyfin's scanner doesn't yet recognize:

- **Logo** — a movie-folder file named `logo` or `clearlogo` (any of the image extensions already
  supported for the other kinds, e.g. `.png`/`.jpg`), standalone (`logo.png`) or as a suffix off the
  video's own basename (`<video-basename>-logo.png`), the same standalone-or-suffix pattern already
  implemented for `Primary`/`Backdrop`.
- **Banner** — same pattern, filename `banner`. (Not part of the movie-detail-page gap that
  triggered this investigation, but the same documentation source covers it and it's the same
  one-line addition as Logo — worth doing together rather than leaving a known, equally-easy gap for
  a future pass to rediscover.)

No new *behavior* beyond "recognize these two more standalone/suffix filename patterns, the same
way the existing two are recognized" — this is an extension of `internal/scan/scan.go`'s existing
`primary`/`backdrop` filename-list pattern, not a new resolution algorithm.

### Implementation findings

Implemented as a direct extension of `seedFolderImages`/`seedFolderImage` — two more candidate lists
(`logo`/`banner`) tried in the same standalone-then-suffix priority order as `primary`/`backdrop`,
same deterministic-`ImageRef`-ID upsert. Confirmed live: the real staging movie folder used for the
`ImageTags` validation above already had both `logo.png` and `banner.jpg` sitting on disk (left there
by a prior manual test-fixture setup, not something this task added) — before this fix, a rescan
recorded neither; after, `GET /Items/{itemId}/Images/Logo` and `.../Images/Banner` both returned 200
with the real file bytes (`image/png` 78KB and `image/jpeg` 135KB respectively), and both `ImageTags`
keys appeared as documented in the section above.

---

## Movie detail page — Theme Media (`GET /Items/{itemId}/ThemeMedia`)

### The gap this resolves

The movie detail page revalidation found `GET /Items/{itemId}/ThemeMedia` isn't implemented at all
in Tinyfin (404; no route registered, no handler, zero mentions anywhere in the codebase), while the
reference server returns 200 for the same request (an empty-but-present result for the specific
title tested, which had no theme files — so this section's shape is confirmed from the wire
contract and public docs, not from observing a populated response live).

### Wire shape (source: reference server's own published OpenAPI schema, fetched live, not the C# source)

- `GET /Items/{itemId}/ThemeMedia` — path param `itemId`; query params `userId` (optional, attaches
  per-user data the same way other item responses do), `inheritFromParent` (bool, default `false`),
  `sortBy`/`sortOrder` (standard item-list sort params, same vocabulary as `GET /Items`).
- Response body: three parallel result blocks — `ThemeVideosResult`, `ThemeSongsResult`,
  `SoundtrackSongsResult` — each independently shaped as `{ Items: [BaseItemDto...],
  TotalRecordCount, StartIndex, OwnerId }`. `OwnerId` is the id of the item the returned theme media
  actually belongs to — the requested item itself when found directly on it, or an ancestor's id
  when `inheritFromParent=true` caused a fallback further up the tree (relevant for episodes
  inheriting a series' theme; a movie has no such ancestor to inherit from).
- A movie item type only ever has `ThemeVideosResult`/`ThemeSongsResult` populated (no
  `SoundtrackSongsResult` concept for movies in scope here); each `Items` entry is a normal
  `BaseItemDto` for the underlying local audio/video file, not a synthetic wrapper.

### Behavior, in prose (source: Jellyfin's own published user documentation, `jellyfin.org`'s movie library guide — not the C# source)

- **Theme songs**: a file named `theme.<ext>` (any supported audio extension) directly in the item's
  own folder, **or** any audio file inside a `theme-music/` subfolder of that item's folder, is
  recognized as a theme song.
- **Theme videos**: video files inside a `backdrops/` subfolder of the item's own folder are
  recognized as theme videos (the same folder Tinyfin doesn't currently use for anything — this is
  a video, not the numbered-backdrop-image convention already implemented for the `Backdrop` image
  kind, which lives directly in the item folder as `fanart.jpg`/`backdrop.jpg`, not inside a
  `backdrops/` subfolder).
- When a client wants to know if it should play a theme song or a theme video and both exist for the
  same item, Jellyfin's documented behavior is: prefer the theme video (this is a client-side
  playback choice communicated by which of the two result blocks is non-empty — the endpoint itself
  just reports both truthfully, it doesn't suppress one in favor of the other).
- `inheritFromParent=false` (the default, and what the movie detail page requests): only the
  requested item's own folder is checked, no walk up to a parent. Tinyfin's implementation needs the
  parent-walk behavior only if a caller actually sends `inheritFromParent=true` — a movie itself has
  no meaningful "parent" for this purpose (it isn't nested under a series/season), so this only
  matters for the Episode/Season case, out of scope for this specific gap (movie detail page) but
  worth a route that at least doesn't break when a TV client sends it.

### Scope note

Movie detail page testing didn't exercise a title that actually has a `theme.mp3`/`backdrops/`
folder present, so the *empty*-result shape (200, all three blocks present with `Items: []`) is what
was live-confirmed; the *populated* shape is the OpenAPI schema plus the documented filename
convention above, not something observed live. An implementer should still verify the populated case
live once a test title with a theme file exists in the staging catalog, rather than trusting this
section blind.

### Implementation findings

Implemented as `internal/api/theme_media.go`'s `handleThemeMedia`, resolved at **request time** (not
scan time): the item's folder is derived from `storage.Item.Path` (`filepath.Dir`) and read directly
on every call, rather than indexed as a new storage type at scan time. This was a deliberate choice,
not forced by anything in the wire contract — theme media has no per-item metadata worth
precomputing (unlike an image's width/height/aspect-ratio), so there's no real cost to resolving it
live, and it means a theme file dropped into a folder shows up on the very next request with no
rescan needed (confirmed live, see below) — a nicer property than the image kinds above, which do
need a rescan.

Both shapes from the spec above are now live-confirmed, not just the empty one:

- **Empty shape**: confirmed against a staging title with no theme files — 200, all three blocks
  present with `Items: []`, matching the reference server's own response for the same title
  (`jellyfin-real-staging:8097`, same `Filmes` library, empty on both servers since neither had a
  theme file for it).
- **Populated shape**: confirmed by writing a real `theme.mp3` into the same staging movie folder
  used for the ImageTags/Logo/Banner validation above and requesting `GET
  /Items/{itemId}/ThemeMedia` again with no rescan in between — `ThemeSongsResult.Items` came back
  with one entry (`Name: "theme"`, `Type`/`MediaType: "Audio"`, a stable deterministic `Id`),
  `ThemeVideosResult` stayed empty (no `backdrops/` folder was added), `TotalRecordCount`/`OwnerId`
  matched. The test file was deleted immediately after, and a follow-up request confirmed the
  response reverted to fully empty — no leftover state, consistent with this being resolved at
  request time rather than persisted.
- One minor formatting note found during the live comparison, not a functional divergence:
  `jellyfin-real-staging`'s own IDs (including `SoundtrackSongsResult.OwnerId`, `"N"`-format, no
  dashes) are formatted without hyphens on this particular reference-server instance, while Tinyfin
  formats every ID with the canonical dashed UUID form throughout the whole project (existing,
  pre-this-task convention — `formatUUID`). Not something this task's `ThemeMedia` work introduced or
  should special-case; noted only so a future byte-for-byte ID-format audit isn't surprised by it.
