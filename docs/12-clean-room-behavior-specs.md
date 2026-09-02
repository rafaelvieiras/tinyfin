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

Two more things surfaced during live comparison that are real, reproducible reference-server
behavior but are **not** implemented by Tinyfin's `internal/api/latest_grouping.go`, logged here
rather than chased further per the scope note above:

- **A series with zero unwatched items among its candidates never appears in the grouped result at
  all**, even when it would otherwise rank into the requested `limit` by recency (confirmed: a
  4-season series whose every episode had `Played: true` was skipped entirely, even at `limit`
  values far larger than the number of series that did appear — a series ranked just behind it by
  recency, with unwatched episodes, took its place instead). This reads as a "recently added,
  unwatched" refinement on top of the ranking this section describes, not a change to the
  window/bulk/card-selection rule itself. Out of scope for the grouping gap this section closes —
  Tinyfin's `tvShowsLatestGrouped` does not filter by playstate — recorded here for whoever picks
  up a future "Latest should skip fully-watched series" gap.
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
