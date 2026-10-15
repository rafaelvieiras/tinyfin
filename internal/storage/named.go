package storage

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
)

// ErrNotFound is wrapped by lookups that find nothing.
var ErrNotFound = errors.New("storage: not found")

// NamedKind is a kind of named, virtual item: a genre, a studio, a person or
// a production year. They are not rows of the items table; they exist while
// at least one item carries the name, and their identifier is derived from
// the kind and the name (NamedID).
type NamedKind string

const (
	NamedGenre  NamedKind = "Genre"
	NamedStudio NamedKind = "Studio"
	NamedPerson NamedKind = "Person"
	NamedYear   NamedKind = "Year"
)

// NamedID is the stable identifier of a named item. Names compare without
// regard to case, so "Action" and "action" are one genre.
func NamedID(kind NamedKind, name string) ItemID {
	return ItemID(guid.Derive("tinyfin-named:" + string(kind) + ":" + strings.ToLower(strings.TrimSpace(name))))
}

// NamedRef identifies a named item.
type NamedRef struct {
	Kind NamedKind
	Name string
	ID   ItemID
}

// NamedEntry is a named item with the number of items that carry it, by
// type. Total is their sum.
type NamedEntry struct {
	NamedRef
	Movies    int
	Series    int
	Episodes  int
	Playlists int
	Others    int
	Total     int
	// Created is the earliest creation date among the carrying items.
	Created time.Time
}

// NamedQuery lists the names of one kind carried by the items of Scope.
type NamedQuery struct {
	Kind NamedKind
	// Scope selects the carrier items: library access, tags, rating limits,
	// a parent, item types, ... (Limit, Offset and SortBy are ignored).
	Scope ItemFilter

	// SearchTerm keeps the names that contain it (SearchKey comparison).
	SearchTerm              string
	NameStartsWith          string
	NameStartsWithOrGreater string
	NameLessThan            string

	// PersonTypes keeps the credits of those types, ExcludePersonTypes
	// drops the credits of those types; AppearsIn keeps the people credited
	// on one item. Only for NamedPerson.
	PersonTypes        []string
	ExcludePersonTypes []string
	AppearsIn          *ItemID

	// ByName orders by name; otherwise by first appearance in the catalog.
	ByName     bool
	Descending bool

	// Limit <= 0 means no limit.
	Limit  int
	Offset int
}

// NamedPage is one page of a NamedQuery; Total counts the names before
// paging.
type NamedPage struct {
	Entries []NamedEntry
	Total   int
}

// SearchQuery drives the search-hints search: ranked matches over the media
// items of Items and over the genre, studio and person names carried by
// them.
type SearchQuery struct {
	// Items selects the media items (and, with its item types cleared, the
	// carriers of the names).
	Items ItemFilter
	// Term is the raw search text.
	Term string
	// Kinds are the named kinds that take part; NoMedia leaves the media
	// items out.
	Kinds   []NamedKind
	NoMedia bool
	// KeepPersonCase orders people by their name as written (an upper-case
	// initial then sorts ahead of the lower-case sort names of everything
	// else of equal rank); otherwise every name folds to lower case.
	KeepPersonCase bool
	// Limit < 0 counts without returning rows.
	Limit  int
	Offset int
}

// SearchHit is one result: a media item or a named item.
type SearchHit struct {
	Item  *Item
	Named *NamedRef
}

// SearchPage is a page of hits; Total counts the matches before paging.
type SearchPage struct {
	Hits  []SearchHit
	Total int
}

// SearchKey normalises text for searching: letters and digits are kept,
// apostrophes and quotation marks are dropped, every other character becomes
// a space, runs of spaces collapse, and the result is lower-cased and
// transliterated (accents removed).
func SearchKey(s string) string {
	runes := make([]rune, 0, len(s))
	pendingSpace := false
	for _, r := range s {
		switch {
		case r == '\'' || r == '’' || r == '‘' || r == '"' || r == '“' || r == '”':
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			if pendingSpace && len(runes) > 0 {
				runes = append(runes, ' ')
			}
			pendingSpace = false
			runes = append(runes, r)
		default:
			pendingSpace = true
		}
	}
	return transliterate(runes)
}

// PlaylistShare is one user a playlist is shared with.
type PlaylistShare struct {
	UserID  UserID
	CanEdit bool
}

// PlaylistAccess says who owns a playlist and who may use it. MediaType is
// the media type chosen at creation ("" when it follows the entries).
type PlaylistAccess struct {
	PlaylistID ItemID
	OwnerID    UserID
	OpenAccess bool
	MediaType  string
	Shares     []PlaylistShare
}

// RelationPlaylist is the ItemRelation kind of a playlist entry.
const RelationPlaylist = "playlist"
