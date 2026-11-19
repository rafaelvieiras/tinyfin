package api

import (
	"net/http"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file holds the per-account preferences the preferences page
// (jellyfin-web's homeScreenSettings, "#/mypreferenceshome") saves with
// POST /Users/{userId}/Configuration and the places where those preferences
// show up again: the order and membership of the library views, and the
// "Recently added" row. Contract: items.views.R6, items.views.R7,
// items.latest.R5, items.latest.R6, users.configuration.R8.

// includeHiddenQuery reads the includeHidden query parameter of GET
// /UserViews and GET /Users/{userId}/Views (items.views.R7): absent is
// false, "true" and "false" bind in any capitalization, and any other value
// is a 400 problem document — the reference refuses "1", "yes" and an empty
// value rather than reading them as truthy. The bool return is "carry on".
func includeHiddenQuery(w http.ResponseWriter, r *http.Request) (bool, bool) {
	raw := strings.TrimSpace(caseInsensitiveQuery(r).Get("includehidden"))
	switch {
	case raw == "":
		return false, true
	case strings.EqualFold(raw, "true"):
		return true, true
	case strings.EqualFold(raw, "false"):
		return false, true
	}
	writeValidationErrors(w, map[string][]string{
		"includeHidden": {"The value '" + raw + "' is not valid."},
	})
	return false, false
}

// dropExcludedViews removes the views named by the account's MyMediaExcludes
// (the "Include in My Media" checkboxes) from a library view list. The
// identifiers are stored as they came off the wire, so both spellings of a
// GUID have to match.
func dropExcludedViews(items []baseItemDto, excludes []string) []baseItemDto {
	if len(excludes) == 0 {
		return items
	}
	drop := make(map[string]bool, len(excludes))
	for _, id := range excludes {
		if g, ok := normalizeGUID(id); ok {
			drop[g] = true
		}
	}
	out := items[:0:0]
	for _, it := range items {
		if g, ok := normalizeGUID(it.ID); ok && drop[g] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// orderViews applies OrderedViews to a library view list (items.views.R6):
// the views it names come first, in its order, and every view it does not
// name keeps the default order behind them. An identifier that names no view
// is ignored, and so is a repeat, so a list the client saved against a
// server that has since lost a library still orders what is left.
func orderViews(items []baseItemDto, ordered []string) []baseItemDto {
	if len(ordered) == 0 {
		return items
	}
	rank := make(map[string]int, len(ordered))
	for i, id := range ordered {
		if g, ok := normalizeGUID(id); ok {
			if _, seen := rank[g]; !seen {
				rank[g] = i
			}
		}
	}
	if len(rank) == 0 {
		return items
	}
	named := make([]baseItemDto, 0, len(rank))
	rest := make([]baseItemDto, 0, len(items))
	for _, it := range items {
		if g, ok := normalizeGUID(it.ID); ok {
			if _, ok := rank[g]; ok {
				named = append(named, it)
				continue
			}
		}
		rest = append(rest, it)
	}
	// A stable sort by rank keeps the saved order even when the same rank
	// was recorded twice (orderViews only records the first).
	for i := 1; i < len(named); i++ {
		for j := i; j > 0; j-- {
			gi, _ := normalizeGUID(named[j-1].ID)
			gj, _ := normalizeGUID(named[j].ID)
			if rank[gi] <= rank[gj] {
				break
			}
			named[j-1], named[j] = named[j], named[j-1]
		}
	}
	return append(named, rest...)
}

// dropExcludedLibraryItems removes the items of the libraries named by the
// account's LatestItemsExcludes from an unscoped "recently added" list
// (items.latest.R6). Media that lives inside a library is dropped by the
// library id the store recorded on the item.
func dropExcludedLibraryItems(items []storage.Item, excludes []string) []storage.Item {
	if len(excludes) == 0 {
		return items
	}
	drop := make(map[string]bool, len(excludes))
	for _, id := range excludes {
		if g, ok := normalizeGUID(id); ok {
			drop[g] = true
		}
	}
	out := items[:0:0]
	for _, it := range items {
		if drop[strings.ToLower(string(it.LibraryID))] {
			continue
		}
		out = append(out, it)
	}
	return out
}
