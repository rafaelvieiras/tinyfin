// Package metadata defines the provider interface docs/08 describes: a
// fixed, small contract Tinyfin's scanner (internal/scan) asks for
// enriched metadata about an already-discovered item, not a general-purpose
// plugin-loading system (docs/01's non-goals; docs/00-decision-log.md's
// D3). NoopProvider (noop.go) is the zero-configuration default — a
// freshly scanned item is fully browsable/playable from filename-derived
// data alone (scan's own job, not this package's) even with no provider
// configured. TMDBProvider (tmdb.go) is the direct-provider reference
// implementation D3 calls for.
//
// The interface is deliberately shaped to survive a later move from an
// in-process Go call to an out-of-process plugin (subprocess + RPC, e.g.
// the HashiCorp go-plugin pattern) without a redesign — see the D3
// extension in docs/00-decision-log.md for the reasoning. Concretely, that
// means every method here:
//   - takes and returns only plain, exported-field structs of primitives/
//     slices/maps (Query, Result, Image below) — nothing that only makes
//     sense in-process (io.Reader, storage.Store, channels, closures);
//   - uses context.Context only for its deadline/cancellation, never to
//     carry a value the implementation depends on;
//   - returns errors as either the ErrNotFound sentinel or *ProviderError,
//     never an arbitrary wrapped Go error chain that loses meaning once it
//     can no longer share an address space with the caller;
//   - takes per-call credentials nowhere — a provider is configured once,
//     at construction (NewTMDBProvider's apiKey param), matching how a
//     go-plugin subprocess receives its config at spawn, not per-RPC.
//
// This package deliberately never imports internal/storage or internal/api
// — structurally enforcing that a provider can only ever see what a Query
// hands it and only ever affect the caller through the Result it returns,
// which is exactly what an RPC boundary would enforce anyway.
package metadata

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Provider.Lookup when the provider has no
// match for the query — a normal, expected outcome (an obscure title, a
// misparsed filename), not a failure. Callers (internal/scan) treat it as
// "leave the item's filename-derived fields as-is," not as an error to
// surface.
var ErrNotFound = errors.New("metadata: not found")

// ProviderError is returned for anything else that goes wrong — a
// transport failure, an unexpected response shape, a rate limit. Code is a
// short, stable, machine-checkable label (e.g. "rate_limited",
// "transport", "bad_response") rather than a wrapped error chain, so it
// still means something to a caller on the other side of a future RPC
// boundary that can't type-assert into this package's internals.
type ProviderError struct {
	Code    string
	Message string
}

func (e *ProviderError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// Kind identifies what sort of item Query is asking about. A plain string
// type, not an alias for storage.ItemType — this package must not import
// internal/storage (see the package doc comment) — but the values are the
// same wire strings so a caller can convert with a simple cast.
type Kind string

const (
	KindMovie   Kind = "Movie"
	KindSeries  Kind = "Series"
	KindSeason  Kind = "Season"
	KindEpisode Kind = "Episode"
)

// Query is what internal/scan already knows about an item purely from its
// path and filename (Kodi/Jellyfin naming-convention parsing, done by
// scan itself before ever calling a Provider) — enough for a provider to
// search its own catalog and identify a match, without the provider
// needing any filesystem or database access of its own.
type Query struct {
	Kind Kind

	// Title/Year identify a Movie, or the parent Series of an Episode.
	Title string
	Year  int

	// SeriesTitle/SeasonNumber/EpisodeNumber are set (Title/Year left
	// zero) when Kind is KindEpisode. KindSeason sets SeriesTitle and
	// SeasonNumber the same way, leaving EpisodeNumber zero.
	SeriesTitle   string
	SeasonNumber  int
	EpisodeNumber int

	// ExternalIDs carries provider IDs already known for this item (e.g.
	// a prior scan's Item.ProviderIDs) so a re-scan can look up by ID
	// directly instead of re-searching by title, avoiding a re-match onto
	// a different title-search result. Keyed the same way
	// Item.ProviderIDs is (e.g. "Tmdb").
	ExternalIDs map[string]string
}

// Image is one piece of remote artwork a Lookup result points at — a URL,
// not bytes: internal/scan downloads and caches it to local disk itself
// (storage.ImageStore holds refs to on-disk files, not blobs or URLs — see
// that interface's doc comment), keeping this package's only network
// dependency the metadata lookup itself.
type Image struct {
	Kind string // storage.ImageKind's wire values, e.g. "Primary", "Backdrop"
	URL  string
}

// Person is one cast/crew credit a Lookup result found — mirrors
// storage.Person's shape (Name/Role/Type/TMDBID) but is kept decoupled
// from it, same "plain structs, no internal/storage dependency" rule as
// every other field on this package's Result (this file's own doc
// comment); internal/scan converts a Result's People into
// []storage.Person, the same conversion it already does for Images ->
// []storage.ImageRef.
type Person struct {
	Name   string
	Role   string // character name — Actor credits only, empty otherwise
	Type   string // "Actor", "Director", "Writer", "Producer", ...
	TMDBID int
}

// Result is the enrichment Lookup returns on a match. Every field is
// optional (zero value = "provider didn't have this") — internal/scan
// overlays only the non-zero fields onto its filename-derived Item,
// never blanking out something it already knows.
type Result struct {
	Title            string
	OriginalTitle    string
	Overview         string
	Genres           []string
	Tags             []string
	Studios          []string // network/studio names, same "provider didn't have this" zero-value convention as Genres
	ProductionYear   int
	PremiereDateUnix int64 // 0 = unknown; Unix seconds, UTC (avoids importing time for a plain data-transfer struct, per the package doc comment's "plain structs" constraint)
	EndDate          int64 // 0 = unknown/still running; Unix seconds, UTC, same convention as PremiereDateUnix
	OfficialRating   string
	CommunityRating  float64 // 0 = unknown

	// ExternalIDs merges into Item.ProviderIDs (e.g. {"Tmdb": "603"}) —
	// the join key a later re-scan's Query.ExternalIDs uses.
	ExternalIDs map[string]string

	Images []Image

	// People is cast/crew, when the provider returns it (TMDBProvider's
	// lookupMovie today — see that method's doc comment for the "why
	// movie only, why these Type values" reasoning). nil for every other
	// Kind/provider until a later pass extends coverage.
	People []Person
}

// Provider is the metadata-enrichment contract docs/08 describes. A
// Movie/Series Lookup is expected to search-then-fetch-details internally
// (whatever that takes on the provider's own remote API) and return one
// best match — Provider does not expose a separate "search" step, keeping
// the interface (and so, per the package doc comment, the eventual RPC
// surface) to a single call per item.
type Provider interface {
	Lookup(ctx context.Context, q Query) (Result, error)
}
