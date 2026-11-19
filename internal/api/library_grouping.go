// library_grouping.go serves the two forms of "grouping options": the list of
// library folders a user may group into one view, which the client's
// preferences page renders as the checkboxes under "Library folders"
// ("Agrupar automaticamente o conteudo destas pastas").
//
// Why it exists at all: on the current jellyfin-web the preferences page
// loads this list in a Promise.all beside the user's views, and hides the
// loading spinner only in that promise's success handler. A 404 here (which
// is what Tinyfin answered before this file) therefore never settled: the
// page sat on a spinner forever with nothing rendered, which is the report
// that produced it. Contract: items.grouping.R1-R4.
//
// Both routes answer the same body, an array of SpecialViewOptionDto
// ({Name, Id}); the SDK's UserViewApi calls /UserViews/GroupingOptions and
// the older client code path calls /Users/{userId}/GroupingOptions.
package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// specialViewOptionDto is the reference server's SpecialViewOptionDto: a
// library folder that may be grouped, named as the user's list names it.
type specialViewOptionDto struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

// groupableCollection reports whether a library of this collection type is
// offered as a grouping option (items.grouping.R2): movies, TV shows and the
// libraries that have no collection type at all — a "mixed" library, which is
// also what the reference stores a photos library as. Music, music videos,
// home videos and books are not offered (each measured against the reference
// one library at a time), and neither are the two virtual folders.
func groupableCollection(t storage.CollectionType) bool {
	switch t {
	case "", storage.CollectionTypeMovies, storage.CollectionTypeTVShows, storage.CollectionTypeMixed:
		return true
	}
	return false
}

// handleUserViewsGroupingOptions serves GET /UserViews/GroupingOptions, the
// form the current SDK uses (items.grouping.R1).
func (s *Server) handleUserViewsGroupingOptions(w http.ResponseWriter, r *http.Request) {
	ac, _ := AuthFromContext(r.Context())
	target := ac.User
	// The parameter is "userId"; the harness lower-cases the keys of every
	// query string it reads through caseInsensitiveQuery, so both spellings
	// reach this point.
	if raw := caseInsensitiveQuery(r).Get("userid"); raw != "" && !strings.EqualFold(raw, string(ac.User.ID)) {
		id, ok := guid.Normalize(raw)
		if !ok {
			// A value that is not an identifier at all is a validation error
			// in the reference; an identifier that names nobody is not
			// (items.grouping.R4).
			writeInvalidID(w, "userId", raw)
			return
		}
		if u, err := s.store.GetUser(r.Context(), storage.UserID(id)); err == nil {
			if !ac.User.IsAdministrator {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			target = u
		}
	}
	s.writeGroupingOptions(w, r, target)
}

// handleUserGroupingOptionsLegacy serves GET /Users/{userId}/GroupingOptions,
// the older form (items.grouping.R4): the same body, with the user taken from
// the path.
func (s *Server) handleUserGroupingOptionsLegacy(w http.ResponseWriter, r *http.Request) {
	ac, _ := AuthFromContext(r.Context())
	target := ac.User
	raw := r.PathValue("userId")
	if !strings.EqualFold(raw, string(ac.User.ID)) {
		id, ok := guid.Normalize(raw)
		if !ok {
			writeInvalidID(w, "userId", raw)
			return
		}
		if u, err := s.store.GetUser(r.Context(), storage.UserID(id)); err == nil {
			if !ac.User.IsAdministrator {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			target = u
		}
	}
	s.writeGroupingOptions(w, r, target)
}

// writeGroupingOptions answers the options for one user, whichever route
// asked: the libraries that user may access (policy EnableAllFolders /
// EnabledFolders, the same scope GET /UserViews uses), narrowed to the
// groupable collection types and ordered by name (items.grouping.R3).
func (s *Server) writeGroupingOptions(w http.ResponseWriter, r *http.Request, u storage.User) {
	scope, err := s.itemsScopeFor(r.Context(), u)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	libs := s.viewLibraries(r.Context(), scope)
	opts := make([]specialViewOptionDto, 0, len(libs))
	for _, lib := range libs {
		if !groupableCollection(lib.CollectionType) {
			continue
		}
		opts = append(opts, specialViewOptionDto{Name: lib.Name, Id: string(lib.ID)})
	}
	// By name, case-insensitively — not the sort-name order viewLibraries just
	// applied, and not the store's byte order either. Two names that fold to
	// the same key keep the incoming order: the reference's answer for that
	// case is not derivable from the names, so nothing is claimed about it.
	sort.SliceStable(opts, func(i, j int) bool {
		return storage.NameFoldKey(opts[i].Name) < storage.NameFoldKey(opts[j].Name)
	})
	writeJSON(w, http.StatusOK, opts)
}
