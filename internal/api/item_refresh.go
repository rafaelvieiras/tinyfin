// item_refresh.go implements POST /Items/{itemId}/Refresh — the route
// jellyfin-web's "Rastrear biblioteca" (scan one library) dashboard action
// calls, passing the library's own ItemId (the same ID GET
// /Library/VirtualFolders hands out, per virtualFolderInfo.ItemId in
// library_admin.go), not a separate library-scoped path. Confirmed live
// against the reference server (staging, :8097): the exact call is
// POST /Items/{itemId}/Refresh?Recursive=true&ImageRefreshMode=Default&
// MetadataRefreshMode=Default&ReplaceAllImages=false&
// RegenerateTrickplay=false&ReplaceAllMetadata=false. The refresh-mode query
// params are accepted and ignored — this project has no per-mode scan
// behavior to select between (docs/11).
package api

import (
	"net/http"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// handleItemRefresh serves POST /Items/{itemId}/Refresh. Two distinct
// itemId shapes reach this route:
//
//   - A library's own ID (see the package doc comment) — queues a background
//     scan of that one library, the same fire-and-forget pattern
//     handleLibraryRefresh (library_admin.go) uses for "scan all libraries".
//   - An ordinary catalog item's ID — historically also the "refresh this
//     item's metadata" route, but this project has no single-item
//     re-fetch mechanism yet (a documented future gap, not this task). Rather
//     than silently no-op an unrecognized ID (indistinguishable from a
//     client bug) or fake a refresh that never happens, this confirms the
//     item is real and does nothing further: an honest no-op, not a
//     simulated one.
func (s *Server) handleItemRefresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("itemId")
	ctx := r.Context()

	libs, err := s.store.QueryLibraries(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, lib := range libs {
		if string(lib.ID) == id {
			go s.runScan(lib)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	if _, err := s.store.GetItem(ctx, storage.ItemID(id)); err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusNotFound)
}
