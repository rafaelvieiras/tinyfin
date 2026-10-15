// collections.go implements the collection (BoxSet) management routes of the
// collections contract (docs/contract/specs/collections.md):
//
//	POST   /Collections                      create (or re-create) a collection
//	POST   /Collections/{collectionId}/Items add members
//	DELETE /Collections/{collectionId}/Items remove members
//	DELETE /Items/{itemId}                   delete a collection
//
// The model itself (library, membership, derived metadata) is
// internal/collection; this file is the wire layer.
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/collection"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// canManageCollections reports whether the caller may create, change and
// delete collections: an administrator, or a user whose policy has
// EnableCollectionManagement.
func canManageCollections(ac *AuthContext) bool {
	if ac == nil || !ac.Authenticated {
		return false
	}
	return ac.User.IsAdministrator || storedUserPolicy(ac.User).EnableCollectionManagement
}

// requireCollectionManagement answers 403 unless the caller may manage
// collections (collections.auth.R2).
func requireCollectionManagement(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ac, _ := AuthFromContext(r.Context())
		if !canManageCollections(ac) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// writeCollectionError is the plain-text 400 the reference answers with for
// an id that names nothing usable.
func writeCollectionError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte("Error processing request."))
}

// collectionMemberIDs resolves the ids query parameter. Tokens that are not
// GUIDs are skipped; ok is false when a GUID names no item the caller can
// read (library ids are accepted and ignored, as the reference does).
func (s *Server) collectionMemberIDs(ctx context.Context, tokens []string) (ids []storage.ItemID, ok bool, err error) {
	sc, err := s.callerScope(ctx)
	if err != nil {
		return nil, false, err
	}
	seen := map[storage.ItemID]bool{}
	for _, tok := range tokens {
		g, valid := normalizeGUID(tok)
		if !valid {
			continue
		}
		id := storage.ItemID(g)
		if seen[id] {
			continue
		}
		item, gerr := s.store.GetItem(ctx, id)
		if gerr != nil {
			if _, lerr := s.store.GetLibrary(ctx, storage.LibraryID(g)); lerr == nil {
				continue
			}
			return nil, false, nil
		}
		if !s.itemReadable(ctx, sc, item) {
			return nil, false, nil
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, true, nil
}

// handleCreateCollection is POST /Collections (name, ids, parentId, isLocked
// in the query). Creating a name that already exists addresses the same
// collection and replaces its members (collections.create.R4).
func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	q := caseInsensitiveQuery(r)
	name := q.Get("name")
	if strings.TrimSpace(name) == "" {
		writeBadRequest(w)
		return
	}
	if raw := strings.TrimSpace(q.Get("parentid")); raw != "" {
		if _, valid := normalizeGUID(raw); !valid {
			writeInvalidID(w, "parentId", raw)
			return
		}
	}
	if raw := strings.TrimSpace(q.Get("islocked")); raw != "" {
		if l := strings.ToLower(raw); l != "true" && l != "false" {
			writeValidationProblem(w, "isLocked", "The value '"+raw+"' is not valid.")
			return
		}
	}
	tokens := itemsListParam(q, "ids")
	for _, tok := range tokens {
		if _, valid := normalizeGUID(tok); !valid {
			writeCollectionError(w)
			return
		}
	}
	ids, ok, err := s.collectionMemberIDs(r.Context(), tokens)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !ok {
		writeCollectionError(w)
		return
	}
	box, err := collection.Create(r.Context(), s.store, s.cfg.DataDir, name, ids)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Id": string(box.ID)})
}

// loadCollection resolves {collectionId}: a non-GUID is a validation
// problem, an id that is not a collection the caller can read answers 400
// like the reference. ok is false when a response was written.
func (s *Server) loadCollection(w http.ResponseWriter, r *http.Request) (storage.Item, bool) {
	raw := r.PathValue("collectionId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "collectionId", raw)
		return storage.Item{}, false
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(norm))
	if err != nil || item.Type != storage.ItemTypeBoxSet {
		writeCollectionError(w)
		return storage.Item{}, false
	}
	sc, err := s.callerScope(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return storage.Item{}, false
	}
	if !s.itemReadable(r.Context(), sc, item) {
		writeCollectionError(w)
		return storage.Item{}, false
	}
	return item, true
}

// handleAddToCollection is POST /Collections/{collectionId}/Items.
func (s *Server) handleAddToCollection(w http.ResponseWriter, r *http.Request) {
	box, ok := s.loadCollection(w, r)
	if !ok {
		return
	}
	ids, ok, err := s.collectionMemberIDs(r.Context(), itemsListParam(caseInsensitiveQuery(r), "ids"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !ok {
		writeCollectionError(w)
		return
	}
	if _, err := collection.AddMembers(r.Context(), s.store, box.ID, ids); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveFromCollection is DELETE /Collections/{collectionId}/Items.
// Ids that are not members, or name nothing, are ignored.
func (s *Server) handleRemoveFromCollection(w http.ResponseWriter, r *http.Request) {
	box, ok := s.loadCollection(w, r)
	if !ok {
		return
	}
	var ids []storage.ItemID
	for _, tok := range itemsListParam(caseInsensitiveQuery(r), "ids") {
		if g, valid := normalizeGUID(tok); valid {
			ids = append(ids, storage.ItemID(g))
		}
	}
	if err := s.store.RemoveFromCollection(r.Context(), box.ID, ids); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteItem is DELETE /Items/{itemId}. Only collections can be
// deleted: Tinyfin never removes media files, so any other item is refused
// (403, or 401 for a caller without content-deletion rights, as the
// reference answers a caller that may not delete).
func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("itemId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "itemId", raw)
		return
	}
	ctx := r.Context()
	ac, _ := AuthFromContext(ctx)
	item, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	sc, err := s.callerScope(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !s.itemReadable(ctx, sc, item) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	if item.Type == storage.ItemTypePlaylist {
		s.deletePlaylist(w, r, item)
		return
	}
	if item.Type != storage.ItemTypeBoxSet {
		if !mayDeleteContent(ac, item) {
			writeUnauthorizedAccess(w)
			return
		}
		// Tinyfin never removes media files: a caller that may delete is
		// refused (403), one that may not gets the reference's 401.
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if !canManageCollections(ac) {
		writeUnauthorizedAccess(w)
		return
	}
	if err := s.store.DeleteItem(ctx, item.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	collection.PruneEmptyFolder(ctx, s.store)
	w.WriteHeader(http.StatusNoContent)
}

// writeUnauthorizedAccess is the 401 with the bare JSON string the reference
// sends to a caller that may not perform the action.
func writeUnauthorizedAccess(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, "Unauthorized access")
}
