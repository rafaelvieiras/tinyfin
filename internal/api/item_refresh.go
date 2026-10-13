// item_refresh.go implements POST /Items/{itemId}/Refresh — the route
// jellyfin-web's "Rastrear biblioteca" (scan one library) dashboard action
// calls, passing the library's own ItemId (the same ID GET
// /Library/VirtualFolders hands out, per virtualFolderInfo.ItemId in
// library_admin.go), not a separate library-scoped path. Confirmed live
// against the reference server: the exact call is
// POST /Items/{itemId}/Refresh?Recursive=true&ImageRefreshMode=Default&
// MetadataRefreshMode=Default&ReplaceAllImages=false&
// RegenerateTrickplay=false&ReplaceAllMetadata=false. Of these,
// ReplaceAllImages is honored (handleItemRefresh below); Recursive,
// ImageRefreshMode, MetadataRefreshMode and RegenerateTrickplay are
// accepted and ignored — this project has no per-mode scan behavior to
// select between (docs/11).
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
//   - An ordinary catalog item's ID — the "refresh this item's metadata"
//     route: queues a background re-enrichment against the configured
//     metadata.Provider (Server.runItemRefresh -> scan.Scanner.RefreshItem),
//     the per-item counterpart to a library scan's own enrich pass. Used to
//     be an honest no-op (confirm the item exists, do nothing else) because
//     this project had no single-item re-fetch mechanism at all — found live
//     when refreshing a real series' metadata silently had no effect at all.
//     Deliberately does not surface as "running" anywhere yet (no
//     GET /ScheduledTasks entry, no progress indicator) — the reference
//     server tracks this per-item, not via the library-scan task; see
//     runItemRefresh's own doc comment for why reusing that tracker was
//     tried and reverted. A real per-item indicator is a documented gap
//     (docs/11), not silently faked here.
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
		// ReplaceAllImages=true, validated against the reference server
		// (docs/08's "Local-vs-remote precedence" section), is the only way
		// a Provider-returned image is allowed to overwrite one the item
		// already has — RefreshItem's own doc comment.
		replaceImages := r.URL.Query().Get("ReplaceAllImages") == "true"
		go s.runItemRefresh(storage.ItemID(id), replaceImages)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusNotFound)
}
