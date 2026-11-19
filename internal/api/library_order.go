package api

import (
	"sort"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Library lists are ordered by the library's sort name (contract:
// admin.libraries.R4) — the same folding an item title gets (items.sort.R8):
// case and accents gone, every number zero-padded so it compares as a number.
// That is why a byte comparison of these keys puts "2 Coisas" before
// "10 Coisas", "apple" before "Banana" and "Ábaco" before "apple", which is
// what the reference server does and a plain `ORDER BY name` does not: SQLite
// compares UTF-8 bytes, so the digits would go 10 then 2, every capital would
// come before every small letter and every accented name would land after "z".
//
// A name whose leading character is punctuation the sort-name set leaves alone
// (`_zebra` keeps its underscore) comes out in a different place on each of the
// reference's three lists; Tinyfin follows the sort name everywhere (DIV-32).

// librarySortKey is the key a library name is ordered by.
func librarySortKey(name string) string { return storage.NormalizeSortName(name) }

// sortLibraries orders libraries in place by that key, keeping the incoming
// order for names that fold to the same key: the reference's order for two
// such names is not derivable from the names, so nothing is claimed about it.
func sortLibraries(libs []storage.Library) {
	sort.SliceStable(libs, func(i, j int) bool {
		return librarySortKey(libs[i].Name) < librarySortKey(libs[j].Name)
	})
}

// sortLibraryItems is sortLibraries for already-built DTOs, so a list that
// mixes libraries with the playlists view sorts as one.
func sortLibraryItems(items []baseItemDto) {
	sort.SliceStable(items, func(i, j int) bool {
		return librarySortKey(items[i].Name) < librarySortKey(items[j].Name)
	})
}
