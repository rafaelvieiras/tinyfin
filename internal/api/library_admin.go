// library_admin.go implements the wire-compatible library-management
// endpoints a real Jellyfin dashboard's "Add Media Library"/"Scan All
// Libraries" actions use, wiring internal/scan (Phase 3, docs/08) up to
// the API surface. Route shapes (path, query param names) were confirmed
// against the published @jellyfin/sdk client's generated
// library-structure-api/library-api modules — docs/04's sanctioned
// secondary-verification source for a route the OpenAPI schema itself
// covers, not the reference server's own source (AGENTS.md's clean-room
// requirement).
//
// Deliberately not implemented (a documented gap, not an oversight):
// renaming a library, per-path add/remove/update on an existing library,
// and library-specific options — the reference server's Paths/Name/
// LibraryOptions sub-endpoints. Nothing this project's own dashboard flow
// (add a library, scan it) needs today; add them only once a real client
// interaction motivates it, per this project's "minimal over guessed"
// convention.
package api

import (
	"net/http"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// virtualFolderInfo mirrors the reference server's VirtualFolderInfo, kept
// to the fields this project actually populates with real data
// (LibraryOptions/PrimaryImageItemId/RefreshProgress/RefreshStatus are
// left off rather than guessed — see the package doc comment).
type virtualFolderInfo struct {
	Name           string   `json:"Name"`
	Locations      []string `json:"Locations"`
	CollectionType string   `json:"CollectionType"`
	ItemId         string   `json:"ItemId"`
}

func (s *Server) handleGetVirtualFolders(w http.ResponseWriter, r *http.Request) {
	libs, err := s.store.QueryLibraries(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out := make([]virtualFolderInfo, 0, len(libs))
	for _, lib := range libs {
		out = append(out, virtualFolderInfo{
			Name: lib.Name, Locations: lib.Paths,
			CollectionType: string(lib.CollectionType), ItemId: string(lib.ID),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddVirtualFolder is POST /Library/VirtualFolders — creates a
// library and, if refreshLibrary=true, queues a scan for it in the
// background (Server.runScan). paths accepts either a repeated query
// param ("paths=a&paths=b", how axios — @jellyfin/sdk's HTTP client —
// serializes an array param) or a single comma-delimited value, for
// robustness against either client-side serialization convention.
func (s *Server) handleAddVirtualFolder(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("name"))
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	paths := q["paths"]
	if len(paths) == 1 && strings.Contains(paths[0], ",") {
		paths = strings.Split(paths[0], ",")
	}

	lib := storage.Library{
		ID:             storage.LibraryID(randomID()),
		Name:           name,
		CollectionType: storage.CollectionType(q.Get("collectionType")),
		Paths:          paths,
	}
	if err := s.store.UpsertLibrary(r.Context(), lib); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if q.Get("refreshLibrary") == "true" {
		go s.runScan(lib)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveVirtualFolder is DELETE /Library/VirtualFolders — removes a
// library by Name (the reference server's own addressing scheme for this
// endpoint, confirmed against @jellyfin/sdk — see the package doc
// comment), not by ID. refreshLibrary is accepted for wire compatibility
// but has nothing to do once the library itself is gone.
func (s *Server) handleRemoveVirtualFolder(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	libs, err := s.store.QueryLibraries(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, lib := range libs {
		if lib.Name == name {
			if err := s.store.DeleteLibrary(r.Context(), lib.ID); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

// handleLibraryRefresh is POST /Library/Refresh — queues a background scan
// for every existing library (Server.runScan), matching a dashboard's
// "Scan All Libraries" action.
func (s *Server) handleLibraryRefresh(w http.ResponseWriter, r *http.Request) {
	libs, err := s.store.QueryLibraries(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, lib := range libs {
		go s.runScan(lib)
	}
	w.WriteHeader(http.StatusNoContent)
}
