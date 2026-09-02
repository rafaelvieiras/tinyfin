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

### C# source cross-check (Phase 1, done after the fact per Rafael's explicit instruction — this
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
same deterministic-`ImageRef`-ID upsert. Confirmed live: the real staging movie folder used for the
`ImageTags` validation above already had both `logo.png` and `banner.jpg` sitting on disk (left there
by a prior manual test-fixture setup, not something this task added) — before this fix, a rescan
recorded neither; after, `GET /Items/{itemId}/Images/Logo` and `.../Images/Banner` both returned 200
with the real file bytes (`image/png` 78KB and `image/jpeg` 135KB respectively), and both `ImageTags`
keys appeared as documented in the section above.

### C# source cross-check (Phase 1, done after the fact per Rafael's explicit instruction — this
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

### C# source cross-check (Phase 1, done after the fact per Rafael's explicit instruction — this
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
But while re-reading the reference server's source for the three gaps above (Rafael's explicit
instruction, after an earlier pass had substituted a different method without asking first — see
this project's own process notes on why that was wrong), its DTO-assembly code turned out to also
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
`jellyfin-real-staging` for this project's own People feature specifically, since staging has no TMDB
key configured (same limitation noted in the People implementation this section corrects).

---

## Episode/movie detail page — MediaStream.DisplayTitle

### The gap this resolves

The episode detail page revalidation found the video/audio track-selector panel doesn't render at
all on Tinyfin, because its `MediaStreams` entries carry only the raw probe fields (`Codec`, `Type`,
`Index`, `Width`/`Height`, `Channels`, a few booleans) and are missing a formatted `DisplayTitle`
string the client needs to label each track (real example captured live: `"Vídeo: 360p HEVC SDR"` /
`"Áudio: Indefinido Estéreo - AAC - Stereo - Padrão"` — the `"Vídeo:"`/`"Áudio:"` prefix is the
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
1. The stream's language, if set and not a placeholder/unset marker — its localized display name
   (Tinyfin has no localization system for this; the raw language code is an acceptable stand-in).
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
for completeness since it's the same field/mechanism): language (or an "Und"/undefined label when
unset) first, then `"Hearing Impaired"`/`"Default"`/`"Forced"` flags as applicable, then the codec
uppercased, then `"External"` when applicable — same `Title`-substring-merge rule.

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
`DisplayTitle` output byte-for-byte against the reference server (staging, :8097) for a real staging
file (`MIB - Homens de Preto (1997) - S01E01`) surfaced that `mediaStreamsFromProbe` never extracted a
stream's own embedded `tags.title`/`tags.language` into `MediaStreamRef.Title`/`.Language` at all —
both fields already existed on the struct, nothing ever populated them. This file's audio track carries
an embedded title (`"Indefinido Estéreo"`, confirmed via `ffprobe` directly), so DisplayTitle's
"explicit Title" merge-rule branch had never actually fired for any real file before this fix. Now
extracted (`probe.go`'s `ffprobeStream.Tags`); see the byte-for-byte comparison below.

**Live byte-for-byte comparison** (same file, both servers, after a `POST /Library/Refresh` rescan on
Tinyfin to pick up the extended prober):

| Stream | Reference server (`:8097`) | Tinyfin (`:8096`) |
|---|---|---|
| Video | `"360p HEVC SDR"` | `"360p HEVC SDR"` — exact match |
| Audio | `"Indefinido Estéreo - AAC - Stereo - Padrão"` | `"Indefinido Estéreo - AAC - Stereo - Default"` |

The one remaining divergence (`"Padrão"` vs. `"Default"`) is the accepted, documented gap from this
section's own "Behavior, in prose" and "Scope note": the reference server localizes some attribute
labels into the client's locale (staging is pt-BR), Tinyfin has no localization system for this and
emits the English label the spec's own wording uses throughout — not a bug, and not something this task
asked to fix.

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
(`en-US`) applies throughout; a season's real localized name (e.g. `"Temporada 1"` for a `pt-BR`
request) is a possible later enhancement, out of this task's scope.

Not live-validated against real TMDB data: `tinyfin-staging` has no TMDB API key configured (a
known, previously-documented limitation — see this file's People/cast section above for the same
caveat). Covered instead by unit tests with a fake HTTP server (`internal/metadata/tmdb_test.go`)
and a fake `Provider` double (`internal/scan/scan_test.go`). The degraded (no-key) path was checked
at the deploy level, not by re-curling endpoints or triggering a fresh scan — this session had no
admin credentials for `tinyfin-staging` and the classifier declined both a direct write to its live
sqlite file and running `cmd/devseed` against it, so a fresh authenticated `POST /Library/Refresh` +
`GET /Items/{id}` pass wasn't done this time. What was confirmed instead: `tinyfin-staging` rebuilt
with this change and restarted cleanly, its `0003_studios` migration applied automatically (schema
version 2 → 3, confirmed via a read-only `sqlite3` query) with no error/panic in the server logs, and
its pre-existing 338 items (21 Movie, 4 Series, 9 Season, 319 Episode) came through with a
correctly-defaulted, empty `studios` column — the no-enrichment shape this change is supposed to
degrade to.
