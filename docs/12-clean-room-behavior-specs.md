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
against the reference server showed and what `docs/11-jellyfin-web-route-coverage.md`'s
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

### Implementation findings (Phase 2, black-box against the reference server)

The grouping toggle mentioned above is the `groupItems` query param (any capitalization),
confirmed live: omitted or anything but exactly `false` groups; `groupItems=false` returns the
flat list. `docs/11`'s prior note that jellyfin-web's own bundle always requests grouped results
still holds — `groupItems` was not observed absent from a real jellyfin-web call.

### Fully watched series are excluded (implemented)

Confirmed live against the reference server, black-box only (no reference source read for
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
  Episode/Season rows a per-series item fetch happens to return (a bounded, recent window — see
  `latestSeriesItemFetchLimit`, `internal/api/latest_grouping.go`). Reproduced live: marking every
  episode of a
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

### Candidate-series ranking must not be biased by episode count (implementation note)

Not a reference-server behavior question — step 1's "look at everything recently added across the
scope, grouped by which series" is unambiguous — but a real implementation bug found live against
a user's own library, worth recording so it isn't reintroduced: an earlier version of
`tvShowsLatestGrouped` approximated step 1 with a single bounded `QueryItems` call (a raw-item pool
sorted by `DateCreated`, capped around 200 items for the default `limit=20`) and only grouped by
series *after* that pool was fetched. A library scanned with a lopsided episode-count split across
a handful of shows could fill that pool entirely with the high-episode-count shows' own rows before
a lower-episode-count show scanned moments earlier ever contributed a single row — that show then
never appeared in the grouped result at all, at any `limit`, because it never reached the grouping
step in the first place. Reproduced with real data: four shows (13/50/150/103 episodes) scanned
within a couple of seconds of each other; the 103-episode show, scanned first (so its rows sorted
just past the 200-item pool's cutoff once the other three's 213 combined rows filled it), never
appeared.

Fixed by ranking series *before* fetching any items: `storage.ItemStore.QuerySeriesLatestActivity`
(a `GROUP BY series_id` / `MAX(date_created)` aggregate) ranks distinct series directly, and only
the already-ranked series get their own items fetched (bounded per series,
`latestSeriesItemFetchLimit`) to resolve a card. A series' presence in the ranking no longer
depends on how many rows other series in the same scan happen to contribute — see
`storage.SeriesActivity`'s doc comment and `TestItemsLatestSeriesGroupingHighEpisodeCountSeriesDoesNotHideOthers`
(`internal/api/latest_grouping_test.go`).

One more thing surfaced during live comparison that is real, reproducible reference-server
behavior but is **not** implemented by Tinyfin's `internal/api/latest_grouping.go`, logged here
rather than chased further per the scope note above:

- **A series whose only recent-addition burst is roughly a month or older than the request time
  sometimes still resolves to a single-episode card**, even though its own items, read directly,
  clearly meet this section's stated 24-hour-trailing-window/bulk/multi-season criteria for a
  season or series card. Reproduced consistently (stable across `limit` values from 3 to 300) on
  one real 4-season series in the test catalog, so it isn't request-limit-dependent the way the
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
the reference server for the same title (an example movie): after a rescan, Tinyfin's
response showed `ImageTags: {"Primary":..., "Logo":..., "Banner":...}` and `BackdropImageTags:
["1"]` — matching the reference server's own response shape for that field set.

One real gap found *during* this live comparison, out of scope for this task and not fixed here: the
reference server's response for the same title also carries `ImageTags.Thumb` and `ImageTags.Disc`
(from a `<video-basename>-thumb.jpg` and a `disc.png` sitting in the same folder) — Tinyfin's
`imageTagsFor` would already surface these correctly *if* the scanner recorded them, but
`internal/scan/scan.go`'s `seedFolderImages` has no filename recognition for `Thumb`/`Disc` at all
(only `Primary`/`Backdrop`/`Logo`/`Banner`, the four kinds this task's Logo/Banner section covers).
Worth a small follow-up pass, same shape as the Logo/Banner section below.

### C# source cross-check (Phase 1, done after the fact on the maintainer's explicit request — this
### section was originally written from the OpenAPI schema and public docs alone; re-verified
### against the reference server's own DTO-assembly source, not just its published contract)

Confirmed correct, no changes needed: the map-vs-array split (`ImageTags` for single-instance kinds,
a separate `BackdropImageTags` array) and the "absent tag = client never requests the image" behavior
both match the reference server's own image-tag assembly code exactly.

One real mechanism this section's earlier OpenAPI-only pass had no way to know about, since it's
response-building logic, not a wire shape: when an item has **no image of its own** for `Logo`/`Art`/
`Thumb`/`Backdrop`, the reference server walks up to the item's parent and, if the parent has one,
surfaces it via a *different* field (`ParentLogoImageTag`+`ParentLogoItemId`,
`ParentBackdropImageTags`+`ParentBackdropItemId`, etc. — already listed as out-of-scope structural
fields in this doc's earlier OpenAPI-derived pass, but their *purpose* wasn't understood until now).
This inheritance doesn't apply to a movie (a movie has no meaningful parent for this purpose) so it
doesn't change anything implemented in this section — but it's very likely relevant for the
Episode/Season/Series item types (an episode with no logo of its own inheriting its series' logo),
which is outside this section's movie-detail-page scope. Flagged here for whichever gap covers
episode/season detail pages next, rather than guessed at further.

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
same deterministic-`ImageRef`-ID upsert. Confirmed live: the real movie folder used for the
`ImageTags` validation above already had both `logo.png` and `banner.jpg` sitting on disk (left there
by a prior manual test-fixture setup, not something this task added) — before this fix, a rescan
recorded neither; after, `GET /Items/{itemId}/Images/Logo` and `.../Images/Banner` both returned 200
with the real file bytes (`image/png` 78KB and `image/jpeg` 135KB respectively), and both `ImageTags`
keys appeared as documented in the section above.

### C# source cross-check (Phase 1, done after the fact on the maintainer's explicit request — this
### section was originally written from `jellyfin.org`'s public docs alone; re-verified against the
### reference server's own local-image-recognition source)

Confirmed correct: the local image resolver tries `"logo"` then falls back to `"clearlogo"` for the
Logo kind, and `"banner"` for Banner — exactly what this section already documented and what's
already implemented. No changes needed for Logo/Banner.

One real gap the C# source surfaced that neither the public docs nor black-box testing had, since it
only shows up for a title that happens to lack a `fanart.jpg`: the reference server's Backdrop
resolution is richer than Tinyfin's `backdrop := []string{"fanart.jpg", "fanart.png", "backdrop.jpg"}`
— it also recognizes `background.*` and `art.*` (same standalone/suffix pattern), a *numbered* series
(`fanart2.jpg`, `fanart3.jpg`, ...) beyond just the first match, and an `extrafanart/` subfolder whose
every image file counts as an additional backdrop. Tinyfin's `seedFolderImage` stops at the first
match per kind and never looks for more than one Backdrop file at all — a real, live-confirmed gap
this task didn't set out to close (the test title only has a single `fanart.jpg`, so the difference
never showed up during this task's own live comparisons) and isn't fixed here. Worth its own future
pass if a multi-backdrop movie ever surfaces the difference in practice.

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
live once a test title with a theme file exists in the catalog, rather than trusting this
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

- **Empty shape**: confirmed against a title with no theme files — 200, all three blocks
  present with `Items: []`, matching the reference server's own response for the same title
  (the reference server, same library, empty on both servers since neither had a
  theme file for it).
- **Populated shape**: confirmed by writing a real `theme.mp3` into the same movie folder
  used for the ImageTags/Logo/Banner validation above and requesting `GET
  /Items/{itemId}/ThemeMedia` again with no rescan in between — `ThemeSongsResult.Items` came back
  with one entry (`Name: "theme"`, `Type`/`MediaType: "Audio"`, a stable deterministic `Id`),
  `ThemeVideosResult` stayed empty (no `backdrops/` folder was added), `TotalRecordCount`/`OwnerId`
  matched. The test file was deleted immediately after, and a follow-up request confirmed the
  response reverted to fully empty — no leftover state, consistent with this being resolved at
  request time rather than persisted.
- One minor formatting note found during the live comparison, not a functional divergence:
  the reference server's own IDs (including `SoundtrackSongsResult.OwnerId`, `"N"`-format, no
  dashes) are formatted without hyphens on this particular reference-server instance, while Tinyfin
  used to format every ID with the dashed UUID form. That convention is gone: every ID is now minted,
  stored and emitted as 32 lowercase hex digits (`internal/guid`, `wire.id.R1`), and dashed or
  upper-case input is folded to that form on the way in (`wire.id.R2`).

### C# source cross-check (Phase 1, done after the fact on the maintainer's explicit request — this
### section was originally written from `jellyfin.org`'s public docs alone; re-verified against the
### reference server's own extras-recognition source)

Confirmed correct, no changes needed: the reference server's own filename-convention table lists
exactly `"theme-music"` (directory) and `"backdrops"` (directory) mapped to theme-song/theme-video
respectively, plus a standalone filename `"theme"` also mapped to theme-song — matching this
section's public-docs-derived write-up exactly, so nothing here needed correcting.

One architectural fact the C# source revealed that the public docs didn't (and that black-box testing
alone couldn't have, since it's an implementation strategy invisible on the wire): the reference
server does **not** resolve theme media live per request the way `theme_media.go` does — it
catalogues theme songs/videos as ordinary library items at scan time (tagged as "extras" owned by the
parent item) and `GetThemeSongs`/`GetThemeVideos` just queries that catalogue. This is a backend
strategy difference, not a wire-visible one (the response shape is identical either way, confirmed by
this task's own live populated-shape test above), and `theme_media.go`'s own doc comment already
argued request-time resolution is equally defensible under `docs/08`'s scanner boundary — so this
doesn't change what's implemented, just confirms the choice was a real design decision, not an
accidental shortcut.

Also found: the reference server recognizes a *far* broader set of audio container extensions for a
theme song (roughly 80, including several niche tracker/module formats like `.it`/`.mod`/`.s3m` no
real theme-song file is realistically encoded in) than `theme_media.go`'s
`themeAudioExtensions` (7: mp3/flac/m4a/ogg/wav/aac/opus). Deliberately not matched — expanding to
cover formats no one actually uses for this purpose would be scope creep with no real benefit, and
every extension a real theme file would plausibly use is already covered. Noted here so this is a
documented, considered gap rather than something that looks like an oversight.

---

## Movie detail page — People (cast & crew) response ordering

### The gap this resolves

`BaseItemDto.People` itself (which credits appear, with what `Name`/`Role`/`Type`) is this project's
own TMDB integration, not a reference-server behavior to extract — TMDB's public API is unrelated to
the clean-room requirement (see `internal/metadata/tmdb.go`'s own doc comment on `TMDBProvider`).
But while re-reading the reference server's source for the three gaps above (done on the maintainer's explicit request, after an earlier pass had
substituted a different method), its DTO-assembly code turned out to also
define a real, wire-visible **ordering** rule for the `People` array — independent of *where* the
person data came from — that neither this project's TMDB integration nor its earlier
OpenAPI-schema-only pass had any way to know about, since an array's element order isn't part of a
JSON schema. This section documents that rule and folds it in.

### Behavior, in prose

The reference server orders a `People` array by two keys:

1. **Primary: each person's own billing/sort-order number**, when one exists (present for cast —
   TMDB's own "billing order" concept — absent, and so tied with every other tied entry, for crew).
2. **Secondary (tie-break): a fixed type-priority table** — Actor first, then Guest Star, then
   Director, then Writer, then Producer and Composer tied at the next slot, then everything else
   last. Two people tied on both keys keep their original relative order (a stable sort).

In practice, for a person set built the way this project's TMDB integration builds one (actors carry
a real billing order, crew doesn't), this reduces to: actors first in billing order (already how
`creditsToPeople` orders them), then crew ordered `Director` → `Writer` → `Producer`, regardless of
whatever order the upstream data source happened to list crew members in.

### Implementation findings

Implemented as `internal/metadata/tmdb.go`'s `personTypePriority` map plus a `sort.SliceStable` pass
over the combined cast+crew slice in `creditsToPeople`, added after the cross-check above (this
project's crew list was previously left in whatever order TMDB's own `crew` array happened to list
it, which is not guaranteed to be Director/Writer/Producer order — confirmed as a real, live-able bug
by reasoning through the reference server's own ordering code, not by observing a live divergence,
since no test title's crew data happened to be in reference-server-mismatching order during this
task's own testing). Covered by a new unit test
(`TestCreditsToPeopleOrdersCrewByTypePriority`) that deliberately feeds crew in reverse
(Producer, Writer, Director) order and asserts the output comes back Director/Writer/Producer —
proving the fix reorders rather than passes input through. Not independently live-verifiable against
the reference server for this project's own People feature specifically, since the test deployment has no TMDB
key configured (same limitation noted in the People implementation this section corrects).

---

## Episode/movie detail page — MediaStream.DisplayTitle

### The gap this resolves

The episode detail page revalidation found the video/audio track-selector panel doesn't render at
all on Tinyfin, because its `MediaStreams` entries carry only the raw probe fields (`Codec`, `Type`,
`Index`, `Width`/`Height`, `Channels`, a few booleans) and are missing a formatted `DisplayTitle`
string the client needs to label each track (real example captured live, English locale: `"Video: 360p HEVC SDR"` /
`"Audio: Und Stereo - AAC - Stereo - Default"` — the `"Video:"`/`"Audio:"` prefix is the
client's own label, everything after it is `DisplayTitle`). This isn't derivable from the OpenAPI
schema (which only documents the field's type, not how its value is computed) or from a couple of
black-box examples with confidence, so the reference source was read for this one specific
algorithm.

### Behavior, in prose

`DisplayTitle` is a formatted label built per-stream from that stream's own already-known
fields — not a separate lookup, and not something a provider or scanner needs to supply; it's pure
string formatting over data Tinyfin's own prober already has or can easily add. The algorithm
differs by stream type:

**Video streams**: join, in order, whichever of these apply (space-separated), falling back to
`""`/omitted if none apply:
1. A resolution label derived from pixel width/height via fixed breakpoints, each bounded by both a
   max width and a max height, matched in order (first-fit), with an interlaced (`i`) vs progressive
   (`p`) suffix based on whether the stream is interlaced:

   | max width | max height | label |
   |---|---|---|
   | 256 | 144 | 144 |
   | 426 | 240 | 240 |
   | 640 | 360 | 360 |
   | 682 | 384 | 384 |
   | 720 | 404 | 404 |
   | 854 | 480 | 480 |
   | 960 | 544 | 540 |
   | 1024 | 576 | 576 |
   | 1280 | 962 | 720 |
   | 2560 | 1440 | 1080 |
   | 4096 | 3072 | 4K |
   | 8192 | 6144 | 8K |

   Note the height bound is *not* simply the label's own resolution for the 540p/720p/1080p rows
   (544/962/1440, not 540/720/1080) — this is exact, not an approximation, confirmed directly against
   `GetResolutionText()` in the reference server's `MediaStream.cs`. A pair that doesn't fit any single
   breakpoint's width **and** height together (past the last breakpoint, or an oddly-proportioned frame
   that clears one bound but not the other) yields no resolution label at all rather than guessing.
   4K/8K have no `i`/`p` suffix.
2. The codec name, uppercased (e.g. `"hevc"` → `"HEVC"`).
3. A dynamic-range label: `"HDR"` when the stream is HDR content, `"SDR"` when it's ordinary
   dynamic range, omitted entirely when unknown. (A Dolby Vision stream gets a more specific label
   than plain "HDR" — out of scope to fully specify here since Tinyfin has no Dolby Vision detection
   today; treat as a future refinement, `"HDR"`/`"SDR"` covers the ordinary case this gap is about.)

If the stream has its own explicit `Title` (a name embedded in the file's own stream metadata, e.g.
a container's per-track title tag) instead of no title, the format changes: start from that `Title`
verbatim, then append `" - "` + each of the attributes above **only if that attribute's text isn't
already a substring of `Title`** (case-insensitive) — avoids a redundant `"1080p - 1080p"`-style
label when the file's own metadata already says what an attribute would add.

**Audio streams**: same two modes (has its own `Title` vs. not), attributes computed differently:
1. The stream's language, if set and not a placeholder/unset marker — its localized display name.
   Tinyfin has no localization system for this, so it always uses the *English* display name (e.g.
   `"por"` → `"Portuguese"`) rather than the raw three-letter code or a client-locale-specific name —
   confirmed not to be the raw code via a live byte-for-byte comparison, see "Implementation
   findings" below.
2. Either the stream's codec *profile* (if set and not the generic value abbreviated `"lc"`), used
   verbatim, **or**, only when there's no usable profile, a friendly codec name: `"ac3"` → `"Dolby
   Digital"`, `"eac3"` → `"Dolby Digital+"`, `"dca"`/`"dts"` → `"DTS"`, anything else → the codec
   code uppercased.
3. The channel layout string if the prober reports one (e.g. `"5.1"`/`"stereo"`, capitalized), else
   a plain `"<N> ch"` count when only a channel count is known.
4. `"Default"` when the stream is flagged as the default track for its type.
5. `"External"` when the stream comes from an external/sidecar file rather than being embedded.
6. `"Original"` when the stream is flagged as the original-language track.

Join whichever of these apply with `" - "`. With an explicit `Title`, same "append only if not
already a substring" merge rule as video streams above.

**Subtitle streams** (out of scope for the movie/episode gaps that motivated this section, recorded
for completeness since it's the same field/mechanism): language (its English display name, same
lookup as the audio case above; or an "Und"/undefined label when unset) first, then `"Hearing
Impaired"`/`"Default"`/`"Forced"` flags as applicable, then the codec uppercased, then `"External"`
when applicable — same `Title`-substring-merge rule.

### Scope note

This is a pure formatting function over fields a prober already extracts (or easily could) — no
network calls, no provider involvement, no per-item storage needed beyond what a `MediaStreamRef`
already carries or can be extended to carry (`Title`, `Profile`, `ChannelLayout`, `IsDefault`,
`IsExternal`, `IsOriginal`, `IsInterlaced`, dynamic-range classification). Implement as a Go function
computed at response-build time (same place `mediaStreamDto`/`mediaStreamDtos` already lives in
`internal/api/dto.go`), not something to persist — recomputing it on every response keeps it correct
if the underlying fields are ever corrected without needing a rescan.

### Implementation findings

Implemented in `internal/api/display_title.go` (`displayTitleFor` and its per-type
`videoDisplayTitle`/`audioDisplayTitle`/`subtitleDisplayTitle` helpers), called from `dto.go`'s
`mediaStreamDtos` once per stream. `storage.MediaStreamRef` (`internal/storage/item.go`) gained every
field this algorithm needs — `Profile`, `ChannelLayout`, `IsExternal`, `IsOriginal`, `IsInterlaced`,
plus the passthrough fields `TimeBase`/`AspectRatio`/`RefFrames`/`PixelFormat`/`Level`/`SampleRate`/
`ColorTransfer` — no schema migration needed, `media_streams` is a single JSON `TEXT` column
(`0001_init.sql`), so a new Go struct field just serializes into it. `internal/scan/probe.go`'s
`mediaStreamsFromProbe` was extended to populate all of these from `ffprobe -show_streams`' own JSON
output (`field_order` for `IsInterlaced`, `disposition.original` for `IsOriginal`, `color_transfer` for
the dynamic-range classification, ...).

**Resolution breakpoints** (`resolutionBreakpoints` in `display_title.go`): the exact width/height table
above (including the non-obvious 544/962/1440 height caps on the 540p/720p/1080p rows) was read directly
from `GetResolutionText()` in the reference server's `MediaStream.cs`, not approximated — an earlier pass
had incorrectly implemented these three height caps as the label's own round number (540/720/1080) before
this was caught by a direct re-comparison against that C# method; fixed to match exactly. A width/height
pair that doesn't fit any single breakpoint's width **and** height together yields no label, per the
spec's own "rather than guessing" instruction for the past-the-end case.

**Dynamic range**: per this task's explicit instruction (not the prose above, which said "omitted
entirely when unknown") — `color_transfer` values `smpte2084`/`arib-std-b67` classify `"HDR"`; every
other recognized value, and the field being absent entirely, classifies `"SDR"` rather than being
omitted. No Dolby Vision detection (documented future refinement, both here and in the prose above).

**A real, pre-existing gap found via live comparison, not introduced by this change**: comparing
`DisplayTitle` output byte-for-byte against the reference server for a real
file (`Example Series (1997) - S01E01`) surfaced that `mediaStreamsFromProbe` never extracted a
stream's own embedded `tags.title`/`tags.language` into `MediaStreamRef.Title`/`.Language` at all —
both fields already existed on the struct, nothing ever populated them. This file's audio track carries
an embedded title (`"Und Stereo"`, confirmed via `ffprobe` directly), so DisplayTitle's
"explicit Title" merge-rule branch had never actually fired for any real file before this fix. Now
extracted (`probe.go`'s `ffprobeStream.Tags`); see the byte-for-byte comparison below.

**Live byte-for-byte comparison** (same file, both servers, after a `POST /Library/Refresh` rescan on
Tinyfin to pick up the extended prober):

| Stream | Reference server | Tinyfin |
|---|---|---|
| Video | `"360p HEVC SDR"` | `"360p HEVC SDR"` — exact match |
| Audio | `"Und Stereo - AAC - Stereo - Default"` | `"Und Stereo - AAC - Stereo - Default"` — exact match |

With an English-locale client the output matches. The accepted, documented gap from this
section's own "Behavior, in prose" and "Scope note": when the client's locale is not English, the
reference server localizes some attribute labels (for example, a pt-BR client receives a localized
word instead of `"Default"`); Tinyfin has no localization system for this and always emits the
English label the spec's own wording uses — not a bug, and not something this task asked to fix.

**A second real gap found via live comparison, not introduced by this change**: a further
byte-for-byte comparison (Playwright capturing jellyfin-web's own traffic against both
servers, same `.mkv`) on a different file — `Example Series (1986) - S01E06.mkv`, audio track 1
(`language=por`, `title="Português (Example)"`, profile `LC`) — surfaced that the "language" attribute
in point 1 above was implemented as the raw three-letter code (`s.Language`, e.g. `"por"`) rather
than that code's English display name (`"Portuguese"`). The reference server's own `DisplayTitle` for
this exact stream was `"Português (Example) - Portuguese - AAC - Stereo - Default"` (the title is a
captured pt-BR-localized example of an embedded stream title); Tinyfin's was
`"Português (Example) - AAC - Stereo - Default"` — missing the `"Portuguese"` attribute entirely: the
merge-with-title rule appends each attribute unless it's already a substring of `Title`, and the raw
code `"por"` *is* a substring of `Title` (`"Português (Example)"` contains "por"), so the old code's
bare-code attribute got silently swallowed by the merge rule on this exact file — a second-order
effect of the raw-code bug that a language-name-only test case wouldn't have caught. Fixed by adding
a `languageName` lookup table (`display_title.go`, same map-plus-uppercase-fallback shape as
`friendlyAudioCodecName`) mapping common ISO 639-2/B codes to their English name, used in place of
the raw code in both `audioDisplayTitle` and `subtitleDisplayTitle`. `"Portuguese"` is not a substring
of `"Português (Example)"`, so it now survives the merge and appends correctly. An unrecognized code
still falls back to itself uppercased (matching `friendlyAudioCodecName`'s own fallback), rather than
failing or guessing.

**Live comparison after the language-name fix** (same file/stream, both servers):

| Server | `DisplayTitle` (audio track 1) |
|---|---|
| Reference server | `"Português (Example) - Portuguese - AAC - Stereo - Default"` |
| Tinyfin, before fix | `"Português (Example) - AAC - Stereo - Default"` — missing `"Portuguese"` |
| Tinyfin, after fix | `"Português (Example) - Portuguese - AAC - Stereo - Default"` |

For non-English client locales, the same accepted localization gap documented above applies — not
something this fix touches.

---

## TMDB enrichment — Series/Season, Studios, EndDate

Not a clean-room section — TMDB is a third-party API `internal/metadata`'s `TMDBProvider` calls
directly over its own published REST contract, unrelated to the reference server's own source (see
`tmdb.go`'s package doc comment). Documented here only because this file is where enrichment
behavior notes live, not because it needed spec extraction from anywhere.

Extended `lookupSeries` to request `append_to_response=credits,external_ids` alongside the plain
`GET /tv/{id}` call (same technique `lookupMovie` already used for `credits` alone — TMDB accepts a
comma-separated resource list in one `append_to_response` parameter), so a Series `Result` now also
carries `People` (via the existing `creditsToPeople`), `ExternalIDs["Imdb"]` (from
`external_ids.imdb_id`), and `Studios` (from the `networks` array `GET /tv/{id}` already returns
natively, no extra append needed — mapped generically in `detailsToResult` so `lookupMovie` picks up
the same field for free, harmlessly empty since a movie response has no `networks`).

`EndDate` is set from `last_air_date` only when TMDB's own `status` field is one of its two
documented terminal values, `"Ended"` or `"Canceled"` (`seriesHasEnded`) — every other status
(`"Returning Series"`, `"In Production"`, `"Planned"`, `"Pilot"`) means more episodes are still
expected, so `last_air_date` (the most recently aired episode, not a finale) would be a misleading
`EndDate` for those.

Added a new `metadata.KindSeason` alongside `lookupSeason`: resolves the parent series via the
existing `resolveSeriesID`, then calls `GET /tv/{id}/season/{season_number}`, mapping `name` →
`Result.Title` and `poster_path` → a `Primary` image, same pattern `lookupEpisode` already uses for
`still_path`. No `append_to_response` support exists for a season endpoint on TMDB's side, so a
Season `Result` carries no `People`/`Studios`/`ExternalIDs` beyond the series' own `Tmdb` id.
`internal/scan`'s `walkSeasons` now enriches each season the same async
enqueue-enrich-then-reupsert pattern the series/movie/episode walks already use — previously a
Season item was created once with the generic `"Season N"` name and never enriched at all.

No language parameter is sent on any `TMDBProvider` call, this one included, so TMDB's own default
(`en-US`) applies throughout; a season's real localized name (e.g. a localized "Season 1" label for a `pt-BR`
request) is a possible later enhancement, out of this task's scope.

Not live-validated against real TMDB data: the test deployment had no TMDB API key configured (a
known, previously-documented limitation — see this file's People/cast section above for the same
caveat). Covered instead by unit tests with a fake HTTP server (`internal/metadata/tmdb_test.go`)
and a fake `Provider` double (`internal/scan/scan_test.go`). The degraded (no-key) path was checked
at the deploy level: Tinyfin rebuilt with this change restarted cleanly, its `0003_studios`
migration applied automatically (schema version 2 → 3, confirmed via a read-only `sqlite3` query)
with no error/panic in the server logs, and pre-existing items came through with a
correctly-defaulted, empty `studios` column — the no-enrichment shape this change is supposed to
degrade to.

---

## Shows/Episodes — no default page cap

### The gap this resolves

`GET /Shows/{seriesId}/Episodes` (the season-detail-page episode list). Real-traffic comparison
via Playwright, running the same bundled jellyfin-web build against both Tinyfin
and the reference server pointed at an identical library (a series with a
single 153-episode season), surfaced a divergence: the request jellyfin-web actually sends for
this page never includes a `Limit` or `StartIndex` query param at all
(`GET /Shows/{seriesId}/Episodes?SeasonId={seasonId}&UserId={userId}&Fields=...`). Both servers
reported `TotalRecordCount: 153`, but only the reference server's `Items` array actually held all
153 — Tinyfin's held 100, silently truncated by the same generic 100-item default this project
applies to plain library listings. Black-box comparison confirmed the discrepancy but not *why*
the reference server doesn't also cap at some default — that's what this section resolves, read
from the reference source under the clean-room process (`AGENTS.md`).

### Behavior, in prose

This endpoint's paging is opt-in per request, not a fallback the server applies on the caller's
behalf. Internally, the endpoint always assembles the *complete* matching episode list first
(every episode of the requested season, or of the whole series when no season is specified),
applies any non-paging filters (a missing-episode filter, an "adjacent to" trim, an explicit
start-item skip), and only *then* checks whether the incoming request itself carried a start
index or a limit value. If neither was present on the request, no windowing step runs at all —
the full, already-filtered list is what gets returned, with no smaller default substituted in its
place. Windowing only happens when the caller explicitly asked for it by sending one or both of
those two params.

In other words: there is no reference-side default page size for this endpoint whatsoever, not
even a larger one than Tinyfin's generic 100 — "the client didn't ask for paging" and "return
everything" are the same case here. The `TotalRecordCount` the reference server reports is simply
the size of that same complete, filtered list — which is why it already agreed with Tinyfin's
(also-correct) total even while the two servers' `Items` arrays diverged: both computed the same
true count, but only the reference server also returned that many rows in the absence of an
explicit `Limit`.

This is a narrower rule than the generic library-listing endpoint (`GET /Items`), which does fall
back to a real default page size when the client omits `Limit` — that endpoint's behavior isn't
in question here and shouldn't change. The divergence is specific to the show/episode-listing
endpoint not sharing that same fallback.

### Scope note for the implementer

Implement by mirroring the "only window when the request actually asked for it" behavior: when
the incoming request supplies an explicit, positive `Limit` and/or `StartIndex`, honor them
exactly as the generic listing endpoint already does elsewhere in this codebase (same
parse-and-clamp pattern). When neither is present, do not substitute any fixed default — the
result set should be exactly what matches, unbounded by any page-size constant. Since this
project's storage layer requires a strictly positive `Limit` on every query (`docs/05`, `O3`),
"unbounded" has to be expressed as a limit value high enough that it is never the actual
constraint for any real library — not as a literal absence of a limit — the same accommodation
the storage interface already forces on every other caller of it.

### Live validation

Confirmed against Tinyfin after the fix (same series/season used
above): an authenticated `GET /Shows/{seriesId}/Episodes?seasonId={seasonId}` with no `Limit` in
the request now returns all 153 items, matching `TotalRecordCount`.

---

## Season detail — local per-season poster artwork recognition

### The gap this resolves

A real traffic comparison (jellyfin-web bundled against Tinyfin and
the reference server, both pointed at the same read-only media directory) found the Season 1
card for "Example Series" rendering as a gray placeholder on Tinyfin — no `ImageTags.Primary` — while
the reference server showed the real poster for the exact same on-disk files. The series' root
folder (`.../Example Series (1986)/`) has `poster.jpg`, `fanart.jpg`, `tvshow.nfo`, and a
`season01-poster.jpg` sitting alongside them; `internal/scan/scan.go`'s `walkSeasons` never looked
for any local artwork at all for a Season item — `seedFolderImages`/`seedFolderImage` (this
project's existing Kodi-filename-convention artwork lookup, see the "Movie detail page — local
Logo/clearlogo artwork recognition" section above) were only ever called for Movie and Series
items. TMDB-sourced season images were already wired correctly through `enrich`'s generic
`res.Images` handling (`storeProviderImage`) — this gap was purely the local-file half, and is
blocked from live TMDB validation only by the test deployment having no TMDB API key configured (a known,
separately documented limitation, not a bug).

### Behavior, in prose

Source: Kodi's own published user documentation (kodi.wiki's "Artwork" guide), the same public
naming-convention source `seedFolderImages`'s existing doc comment already cites for
`poster.jpg`/`fanart.jpg`/`logo.png`/`banner.png` — not the reference server's C# source, and not
subject to this project's clean-room process (`docs/00-decision-log.md`'s D12) for that reason,
same category as the rest of this file's Kodi/`jellyfin.org`-sourced artwork sections:

- A season-specific poster is **not** stored inside the season's own subfolder (`Season 01/`) — it
  lives directly in the **series root** folder, the same folder that already holds the series'
  own `poster.jpg`/`fanart.jpg`/`tvshow.nfo`.
- Its filename is `season{NN}-poster.jpg`, where `NN` is the season number zero-padded to two
  digits: `season01-poster.jpg`, `season02-poster.jpg`, etc. Kodi also documents `season{NN}.jpg`
  and `seasonNN.tbn` as accepted variants for the same artwork slot.
- This is the Primary image kind only (a season poster) — Kodi's guide documents season-level
  Backdrop/Banner/Logo variants too, but no real library file for any of those was found during
  this investigation, so only the poster form is covered.

### Fix, described behaviorally (not an algorithm to extract — just wiring)

A new local-artwork lookup, run for every Season item, tries — in the series root folder, not the
season's own subfolder — `season{NN}-poster.jpg`, `season{NN}.jpg`, `season{NN}-poster.png`,
`season{NN}.png` in that order, first match wins, same "local always exists, provider overwrites
later via the same deterministic `ImageRef` ID" rule `seedFolderImages`'s doc comment already
states for Movie/Series. This local lookup must run *before* `enrich` so a locally-provided season
poster is never left unset just because no Provider is configured (matches the existing ordering
for Movie's `seedFolderImages` → `enrich` and Series' `seedFolderImages` → `enrich` in
`walkMovies`/`processMovie` and `walkSeries`).

### Implementation findings

Implemented as `internal/scan/scan.go`'s `seedSeasonImage`, a new function built directly on top
of the existing `seedFolderImage` helper (same candidate-list-first-match pattern the
Primary/Backdrop/Logo/Banner lists already use) rather than a new resolution algorithm — the only
new thing is passing the series root path instead of the season's own folder, and building the
candidate filenames with the season number. Called from `walkSeasons` right after the season
item's own upsert, before its `enrich` job is enqueued. Covered by
`TestScanLibrarySeasonPosterDetected` in `internal/scan/scan_test.go`.

Confirmed live against Tinyfin for the exact series/season from the original
comparison (Season 1, real `season01-poster.jpg` in the media directory):
before the fix, `GET /Shows/{seriesId}/Seasons?userId=...` returned no `ImageTags` entry at all for
Season 1; after a rebuild and a triggered rescan (`POST /Library/Refresh`), the same call returns
`ImageTags: {"Primary": "1"}`, and `GET /Items/{seasonId}/Images/Primary` serves 54,247 bytes,
MD5-identical to the on-disk `season01-poster.jpg` — matching the reference server's own behavior
for this title.

---

## Season/Series/BoxSet UserData — `UnplayedItemCount`

### The gap this resolves

Real-traffic comparison via Playwright (same bundled jellyfin-web build against
Tinyfin and the reference server, "Example Series" — a single, 153-episode
season) showed `GET /Shows/{seriesId}/Seasons` on the reference server returning
`UserData.UnplayedItemCount: 152` on the Season 1 item (152 of 153 episodes unwatched), while
Tinyfin's equivalent response omits the key from `UserData` entirely. This is the field that
actually drives the season card's unplayed-count badge in jellyfin-web — not `ChildCount`, which
the reference server doesn't even return on this endpoint (it stays `null` there regardless of the
`Fields=ItemCounts` request parameter). This section is the behavior spec for computing it.

### Behavior, in prose

Only a folder-shaped item that is meant to aggregate its children's watched status carries this
field at all — a plain synthetic browsing view (a library root, a "Favorites"-style aggregate
view, or anything not a real, on-disk library entry) never does, because walking its children for
this purpose is considered too expensive/meaningless for that kind of node. Series, Season, and
BoxSet are the ordinary library-entry folder types this applies to.

For an item that qualifies, the server first works out the *complete set of real, playable leaf
descendants* underneath it — every item, at any depth, that is not itself a sub-folder and is not
a placeholder standing in for an episode that has no real file (a missing/unaired-episode marker).
This is a genuinely recursive walk: for a Series specifically, its immediate children are Seasons,
which are themselves folders, so the leaves that actually count are the Episodes one level further
down, not the Seasons. A special (an episode filed under a "season zero") is an ordinary episode
for this purpose — nothing excludes it. An episode that has no on-disk media file but isn't a
formally-tracked "missing episode" placeholder is still counted (only the formal placeholder
marker is excluded, not "any item lacking a file" in general).

Once that leaf set is known, the server counts two numbers: how many of those leaves the
requesting user has marked played, and the total leaf count. `UnplayedItemCount` is simply
`total - played`. When the total is zero (an empty folder), the count is zero. This is computed
per request, for the requesting user — it is not a value stored anywhere, since "played" is itself
per-user state.

### Scope note for the implementer

Tinyfin's hierarchy is shallow enough (`Series -> Season -> Episode`, nothing deeper) that a
general recursive-descendant walk isn't needed to get the same leaf set the rule above describes:

- For a **Season**, its own direct children already are the leaf Episodes — no deeper level
  exists to recurse into.
- For a **Series**, the leaf Episodes are not its direct children (its direct children are
  Seasons), but every Item this project scans already carries a direct series-id back-reference on
  the Episode row itself (populated at scan time, independent of the Season/Series parent chain) —
  so the leaf set for a Series is reachable in one query keyed on that back-reference, without
  walking through Seasons at all.
- **BoxSet** membership beyond a direct parent link isn't wired up in this codebase yet (same gap
  `ChildCount` already has for BoxSet, see that field's own doc comment in `internal/api/dto.go`)
  — so there is nothing to recurse into for a BoxSet beyond its direct children either, today.

A single bounded, indexed query per item (counting rows matching the relevant parent/series
reference for the requesting user, filtered to "not played") is enough — same performance
trade-off `internal/api/dto.go`'s existing child-count helper already documents and accepts (no
N+1 across a whole page, no unbounded scan; each page is itself bounded by the caller's own
`Limit`, AGENTS.md principle 1). No new aggregate table is needed.

### Live validation

Confirmed against Tinyfin after the fix (same series/season, with a
partial-watch playstate seeded for its episodes): an authenticated
`GET /Shows/{seriesId}/Seasons?userId={userId}` now returns a populated
`UserData.UnplayedItemCount` on the Season 1 item, reflecting the number of that season's episodes
not yet marked played for the requesting user.

---

## Season/Episode DTO — inherited `Parent*` image fields and `SeriesPrimaryImageTag`

### The gap this resolves

The same real-traffic comparison showed a real Jellyfin Season item carrying several
image-inheritance fields Tinyfin's equivalent response doesn't have at all:
`ParentLogoItemId`/`ParentLogoImageTag`, `ParentBackdropItemId`/`ParentBackdropImageTags`,
`ParentThumbItemId`/`ParentThumbImageTag`, and `SeriesPrimaryImageTag`. These let a client render a
season (or episode) card with the series' own logo/backdrop/thumb art and poster when the season
or episode doesn't have that art of its own — without them, a season card with no art of its own
renders with nothing at all instead of falling back to the show's branding, which is what the real
server's own card-building logic expects to be able to do. The same fields are missing from
Tinyfin's Episode response for the same reason.

### Behavior, in prose

`SeriesPrimaryImageTag` is unconditional: whenever the item belongs to a series (a Season or an
Episode) and that series has its own Primary (poster) image, this field always carries that
image's tag — regardless of whether the season/episode already has its own Primary image. It is
not part of the "only when the item itself lacks one" fallback pattern the other fields below
follow; it's a fixed "what does this item's show look like" reference a client can use for
show-level branding independent of the specific season/episode artwork. (A second, item-specific
fallback exists on the reference server for an Episode's own Primary image specifically —
`ParentPrimaryImageItemId`/`ParentPrimaryImageTag`, preferring the season's own poster over the
series' — but that pair is out of scope here: it wasn't part of the observed gap and isn't needed
today, since Tinyfin's Episode responses fall back to `ImageTags.Primary` already resolving
correctly once an episode or its season has art.)

The Logo/Backdrop/Thumb fields follow one shared rule: they only ever populate when the item
itself has no image of that same kind (checked against the item's own already-computed image
tags/backdrop list) — an item that has its own Logo, for instance, never gets a
`ParentLogoItemId`/`ParentLogoImageTag` pair at all. When the item does lack that kind of image,
the server walks its ancestor chain closest-first (an Episode's chain is its Season, then that
Season's Series; a Season's chain is just its Series) and uses the *first* ancestor in that order
that has an image of the missing kind — once one is found, closer ancestors are preferred and
further ones are never consulted for that kind. This is the same rule for Logo, Backdrop, and
Thumb alike, with one documented quirk on the reference server: for Thumb specifically, if the
series (the *last* ancestor in the walk) has its own Thumb image, the series' Thumb wins even over
a closer ancestor (the season) that also has one — every other kind, including Backdrop and Logo,
is strictly closest-ancestor-wins with no such override.

Each of these fields is a pair: the `*ItemId` half names which ancestor the art actually came from
(so a client can request the image from that ancestor's own image endpoint), and the
`*ImageTag`/`*ImageTags` half is that ancestor's own cache-busting tag(s) for the image, the same
tag value that ancestor's own `ImageTags`/`BackdropImageTags` would carry.

### Scope note for the implementer

The closest-ancestor-wins-except-Thumb-prefers-series quirk noted above only matters when more
than one ancestor in the chain can actually carry a Thumb image of its own. Tinyfin's scanner does
not recognize/store a Thumb-kind image for any item type today (no local-file detection for it
exists yet, unlike Primary/Logo/Backdrop/Banner) — so, as implemented now, that distinction is
unobservable: there is never a case where a season's own Thumb and its series' own Thumb both
exist and disagree. Implement the plain closest-ancestor-wins rule uniformly across Logo, Backdrop,
and Thumb for now; if Thumb-image detection is ever added to the scanner, revisit this specific
override before trusting Thumb inheritance in a mixed case.

`ImageBlurHashes` (blurhash placeholders embedded per image tag) is a related field the reference
server also populates alongside all of the above — **not implemented here**, same as this doc's
other already-noted image-processing gaps: it requires actual image decoding/blurhash generation,
a separate, larger feature than DTO field wiring. Documented as a known gap, not part of this
DTO's field set for now.

### Live validation

Confirmed against Tinyfin after the fix (same series/season, whose
series item has its own Primary/Logo/Backdrop art already indexed): an authenticated
`GET /Shows/{seriesId}/Seasons?userId={userId}` now returns `SeriesPrimaryImageTag`,
`ParentLogoItemId`/`ParentLogoImageTag`, and `ParentBackdropItemId`/`ParentBackdropImageTags` on
the Season 1 item, each pointing back at the series' own item id and image tags.

---

## Episode DTO — item-level video/permission/identity fields

### The gap this resolves

The same comparison, on `GET /Users/{id}/Items/{episodeId}`, found a real Jellyfin Episode
response carrying a further set of fields with no corresponding struct field in Tinyfin at all
(not merely unpopulated) — item-level `Width`/`Height` (distinct from the per-stream ones already
nested under `MediaStreams`), `IsHD`, `HasSubtitles`, `VideoType`, `PlayAccess`, `CanDelete`,
`CanDownload`, `LocationType`, `Etag`, `DisplayPreferencesId`, and `SeriesStudio`, plus a handful
of always-empty placeholders (`LocalTrailerCount`, `SpecialFeatureCount`, `RemoteTrailers`,
`LockedFields`) for features this project doesn't implement yet. This section covers the behavior
for each.

### Behavior, in prose

**Item-level `Width`/`Height`/`IsHD`.** These mirror the resolution of the item's own primary
video track (the same numbers the corresponding `MediaStreams` entry already carries for that
track) hoisted onto the item itself, for a client that wants resolution without digging through
the stream list. They're only meaningful for an item that actually has a video file behind it.
`IsHD` is a simple derived flag off that same height: true at 720 lines or taller, false (and, on
the reference server, omitted rather than sent as an explicit `false`) below that.

**`HasSubtitles`.** True whenever the item has at least one subtitle track among its media
streams, false (and omitted, same convention as `IsHD`) otherwise. Purely derived from the
already-known stream list, not separately stored.

**`VideoType`.** Describes what kind of on-disk structure the video came from — a plain video
file, versus a DVD- or Blu-ray-style disc folder structure. Tinyfin's scanner only ever recognizes
plain video files (it has no disc-folder-structure recognition at all), so this is always the
"plain file" value for any item that has a video file behind it, and absent otherwise.

**`PlayAccess`.** Reports whether the requesting user is allowed to play media at all, as a
coarse `"Full"`/`"None"` distinction tied to a playback-permission toggle on the user's account.
Tinyfin doesn't model that per-user toggle (there's no granular permission system yet — every
authenticated user can play everything), so this simplifies to always `"Full"` for any
authenticated caller.

**`CanDelete`/`CanDownload`.** On the reference server, `CanDelete` is true when the item is a
real, on-disk library entry (true for essentially everything reachable through normal browsing in
this project's scope, which has no remote/channel source concept at all) *and* the user is either
allowed to delete generally or specifically permitted to delete from that item's library —
Tinyfin doesn't model that per-user/per-folder permission grid, so this simplifies to "true for an
administrator, false otherwise." `CanDownload` follows a same-shaped but stricter rule: it is
never true for a folder-shaped item (Series/Season/BoxSet — the download permission only ever
applies to an actual playable file) even for an administrator, and — for a real playable item —
also depends on the same per-user download-permission toggle Tinyfin doesn't model, simplified the
same way: true for an administrator on a non-folder item, false otherwise.

**`LocationType`.** Reports where the item's content lives. Tinyfin only ever has two of the
reference server's possible values in practice, since it has no remote/channel source concept:
the "on local disk" value when the item has a real file path, and the "no file at all" value
(covering both a placeholder/virtual item and a folder-shaped item with nothing of its own on
disk) when it doesn't.

**`Etag`/`DisplayPreferencesId`.** Both are opaque, deterministic identifier strings the
reference server derives via a one-way hash — not values a client parses or attaches meaning to
beyond "did this change" (`Etag`) or "which display-preferences bucket does this item share with
others" (`DisplayPreferencesId`). `Etag` is derived from the item's own identity plus whatever the
server considers its "last modified" signal to be. `DisplayPreferencesId`, notably, is **not**
unique per item on the reference server for an ordinary (non-generic-folder) item type — it's
derived purely from the item's *type*, meaning every Episode, e.g., shares the same
`DisplayPreferencesId` as every other Episode; only a bare, undifferentiated folder item gets one
keyed off its own individual identity instead. Since neither value needs to match the reference
server's exact hash (they're opaque, per-server-instance identifiers, never compared across
servers by a client), Tinyfin can use any deterministic hash producing the same shape (a
fixed-length hex string) as long as it's stable across repeated requests for the same
item/item-type.

**`SeriesStudio`.** The first studio/network name in the owning series' own studio list, when the
series has one — a convenience so an episode/season card can show its network without a client
having to separately fetch the series. Absent when the series has no studio recorded.

**`LocalTrailerCount`/`SpecialFeatureCount`/`RemoteTrailers`/`LockedFields`.** These describe
trailers, bonus/extra content, and metadata-field-locking — none of which this project implements
as a concept yet. They're carried on the DTO as always-empty/always-zero placeholders so a client
that expects the keys to exist doesn't have to special-case their absence, without implying any of
those features actually work yet.

**Out of scope, deliberately not implemented here:** `Chapters` (chapter-marker extraction is a
separate, larger ffmpeg-probing feature — a distinct probe pass from the stream/format probing
already implemented — not part of this DTO-field-wiring pass) and `Trickplay` (scrubbing-preview
sprite generation, a separate feature this project hasn't built any part of yet, same "documented
gap, not silently reinterpreted" treatment this doc already gives Dolby Vision detection
elsewhere).

### Live validation

Confirmed against Tinyfin after the fix: an authenticated
`GET /Users/{userId}/Items/{episodeId}` for the same episode used throughout this
comparison now returns `Width`/`Height` matching its primary video stream's own resolution,
`IsHD: true`, `VideoType: "VideoFile"`, `PlayAccess: "Full"`, `CanDelete: true`,
`CanDownload: true` for the admin test account.

---

## Dashboard overview — the `GET /socket` realtime channel

### The gap this resolves

The Dashboard overview pass (see `docs/11`'s "Dashboard" section) left one call
unresolved: jellyfin-web opens a WebSocket to `GET /socket` on every page and keeps it open for
the life of the session. Tinyfin doesn't implement it at all today (connection refused/404 — no
route registered). Black-box capture (Playwright's raw frame listener against a real reference
server) recovered the wire shapes and the client-visible timing, but
left one real question open: two of the messages the client explicitly subscribes to
(`SessionsStart`, `ScheduledTasksInfoStart`) never arrived during a 75-second idle window even
though nothing else about the subscription looked wrong — was this a push-on-change design, a
timing artifact of the capture, or something the client has to re-request periodically? Read the
reference source (on the maintainer's explicit request) to resolve that specifically, rather than
guessing from a longer black-box capture window.

### Wire shapes and timing (black-box, the reference server, no C# read needed for this part)

- **Connection**: `ws://<host>/socket?api_key=<token>&deviceId=<deviceId>` — plain query-string
  auth, same lowercase `api_key` fallback Tinyfin's own `auth.go` already recognizes for the
  stream-URL case. No `Authorization`/`X-Emby-Authorization` header is sent on the WebSocket
  upgrade request itself in this client's implementation, only the query string.
  (Superseded by D14a: `api_key` and `X-Emby-*` are no longer honored; the 12+ client's `ApiKey`
  spelling is the supported one.)
- **On connect**, before anything else, the server immediately sends one frame:
  `{"MessageId":"<uuid>","Data":60,"MessageType":"ForceKeepAlive"}`. `Data` is a keep-alive
  timeout in seconds.
- The client replies `{"MessageType":"KeepAlive"}` (bare, no `MessageId`, no `Data`) roughly every
  30 seconds — this is the *client's* own heartbeat cadence (jellyfin-web), not something the wire
  contract fixes; a different client is free to pick a different interval as long as it stays
  under the server's timeout. The server acks each one with
  `{"MessageId":"<uuid>","MessageType":"KeepAlive"}` (also bare `Data`).
- Right after connecting, the client sends two subscription requests:
  `{"MessageType":"SessionsStart","Data":"0,1500"}` and
  `{"MessageType":"ScheduledTasksInfoStart","Data":"1000,1000"}`. `Data` is
  `"<initialDelayMs>,<intervalMs>"`.
- Triggering a real state change while connected (`POST /Library/Refresh`) immediately produced,
  unprompted, both a `ScheduledTasksInfo` push (full scheduled-task list, matching the shape
  `GET /ScheduledTasks` already returns) and a handful of `RefreshProgress` pushes
  (`{"MessageId":"<uuid>","Data":{"ItemId":"<id>","Progress":"<0-100 string>"},"MessageType":"RefreshProgress"}`)
  — one burst per item being refreshed, culminating in another full `ScheduledTasksInfo` push once
  the scan task itself finished. No `Sessions` push was observed in this same window (nothing about
  this test changed the session list), consistent with the source-derived rule below.
- `RefreshProgress` was never explicitly subscribed to (no corresponding `...Start` message was
  ever sent) — it just started arriving the moment the triggering action began, to whatever socket
  happened to be connected.

### Behavior, in prose (source: reference server's `BasePeriodicWebSocketListener` plus its three
### concrete listeners for Sessions/ScheduledTasksInfo/ActivityLogEntry, and the always-on
### keep-alive watchdog and connection-registration logic)

- **There is no ticking clock driving `Sessions`/`ScheduledTasksInfo`/`ActivityLogEntry` at all.**
  Each of the three is push-on-event, full stop — a `...Start` subscription registers interest and
  an initial "how often, at minimum" spacing, but nothing about the subscription itself schedules
  a future send. A send only ever happens because something else in the server called "notify
  subscribers of this channel," triggered by a real state transition:
  - **Sessions**: a session starting or ending, playback starting/stopping/reporting progress, a
    client's capabilities changing, or general session-activity heartbeats (any authenticated
    request from a session counts as activity for this purpose, not just playback-related ones).
  - **ScheduledTasksInfo**: a task starting execution, a task completing, or a running task's own
    progress percentage updating.
  - **ActivityLogEntry**: a new activity-log row being recorded (login, login failure, etc. — the
    same events Tinyfin's own `activity_log.go`/`ActivityLogStore` already records for
    `GET /System/ActivityLog/Entries`).
- **The registered interval is a per-connection minimum-spacing throttle, not a schedule.** Some
  triggers are "forced" (sent to every subscribed connection immediately, ignoring the interval
  entirely) — session start/end, capabilities change, playback start/stop, task
  start/complete, any new activity-log entry. Others are throttled to at most once per the
  connection's own registered interval — session activity heartbeats, and *automated* playback
  progress reports (an explicit/manual progress report from the client is still forced; only the
  client's own periodic automatic ones are throttled) and task-progress percentage updates. A
  connection that registered `"0,1500"` for Sessions, for example, gets forced sends immediately
  on any of the forced triggers, but a mere activity heartbeat for that same connection is
  collapsed to at most one send per 1500ms even if several heartbeats land within that window.
  This is exactly why a 75-second idle capture with nothing actually happening produced zero
  `Sessions`/`ScheduledTasksInfo` frames: idle means no trigger ever fired, forced or throttled.
- **A subscription lasts until an explicit stop message or the connection closing** — there is no
  need to re-send `SessionsStart`/`ScheduledTasksInfoStart` periodically to keep receiving updates;
  it is a one-time "start watching" registration per connection, undone only by
  `SessionsStop`/`ScheduledTasksInfoStop` (mirroring each `...Start`) or the socket disconnecting.
- **Session-list visibility is per-connection, not admin-gated.** Any authenticated connection
  (including a non-admin user) may subscribe to `SessionsStart` — but a non-admin/non-API-key
  connection's own copy of the `Sessions` payload is filtered server-side down to only the
  sessions that connection's own user owns (or is an "additional user" on), never the full list.
  Only `ActivityLogEntryStart` is actually rejected outright for a non-admin/non-API-key caller
  (an authentication error, closing that specific subscription attempt). This is a **materially
  different exposure model than Tinyfin's existing `GET /Sessions` REST route**, which this
  project deliberately made admin-only-or-nothing (see `sessions.go`'s doc comment) rather than
  building the reference server's own per-connection filtering — a real, intentional simplification
  worth keeping in mind if/when `SessionsStart` over the socket is ever implemented: it should
  filter per-connection like the reference server does, not reuse the REST route's coarser
  admin-only gate, since a non-admin client legitimately depends on seeing its own session (e.g.
  to know it's still alive) over this channel in the real app.
- **`RefreshProgress` is not one of the three periodic-listener channels above** — it has no
  `...Start`/`...Stop` message pair at all in the wire vocabulary; it is a direct, unconditional
  broadcast tied to the underlying refresh operation itself; every currently-connected,
  authenticated socket receives it while a refresh is in flight, with no per-connection
  subscription state to track for it.
- **Keep-alive is a server-driven watchdog independent of the three data channels above**: on
  every new connection the server immediately sends one `ForceKeepAlive` (with the timeout value,
  60 seconds observed) and starts watching that connection's "last activity" timestamp (updated
  whenever *any* inbound frame arrives, not just `KeepAlive` ones — this is real, not incidental,
  since the reference server's own bookkeeping records last-activity on every processed message,
  including the `Sessions`/`ScheduledTasksInfo` subscription requests themselves). On a recurring
  check (roughly every 12 seconds against the 60-second timeout observed), any connection that has
  gone quiet past 75% of the timeout gets another `ForceKeepAlive` nudge; any connection that's
  gone completely silent past the full timeout is treated as lost and the connection is torn down
  server-side — this is what actually ends a session whose client vanished without a clean close
  frame (killed process, network drop), not a client-side timeout.
- **A bare inbound `KeepAlive` message is special-cased** ahead of the normal per-listener message
  dispatch: it never reaches the Sessions/ScheduledTasksInfo/ActivityLogEntry listeners at all,
  it's answered directly with an outbound `KeepAlive` acknowledgement (also refreshes that
  connection's last-activity/last-keep-alive timestamp).
- **Auth reuses the same mechanism as every other route** — the WebSocket upgrade request is
  authenticated through the identical token-resolution path as an ordinary HTTP request (the same
  header/`X-Emby-Token`/`api_key`-query-param precedence Tinyfin's `auth.go` already implements),
  not a separate WebSocket-specific auth scheme; an unauthenticated upgrade attempt is rejected
  before the socket is even accepted.
- **The connection is tied to a session, not created standalone.** Accepting the socket resolves
  (or creates, mirroring the same device-identification flow `POST /Sessions/Capabilities/Full`
  already drives in Tinyfin's `sessionRegistry`) a session for the connecting client, and attaches
  a remote-control capability to that session that lets a *different* client send it commands
  (`POST /Sessions/{id}/Command`, `/Message`, etc.) — those command-delivery routes stay explicitly
  out of scope here (already `❌` in `docs/11`, unrelated to the Dashboard gap this section closes)
  since they're a separate, larger remote-control feature, not part of what the Dashboard overview
  page itself needs.

### Scope note for the implementer

This section is self-contained enough to implement the three periodic channels
(`Sessions`/`ScheduledTasksInfo`/`ActivityLogEntry`) plus `RefreshProgress` and the keep-alive
watchdog from the prose above, reusing Tinyfin's existing data sources for each
(`sessionRegistry` for Sessions, `scanState`/the scheduled-tasks handler's own task-list assembly
for ScheduledTasksInfo, `ActivityLogStore` for ActivityLogEntry) plus black-box verification
against a running reference server for exact framing/timing — without reopening the reference
source. `GeneralCommand`/`Message`/remote-control delivery is a distinct follow-up, not covered by
this section. This section is a survey/spec only — no Go code was written from it in this pass;
implementation is a separate task the way every other section in this document treats it.

### Known chronic issues in the reference implementation — deliberately not reproduced

> These are architecture differences, not wire behavior; they are not listed in
> [`13-divergences.md`](13-divergences.md), which covers what a client can observe.


Same treatment as the "Candidate-series ranking must not be biased by episode count" note in this
document's `Items/Latest` section above: a real architectural flaw found while extracting this
spec, worth designing around from the start rather than discovering by hitting it later. This one
is corroborated by the reference project's own public issue tracker (`jellyfin/jellyfin`, referenced
here only as evidence of a real, multi-year, still-recurring bug class — the *idea* "don't do this"
is what's being recorded, nothing about how the reference server's code is organized), not just a
single reading of the source:

**1. No single owner serializes writes/lifecycle for a given connection.** The keep-alive watchdog
(a timer callback), each of the three periodic listeners, and at least one other broadcaster
(`UserDataChangeNotifier`, seen in a crash stack trace, not otherwise read for this spec) all hold
their own independent reference to the same connection and each may call its send method or
dispose it, from different goroutine/thread-pool contexts, with no shared lock actually
serializing calls into the underlying socket. `System.Net.WebSockets.WebSocket.SendAsync` only
permits one in-flight send at a time; concurrent callers racing against a disposed/half-closed
socket produces exactly the crash class this project's tracker shows recurring across years,
multiple distinct code paths, and at least one PR that claimed to fix it and didn't fully
(`jellyfin/jellyfin#5705`, `#10614`, `#13457`/`#13459` (the attempted fix) reopened by `#14974`,
`#16511`) — server-crashing `ObjectDisposedException`/`OperationCanceledException` thrown from deep
inside the socket's own frame-writing code, and, separately, stale references that never get
cleaned up (`#5705`'s "de-referenced properly," `#10614`'s unbounded memory growth from the same
root cause).

**How Tinyfin avoids this by construction:** give every accepted connection exactly one owning
goroutine that performs every write to it (reads too, naturally, since a single goroutine already
owns the receive loop). Every other component that wants to push data to that connection — the
keep-alive nudge, each of the three periodic channels, `RefreshProgress` — sends a message value
into that connection's own outbound channel; only the owning goroutine ever calls the actual
websocket write. The same owning goroutine is the only thing that ever closes the underlying
connection, exactly once. This isn't extra ceremony for its own sake — it's the direct structural
fix for a bug class the reference project has been unable to fully close for years by patching
individual call sites one at a time.

**2. Connection cleanup is each subsystem's own independent responsibility, not one canonical
signal.** Because the reference architecture has N independent listeners each keeping their own
`_activeConnections`-shaped list, "did this connection die" has to be independently detected and
acted on N times — once per listener — rather than once. This is consistent with why a fix in one
call path (`#13459`) left the same failure recurring in a different one (`#14974`, a different
broadcaster than the one `#13459` patched) and with `BasePeriodicWebSocketListener`'s own shipped
code carrying an unresolved doubt about whether its dispose path even works right (an admitted "not
sure what this actually accomplishes" left directly in that method, not something inferred).

**How Tinyfin avoids this:** one canonical "this connection is done" signal per connection (a
`context.Context` cancellation or a closed `done` channel), fired exactly once from the owning
goroutine's receive-loop exit or the keep-alive watchdog's lost-timeout — whichever happens first.
Every subsystem that keeps a per-connection bookkeeping entry (the session registry, each
subscribed channel's own "who's listening" set) tears its own entry down by watching that one
signal, not by independently guessing from its own send failures whether the connection is still
alive.

**3. Silently-dropped sockets never ending their session** (`jellyfin/jellyfin#17078`/`#17079`,
filed and fixed very recently against the version of the source read for this spec): a socket that
goes quiet without a close frame (killed app, network drop, no clean TCP shutdown) used to be
merely removed from the keep-alive watchdog's own watchlist when it timed out, without that timeout
ever propagating to "end the session," leaving a zombie session (and, for SyncPlay specifically, a
group stuck forever waiting on a dead participant that could never be removed). The version of
`SessionWebSocketListener.KeepAliveSockets` read for this spec already carries the fix (disposing a
lost socket to force its receive loop to exit and raise the normal closed-connection path) — this
section's "Behavior, in prose" above already describes the *fixed* behavior, not the historical
bug. Recorded here only so a future re-read of an older reference version, or a differently-sourced
description of this feature, isn't mistaken for the current contract: **a connection declared lost
by the keep-alive timeout must end its session**, not just stop being watched.

**4. Oversized periodic payloads** (`jellyfin/jellyfin#16072`/`#16074`): an earlier version of the
`Sessions` push embedded a session's full playback queue (`NowPlayingQueueFullItems`) in every
single push, producing multi-megabyte frames on every playback-progress heartbeat for a client with
a large queue loaded. Tinyfin's own `sessionInfoDto` (`sessions.go`) already keeps
`NowPlayingQueue`/`NowPlayingQueueFullItems` as structural empty placeholders (docs/11's Dashboard
section) — recorded here so that stays a deliberate design choice going forward, not something a
future "let's populate this properly" pass reintroduces without remembering why the reference
project itself had to walk it back.

**5. Duplicate/reentrant session-lifecycle firing** (`jellyfin/jellyfin#16457`): a client that
fired `SessionStarted` twice for what was really one connection lifecycle crashed the reference
server via a lock-state mismatch on the corresponding double `SessionEnded`. The specific crash
mechanism is a C#-locking-primitive detail, not something a Go mutex can even reproduce the same
way, but the root trigger — a device/client that (for whatever reason) causes the "session started"
path to run twice for one logical connection — is real and worth defending against directly:
Tinyfin's session-registry upsert (`sessionRegistry.upsertCapabilities`/`touch`) should stay
idempotent by device/session key the way it already is (`internal/api/sessions.go`), so a duplicate
start event merges into the existing entry rather than creating a second one or assuming
exactly-once delivery.

**6. WebSocket-specific remote-IP resolution silently skipping proxy headers**
(`jellyfin/jellyfin#15645`): the WebSocket upgrade handler was written as its own code path
separate from the rest of the HTTP pipeline and initially used the raw connection's remote address
directly, missing the `X-Forwarded-For`-aware resolution the ordinary HTTP handlers already had —
so every session opened over a reverse-proxied WebSocket recorded the proxy's own IP instead of the
real client's. Tinyfin must reuse whatever single remote-IP-resolution helper the rest of
`internal/api` already uses for REST routes (`sessions.go`'s `remoteEndPoint(r)`, called from the
same `*http.Request` the WebSocket upgrade also arrives as) for the `/socket` handler too, rather
than writing a second, narrower one for just this route.

**7. API-key authentication under-tested on the WebSocket path**
(`jellyfin/jellyfin#13499`/`#13795`/`#13479`, three separate issues/PRs against the same gap): the
WebSocket auth path was originally only exercised with an ordinary user session token and didn't
correctly recognize a bare API key, needing three follow-up fixes. Tinyfin's `auth.go` already
resolves a bare API key the same way as a user token (`LookupToken` doesn't distinguish the two),
so this should already work for `/socket` by construction — but this history is a concrete signal
to add an explicit API-key-authenticated WebSocket test case when implementing, rather than relying
on "the REST routes handle this fine, so the socket surely does too."

### Implementation findings

Implemented in `internal/api/websocket.go`, wired into `server.go` (`GET /socket`, `requireAuth`,
a `*wsHub` field, a background `runWSWatchdog` goroutine started in `NewServer`) and into every
existing mutation point that now has something to notify (`sessions.go`'s
`handleSessionsCapabilitiesFull`, `sessions_playback.go`'s `touchSession` — extended with a `force`
parameter — `server.go`'s `runScan`, `auth_by_name.go`'s `logAuthSuccess`/`logAuthFailure`).

**Library choice** — D13 (`docs/00-decision-log.md`): `github.com/coder/websocket` v1.8.15, chosen
specifically because its documented concurrency contract ("every `*Conn` method except
`Reader`/`Read` may be called concurrently") already satisfies chronic issue #1's underlying
requirement without a dedicated single-writer-goroutine workaround — only chronic issue #2
(canonical, single-sourced connection cleanup) needed a deliberate design choice on Tinyfin's side,
via each connection's own mutex-guarded state plus the hub's single watchdog declaring a connection
lost and closing it, which is what actually unblocks that connection's own `conn.Read` and runs its
one cleanup path (`handleSocket`'s deferred `hub.remove`).

**Chronic issue #6 (remote-IP resolution) turned out not to apply**: Tinyfin's WebSocket connection
never independently records a remote IP at all — `handleSocket` only calls
`sessionRegistry.touch(deviceID)` (liveness only), never `upsertCapabilities` (the only method that
stores `remoteEndPoint`, sourced from the REST `POST /Sessions/Capabilities/Full` call every client
already makes over ordinary HTTP before opening the socket). So there was no second, narrower IP
resolution path to accidentally get wrong — the gap the reference project actually hit doesn't have
a Tinyfin equivalent to reproduce.

**RefreshProgress was deliberately not implemented** — flagged as a documented gap, not a silent
omission (package doc comment, `websocket.go`): `internal/scan.Scanner` has no per-item progress
callback today, and adding one purely to feed this one wire message would be scope creep beyond
what black-box observation of the Dashboard page actually required.

**ActivityLogEntry payload deliberately diverges from the reference server** (its own listener
sends an empty array on every push, per the "Behavior, in prose" section above) — Tinyfin's
`notifyActivityLog` sends the real `wsActivityLogPushLimit` (10) most recent entries instead, since
no black-box observation ever depended on an empty payload and the real data is strictly more
useful.

**Live verification** against Tinyfin (rebuilt, restarted) with a real jellyfin-web build
via Playwright, a fresh browser tab, capturing raw WebSocket frames:

- Connection URL observed as `ws://localhost:<port>/socket?ApiKey=<token>` — this particular
  jellyfin-web build used the capital-`A` `ApiKey` query param (distinct from the lowercase
  `api_key` observed earlier against the reference server), both already handled by `auth.go`'s
  existing query-param fallback precedence; no code change needed, confirmed by the connection
  succeeding.
- `ForceKeepAlive` (`Data: 60`) received immediately on connect, matching the captured reference
  behavior exactly.
- Client's `SessionsStart`/`ScheduledTasksInfoStart`/`KeepAlive` sent and acked correctly.
- Triggering a real `POST /Library/Refresh` (three configured libraries) produced a live burst of
  `ScheduledTasksInfo` pushes — three `"State":"Running"` pushes (one per library's own
  `scanState.begin()`) followed by `"State":"Running"` with a populated `LastExecutionResult`
  (`"Status":"Completed"`) as each library's scan finished within the capture window — exactly the
  push-on-event pattern this section documents, not a fixed interval.
- No server-side error, panic, or crash in Tinyfin's logs throughout, and the process
  stayed healthy/running afterward — the specific outcome chronic issues #1/#2 exist to prevent.

Unit/integration tests: `internal/api/websocket_test.go`, dialing real WebSocket connections
against an `httptest.Server` via the same `coder/websocket` client library (`TestSocketRequiresAuth`,
`TestSocketKeepAliveHandshake`, `TestSocketSessionsPushOnCapabilitiesChange`,
`TestSocketSessionsFilteredForNonAdmin` — the per-connection-filtering rule from "Behavior, in
prose" above, `TestSocketScheduledTasksInfoPushOnScanStateChange`,
`TestSocketActivityLogEntryAdminOnly`, `TestParseWSInterval`).

One real, pre-existing bug found and fixed as a direct consequence of writing an actual end-to-end
test rather than a handler-level unit test: `server.go`'s `withAccessLog` middleware wraps every
response in a `statusRecorder` that only forwards `http.ResponseWriter`'s three interface methods —
`websocket.Accept` (via `net/http`'s `http.ResponseController`) needs to reach the underlying
concrete writer's `Hijack` support through that wrapper and couldn't, failing every upgrade with a
501 before any of the behavior above could be observed at all. Fixed by adding
`func (r *statusRecorder) Unwrap() http.ResponseWriter`, the standard-library-documented mechanism
for exactly this situation (`net/http`'s `ResponseController` deliberately looks for an `Unwrap`
method on a wrapping type). Not something black-box observation against the reference server could
ever have surfaced, since the reference server has no equivalent wrapping middleware to get this
wrong — found only because this project tested its own middleware stack end-to-end instead of
calling the handler directly.
