// nextup.go implements GET /Shows/NextUp — the home screen's "next episode"
// row for series with in-progress watching, and the series detail page's
// equivalent widget (seriesId param). Found 404ing, not just unpolished, by
// testing a real jellyfin-web build via Playwright: the bundle calls this
// unconditionally on every home-screen load.
//
// There is no stored "next up" concept anywhere in storage (docs/05) — this
// is entirely a derived, query-time computation over ItemStore (episode
// order) and PlaystateStore (watched/resume state), the same "derive, don't
// store" approach dto.go's isFolderType/mediaTypeFor already take for other
// query-time aggregates.
//
// The exact rule below was not guessed from the field names in the request
// jellyfin-web sends — it was reverse-engineered by black-box testing
// against a real Jellyfin server (staging, :8097, AGENTS.md's preferred
// verification method): marking episodes played/resumed in various orders
// and diffing the NextUp response each time. See handleShowsNextUp and
// nextUpCandidateForSeries below for what each test established.
package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// nextUpEpisodeScanLimit bounds how many of one series' episodes
// nextUpCandidateForSeries reads to find the last-touched one. Deliberately
// larger than defaultItemsLimit (100): a long-running series (recorded
// during testing here — the staging catalog's own "Chapolin Colorado" has
// 103 episodes across 3 seasons) can exceed that page size, and truncating
// here would silently miss touched episodes past the cut, corrupting the
// "last touched, by order" computation this whole handler depends on. Still
// a bounded query, not the unbounded "ListAll" ItemStore's doc comment
// forbids (AGENTS.md principle 1): one series' episode count is
// content-scale (bounded by how many episodes a show ever airs), not
// catalog-scale, the same reasoning QueryLibraries' unbounded call in
// library.go already relies on for library count.
const nextUpEpisodeScanLimit = 5000

// nextUpSeriesScanLimit bounds the "every series" query used when no
// seriesId is given. Series count, like library count, is operator/catalog
// scale rather than something that grows unbounded per request — this just
// needs to be larger than any real deployment's series count, not
// literally unbounded.
const nextUpSeriesScanLimit = 5000

// defaultNextUpLimit is GET /Shows/NextUp's own page-size default when the
// client doesn't specify limit — jellyfin-web's own call always sends
// limit=24 (this file's package doc comment example), so this default is
// never actually exercised by that call site; kept for parity with every
// other paginated handler in this package (defaultItemsLimit,
// defaultLatestLimit) rather than left unbounded.
const defaultNextUpLimit = defaultItemsLimit

// nextUpCandidate is one series' current "next up" episode plus the
// timestamp that decides both its place in nextUpDateCutoff filtering and
// the response's ordering.
type nextUpCandidate struct {
	item storage.Item
	// lastActivity is the touched episode's own PlaystateStore.LastPlayedDate
	// — the *previous* episode's, when the candidate is "the episode after
	// the last one finished" (the advance-past-last-played case below); the
	// candidate's own, when the candidate is itself the in-progress episode
	// (the resume case below). Confirmed against the real server: an
	// advanced-to (never-touched) candidate's own UserData carries no
	// LastPlayedDate at all, so ordering/cutoff can only ever come from the
	// touched episode that produced the candidate, never the candidate
	// itself in that branch.
	lastActivity time.Time
}

// handleShowsNextUp serves GET /Shows/NextUp. userId is accepted but
// ignored — confirmed against the real server, not assumed, same
// simplification already applied elsewhere in this package (e.g.
// handleUserItemsResume's doc comment): a request carrying a syntactically
// invalid userId (too long to be a real GUID) came back with the same
// three in-progress series' NextUp entries as a request with the param
// left off entirely, both scoped to the token's own user — the reference
// server evidently never even attempts to resolve the param against a
// different account here.
func (s *Server) handleShowsNextUp(w http.ResponseWriter, r *http.Request) {
	ac, ok := AuthFromContext(r.Context())
	if !ok || ac == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	userID := ac.User.ID

	q := r.URL.Query()

	// enableResumable defaults true (absent or anything but exactly
	// "false") — confirmed against the real server: an in-progress
	// (resumed, not fully watched) episode appears as its own NextUp entry
	// by default, and is dropped from the response entirely (not replaced
	// by some other candidate) when this is "false". jellyfin-web's own
	// home-screen call (this file's package doc comment) always sends
	// "false" itself — Continue Watching (GET /UserItems/Resume,
	// library.go) is where it wants in-progress items, not this row.
	enableResumable := !strings.EqualFold(q.Get("enableResumable"), "false")

	// enableRewatching defaults false. Confirmed only partially against the
	// real server: with every episode of a series marked played and this
	// set to "true", the series still did not reappear pointing back at
	// episode 1 — contrary to the behavior this flag's name implies.
	// Several repeats of that test (different casing, with/without
	// nextUpDateCutoff) all came back the same empty result, so this isn't
	// a one-off fluke, but nothing in a black-box test can rule out some
	// other precondition the reference server requires that this staging
	// catalog's data never happened to satisfy. Decision (not a guess left
	// unflagged): implement the documented/standard interpretation anyway —
	// enabled and the series has no next episode, restart at episode 1 —
	// since it is a strict superset of confirmed default behavior (off
	// changes nothing observable) and doing nothing here would leave the
	// flag a silent no-op forever. If this turns out wrong once a
	// reproducible real-server case is found, it's a one-line fix isolated
	// to nextUpCandidateForSeries below.
	enableRewatching := strings.EqualFold(q.Get("enableRewatching"), "true")

	var cutoff time.Time
	hasCutoff := false
	if v := q.Get("nextUpDateCutoff"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			cutoff = t
			hasCutoff = true
		} else if t, err := time.Parse("2006-01-02", v); err == nil {
			// jellyfin-web's own call (package doc comment) sends a bare
			// date, not a full timestamp ("2025-09-02", not RFC3339) —
			// accept that shape too rather than silently dropping the
			// filter for the one caller this was built for.
			cutoff = t
			hasCutoff = true
		}
		// An unparseable value is treated as "no cutoff" rather than a 400
		// — consistent with how every other query param in this package
		// (limit, startIndex, ...) degrades to a default instead of
		// erroring on a malformed value.
	}

	// seriesId scopes the whole computation to one series (the series
	// detail page's call, package doc comment) instead of every series with
	// progress (the home screen's call). Confirmed against the real server:
	// this filter only takes effect when the id actually resolves to an
	// existing item — a syntactically well-formed but non-existent id (32
	// hex characters, matching no item) came back with every series'
	// NextUp entry, identical to the param being left off entirely, not an
	// empty result. Replicated here rather than "fixed" to only filter
	// when valid: AGENTS.md principle 4, wire compatibility is the
	// product, and a client that only ever sends ids it got from this same
	// server (the real, intended use) never observes the difference
	// anyway.
	var seriesIDs []storage.ItemID
	if v := q.Get("seriesId"); v != "" {
		if _, err := s.store.GetItem(r.Context(), storage.ItemID(v)); err == nil {
			seriesIDs = []storage.ItemID{storage.ItemID(v)}
		}
	}
	if seriesIDs == nil {
		page, err := s.store.QueryItems(r.Context(), storage.ItemFilter{
			ItemTypes: []storage.ItemType{storage.ItemTypeSeries},
			Recursive: true,
			Limit:     nextUpSeriesScanLimit,
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		seriesIDs = make([]storage.ItemID, 0, len(page.Items))
		for _, it := range page.Items {
			seriesIDs = append(seriesIDs, it.ID)
		}
	}

	candidates := make([]nextUpCandidate, 0, len(seriesIDs))
	for _, sid := range seriesIDs {
		c, err := s.nextUpCandidateForSeries(r.Context(), userID, sid, enableResumable, enableRewatching)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if c == nil {
			continue
		}
		if hasCutoff && c.lastActivity.Before(cutoff) {
			continue
		}
		candidates = append(candidates, *c)
	}

	// Most-recently-active series first — confirmed against the real
	// server: three series with progress, touched in a known order, came
	// back in the reverse of that order (most recent touch first), not
	// e.g. alphabetically or by series id.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].lastActivity.After(candidates[j].lastActivity)
	})

	total := len(candidates)

	offset := 0
	if v := q.Get("startIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	limit := defaultNextUpLimit
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := candidates[start:end]

	items := make([]baseItemDto, 0, len(page))
	for _, c := range page {
		items = append(items, s.baseItemDto(r.Context(), c.item))
	}

	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{
		Items:            items,
		TotalRecordCount: nextUpTotalRecordCount(q.Get("enableTotalRecordCount"), total),
		StartIndex:       offset,
	})
}

// nextUpTotalRecordCount decides GET /Shows/NextUp's TotalRecordCount.
// Deliberately NOT resolveTotalRecordCount (library.go) reused unchanged,
// despite this endpoint accepting the same EnableTotalRecordCount param:
// confirmed against the real server that the two endpoints disagree on
// what "false" means. GET /Items?EnableTotalRecordCount=false reports the
// current page's own item count (resolveTotalRecordCount's doc comment);
// GET /Shows/NextUp?EnableTotalRecordCount=false with limit=1 against three
// real in-progress series came back with TotalRecordCount 0, not 1 — the
// page's own item count — every time this was retested. Reusing the other
// function's rule here would have been a plausible-looking but wrong guess
// this black-box check exists specifically to catch.
func nextUpTotalRecordCount(enableParam string, realTotal int) int {
	if strings.EqualFold(enableParam, "false") {
		return 0
	}
	return realTotal
}

// nextUpCandidateForSeries computes seriesID's current NextUp entry, or nil
// if it has none. The rule (confirmed against the real server by marking
// episodes played/resumed in different orders and diffing the response each
// time, see this file's package doc comment):
//
//  1. Read every episode of the series, ordered the same way
//     handleShowEpisodes already does (ParentIndexNumber then IndexNumber —
//     season then episode).
//  2. Find the last one, by that order (NOT by watch date — confirmed by
//     marking episode 5 of a 20+ episode series played while 1-4 stayed
//     untouched: NextUp advanced straight to episode 6, never surfacing
//     1-4), with PlayCount > 0 ("touched": either fully watched or resumed
//     at least once — PlayCount is 1 on a real server after either a
//     PlayedItems mark-watched call or a single Sessions/Playing/Progress
//     report, confirmed via both).
//  3. No touched episode at all -> no candidate (confirmed: a series with
//     zero playback activity produces no NextUp entry, not "episode 1" as
//     a naive "next unwatched from the start" reading of the endpoint name
//     would suggest).
//  4. The last-touched episode is NOT fully played (still has resume
//     progress) -> the candidate is that same episode, unchanged — the
//     in-progress episode itself, not the one after it (confirmed: setting
//     resume progress on an untouched episode surfaced that exact episode
//     in NextUp, immediately, with no prior fully-watched episode in the
//     series at all). Only surfaced when enableResumable is true; excluded
//     (not replaced by anything else) otherwise (confirmed: identical
//     setup with enableResumable=false came back with that series entirely
//     absent from the response).
//  5. The last-touched episode IS fully played -> the candidate is the
//     next episode after it in order (confirmed by the season-boundary
//     case: finishing every episode of season 1 advanced NextUp straight
//     to season 2 episode 1). If there is no next episode (the series is
//     completely watched through its last episode) -> no candidate, unless
//     enableRewatching selects episode 1 instead (see its doc comment on
//     the caller for what this project confirmed and didn't).
func (s *Server) nextUpCandidateForSeries(ctx context.Context, userID storage.UserID, seriesID storage.ItemID, enableResumable, enableRewatching bool) (*nextUpCandidate, error) {
	page, err := s.store.QueryItems(ctx, storage.ItemFilter{
		ItemTypes: []storage.ItemType{storage.ItemTypeEpisode},
		SeriesID:  &seriesID,
		SortBy:    []storage.SortField{{By: storage.SortByParentIndexNumber}, {By: storage.SortByIndexNumber}},
		Limit:     nextUpEpisodeScanLimit,
	})
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, nil
	}

	lastTouchedIdx := -1
	var lastTouchedState storage.Playstate
	for i, ep := range page.Items {
		st, err := s.store.GetPlaystate(ctx, userID, ep.ID)
		if err != nil {
			// No playstate row yet — this episode was never touched, same
			// zero-value convention userDataFor (dto.go) already uses.
			continue
		}
		if st.PlayCount > 0 {
			lastTouchedIdx = i
			lastTouchedState = st
		}
	}
	if lastTouchedIdx == -1 {
		return nil, nil
	}

	activity := time.Time{}
	if lastTouchedState.LastPlayedDate != nil {
		activity = *lastTouchedState.LastPlayedDate
	}

	if !lastTouchedState.Played {
		if !enableResumable {
			return nil, nil
		}
		return &nextUpCandidate{item: page.Items[lastTouchedIdx], lastActivity: activity}, nil
	}

	nextIdx := lastTouchedIdx + 1
	if nextIdx >= len(page.Items) {
		if !enableRewatching {
			return nil, nil
		}
		// See handleShowsNextUp's enableRewatching doc comment: this branch
		// (restart at episode 1) is a documented decision, not something
		// the real server actually reproduced under test.
		return &nextUpCandidate{item: page.Items[0], lastActivity: activity}, nil
	}
	return &nextUpCandidate{item: page.Items[nextIdx], lastActivity: activity}, nil
}
