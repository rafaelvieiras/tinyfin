// playlists.go implements the playlist routes of the playlists contract
// (docs/contract/specs/playlists.md):
//
//	POST   /Playlists                                       create
//	GET    /Playlists/{playlistId}                          read (access + entry ids)
//	POST   /Playlists/{playlistId}                          update
//	GET    /Playlists/{playlistId}/Items                    the entries as items
//	POST   /Playlists/{playlistId}/Items                    add entries
//	DELETE /Playlists/{playlistId}/Items                    remove entries
//	POST   /Playlists/{playlistId}/Items/{itemId}/Move/{n}  reorder
//	GET    /Playlists/{playlistId}/Users                    shares (owner)
//	GET    /Playlists/{playlistId}/Users/{userId}           own permission
//	POST   /Playlists/{playlistId}/Users/{userId}           share / change
//	DELETE /Playlists/{playlistId}/Users/{userId}           unshare
//
// The model itself (library, folder, derived metadata) is internal/playlist;
// this file is the wire layer.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/playlist"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// playlistsViewID and playlistsFolderItemID: the user's Playlists view
// (listed with the libraries) and the folder every playlist sits in.
var playlistsViewID = string(playlist.ViewID())

// playlistShareDto mirrors the reference's PlaylistUserPermissions.
type playlistShareDto struct {
	UserID  string `json:"UserId"`
	CanEdit bool   `json:"CanEdit"`
}

// playlistDto is the body of GET /Playlists/{playlistId}.
type playlistDto struct {
	OpenAccess bool               `json:"OpenAccess"`
	Shares     []playlistShareDto `json:"Shares"`
	ItemIDs    []string           `json:"ItemIds"`
}

// createPlaylistBody is CreatePlaylistDto; pointers tell absent from empty.
type createPlaylistBody struct {
	Name      *string            `json:"Name"`
	IDs       []string           `json:"Ids"`
	UserID    *string            `json:"UserId"`
	MediaType *string            `json:"MediaType"`
	Users     []playlistShareDto `json:"Users"`
	IsPublic  *bool              `json:"IsPublic"`
}

// updatePlaylistBody is UpdatePlaylistDto: a field left out is not changed.
type updatePlaylistBody struct {
	Name     *string             `json:"Name"`
	IDs      *[]string           `json:"Ids"`
	Users    *[]playlistShareDto `json:"Users"`
	IsPublic *bool               `json:"IsPublic"`
}

// writePlaylistNotFound is the 404 the reference gives for a playlist that
// does not exist or that the caller may not read: a bare JSON string.
func writePlaylistNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, "Playlist not found")
}

// playlistAccessFor loads the access record of a playlist.
func (s *Server) playlistAccessFor(ctx context.Context, id storage.ItemID) (storage.PlaylistAccess, bool) {
	a, err := s.store.GetPlaylistAccess(ctx, id)
	return a, err == nil
}

// canReadPlaylist: owner, shared with, or open to everybody. An empty user
// (an API key) reads everything.
func canReadPlaylist(a storage.PlaylistAccess, user storage.UserID) bool {
	if user == "" || a.OpenAccess || a.OwnerID == user {
		return true
	}
	for _, sh := range a.Shares {
		if sh.UserID == user {
			return true
		}
	}
	return false
}

// canEditPlaylist: owner, or shared with edit permission.
func canEditPlaylist(a storage.PlaylistAccess, user storage.UserID) bool {
	if user == "" || a.OwnerID == user {
		return true
	}
	for _, sh := range a.Shares {
		if sh.UserID == user && sh.CanEdit {
			return true
		}
	}
	return false
}

// playlistReadable reports whether user may read the playlist id.
func (s *Server) playlistReadable(ctx context.Context, user storage.UserID, id storage.ItemID) bool {
	a, ok := s.playlistAccessFor(ctx, id)
	return ok && canReadPlaylist(a, user)
}

// callerUserID is the id of the authenticated user ("" for an API key).
func callerUserID(ctx context.Context) storage.UserID {
	if ac, ok := AuthFromContext(ctx); ok && ac != nil {
		return ac.User.ID
	}
	return ""
}

// loadPlaylist resolves {playlistId}: a non-GUID is a validation problem, a
// playlist that is missing or unreadable is the 404 string. needEdit makes a
// readable playlist the caller may not edit a 403.
func (s *Server) loadPlaylist(w http.ResponseWriter, r *http.Request, needEdit bool) (storage.Item, storage.PlaylistAccess, bool) {
	raw := r.PathValue("playlistId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "playlistId", raw)
		return storage.Item{}, storage.PlaylistAccess{}, false
	}
	ctx := r.Context()
	item, err := s.store.GetItem(ctx, storage.ItemID(norm))
	if err != nil || item.Type != storage.ItemTypePlaylist {
		writePlaylistNotFound(w)
		return storage.Item{}, storage.PlaylistAccess{}, false
	}
	a, ok := s.playlistAccessFor(ctx, item.ID)
	user := callerUserID(ctx)
	if !ok || !canReadPlaylist(a, user) {
		writePlaylistNotFound(w)
		return storage.Item{}, storage.PlaylistAccess{}, false
	}
	if needEdit && !canEditPlaylist(a, user) {
		w.WriteHeader(http.StatusForbidden)
		return storage.Item{}, storage.PlaylistAccess{}, false
	}
	return item, a, true
}

// expandPlaylistEntries turns requested item ids into playlist entries: a
// playable item is itself; a series, season, folder or collection is the
// playable items below it in listing order; a playlist is its entries. ok is
// false when a GUID names no item the caller can read.
func (s *Server) expandPlaylistEntries(ctx context.Context, tokens []string, strict bool) (entries []storage.ItemID, ok bool, err error) {
	sc, err := s.callerScope(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, tok := range tokens {
		g, valid := normalizeGUID(tok)
		if !valid {
			if strict {
				return nil, false, nil
			}
			continue
		}
		item, gerr := s.store.GetItem(ctx, storage.ItemID(g))
		if gerr != nil || !s.itemReadable(ctx, sc, item) {
			if strict {
				return nil, false, nil
			}
			continue
		}
		leaves, err := s.playlistLeaves(ctx, sc, item, 0)
		if err != nil {
			return nil, false, err
		}
		entries = append(entries, leaves...)
	}
	return entries, true, nil
}

const maxPlaylistExpansion = 5000

func (s *Server) playlistLeaves(ctx context.Context, sc itemsScope, item storage.Item, depth int) ([]storage.ItemID, error) {
	if depth > 4 {
		return nil, nil
	}
	switch item.Type {
	case storage.ItemTypeSeries, storage.ItemTypeSeason, storage.ItemTypeFolder, storage.ItemTypePhotoAlbum:
		f := storage.ItemFilter{ParentID: &item.ID, Recursive: true, ExcludeItemTypes: storage.FolderItemTypes, Limit: maxPlaylistExpansion}
		sc.apply(&f)
		page, err := s.store.QueryItems(ctx, f)
		if err != nil {
			return nil, err
		}
		out := make([]storage.ItemID, 0, len(page.Items))
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out, nil
	case storage.ItemTypeBoxSet:
		members, err := s.store.CollectionMembers(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		var out []storage.ItemID
		for _, m := range members {
			mi, err := s.store.GetItem(ctx, m)
			if err != nil || !s.itemReadable(ctx, sc, mi) {
				continue
			}
			sub, err := s.playlistLeaves(ctx, sc, mi, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
		return out, nil
	case storage.ItemTypePlaylist:
		return s.store.PlaylistEntries(ctx, item.ID)
	}
	return []storage.ItemID{item.ID}, nil
}

// readBody decodes a JSON body into v. present is false for an empty body.
func readBody(r *http.Request, v any) (present bool, err error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return false, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(data, v)
}

func writeBodyError(w http.ResponseWriter, msg string) {
	writeValidationProblem(w, "$", msg)
}

// parseShares validates a Users list: every UserId must be a GUID.
func parseShares(in []playlistShareDto) ([]storage.PlaylistShare, bool) {
	out := make([]storage.PlaylistShare, 0, len(in))
	seen := map[storage.UserID]int{}
	for _, u := range in {
		g, ok := normalizeGUID(u.UserID)
		if !ok {
			return nil, false
		}
		id := storage.UserID(g)
		if i, dup := seen[id]; dup {
			out[i].CanEdit = u.CanEdit
			continue
		}
		seen[id] = len(out)
		out = append(out, storage.PlaylistShare{UserID: id, CanEdit: u.CanEdit})
	}
	return out, true
}

// handleCreatePlaylist is POST /Playlists. The playlist is described by the
// JSON body; name, ids, userId and mediaType in the query are the older
// form and win over the body. A request with no body at all creates a
// private playlist, a body without IsPublic a public one.
func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := caseInsensitiveQuery(r)
	var body createPlaylistBody
	present, err := readBody(r, &body)
	if err != nil {
		writeInvalidBody(w)
		return
	}
	name := ""
	haveName := false
	if body.Name != nil {
		name, haveName = *body.Name, true
	}
	if v, ok := q["name"]; ok && len(v) > 0 {
		name, haveName = v[0], true
	}
	if !haveName {
		if present {
			writeBodyError(w, "The request body is missing the required property 'Name'.")
		} else {
			writeValidationProblem(w, "name", "The name field is required.")
		}
		return
	}
	tokens := body.IDs
	if _, ok := q["ids"]; ok {
		tokens = itemsListParam(q, "ids")
	}
	for _, tok := range tokens {
		if _, valid := normalizeGUID(tok); !valid {
			writeInvalidID(w, "ids", tok)
			return
		}
	}
	ac, _ := AuthFromContext(ctx)
	owner := ac.User.ID
	rawOwner := ""
	if body.UserID != nil {
		rawOwner = *body.UserID
	}
	if v := q.Get("userid"); v != "" {
		rawOwner = v
	}
	if rawOwner != "" {
		g, valid := normalizeGUID(rawOwner)
		if !valid {
			writeInvalidID(w, "userId", rawOwner)
			return
		}
		if g != guid.Zero && storage.UserID(g) != ac.User.ID {
			// Only an administrator creates a playlist for somebody else.
			if !ac.User.IsAdministrator {
				writeCollectionError0(w, http.StatusForbidden)
				return
			}
			u, gerr := s.store.GetUser(ctx, storage.UserID(g))
			if gerr != nil {
				writeCollectionError0(w, http.StatusBadRequest)
				return
			}
			owner = u.ID
		}
	}
	media := ""
	if body.MediaType != nil {
		media = *body.MediaType
	}
	if v := q.Get("mediatype"); v != "" {
		media = v
	}
	entries, ok, err := s.expandPlaylistEntries(ctx, tokens, true)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !ok {
		writeCollectionError0(w, http.StatusBadRequest)
		return
	}
	open := present
	if body.IsPublic != nil {
		open = *body.IsPublic
	}
	shares, valid := parseShares(body.Users)
	if !valid {
		writeBodyError(w, "A share names a user id that is not valid.")
		return
	}
	item, err := playlist.Create(ctx, s.store, s.cfg.DataDir, playlist.Spec{
		Name: name, Owner: owner, MediaType: media, Open: open, Shares: shares, Entries: entries,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Id": string(item.ID)})
}

// writeCollectionError0 is the plain-text "Error processing request." body
// with the given status (the collections routes use the same one).
func writeCollectionError0(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("Error processing request."))
}

// handleGetPlaylist is GET /Playlists/{playlistId}.
func (s *Server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	item, a, ok := s.loadPlaylist(w, r, false)
	if !ok {
		return
	}
	entries, err := s.store.PlaylistEntries(r.Context(), item.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	dto := playlistDto{OpenAccess: a.OpenAccess, Shares: []playlistShareDto{}, ItemIDs: make([]string, 0, len(entries))}
	for _, sh := range a.Shares {
		dto.Shares = append(dto.Shares, playlistShareDto{UserID: string(sh.UserID), CanEdit: sh.CanEdit})
	}
	for _, e := range entries {
		dto.ItemIDs = append(dto.ItemIDs, string(e))
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleUpdatePlaylist is POST /Playlists/{playlistId}.
func (s *Server) handleUpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	item, a, ok := s.loadPlaylist(w, r, true)
	if !ok {
		return
	}
	var body updatePlaylistBody
	if _, err := readBody(r, &body); err != nil {
		writeInvalidBody(w)
		return
	}
	if body.IDs != nil {
		for _, tok := range *body.IDs {
			if _, valid := normalizeGUID(tok); !valid {
				writeInvalidID(w, "Ids", tok)
				return
			}
		}
	}
	var entries []storage.ItemID
	if body.IDs != nil {
		var okIDs bool
		var err error
		entries, okIDs, err = s.expandPlaylistEntries(ctx, *body.IDs, true)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !okIDs {
			writeCollectionError0(w, http.StatusBadRequest)
			return
		}
	}
	if body.Users != nil {
		shares, valid := parseShares(*body.Users)
		if !valid {
			writeBodyError(w, "A share names a user id that is not valid.")
			return
		}
		a.Shares = shares
	}
	if body.IsPublic != nil {
		a.OpenAccess = *body.IsPublic
	}
	if err := s.store.SetPlaylistAccess(ctx, a); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if body.IDs != nil {
		if err := s.store.SetPlaylistEntries(ctx, item.ID, entries); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" && *body.Name != item.Name {
		item.Name, item.SortName = *body.Name, *body.Name
		if err := s.store.UpsertItem(ctx, item); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	if _, err := playlist.Refresh(ctx, s.store, item.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePlaylistItems is GET /Playlists/{playlistId}/Items: the entries in
// order, each carrying PlaylistItemId. Entries the caller's parental limits
// hide are left out; library access does not apply to them.
func (s *Server) handlePlaylistItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	item, _, ok := s.loadPlaylist(w, r, false)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	user, ok := s.itemsContextUser(w, r, q.Get("userid"))
	if !ok {
		return
	}
	iq, err := parseItemsQuery(q, false)
	if err != nil {
		writeValidationProblem(w, "query", "One or more query parameters are not valid.")
		return
	}
	entries, err := s.store.PlaylistEntries(ctx, item.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	sc, err := s.itemsScopeFor(ctx, user)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if ac, ok := AuthFromContext(ctx); ok && ac.User.ID != user.ID {
		cp := *ac
		cp.User = user
		ctx = contextWithAuth(ctx, &cp)
	}
	cache := map[storage.ItemID]*storage.Item{}
	var visible []storage.Item
	for _, e := range entries {
		it, seen := cache[e]
		if !seen {
			got, gerr := s.store.GetItem(ctx, e)
			if gerr != nil || !s.passesParental(ctx, sc, got) {
				cache[e] = nil
				continue
			}
			it = &got
			cache[e] = it
		}
		if it != nil {
			visible = append(visible, *it)
		}
	}
	total := len(visible)
	start := iq.startIndex
	if start > total {
		start = total
	}
	page := visible[start:]
	if iq.limitGiven && iq.limit < len(page) {
		page = page[:iq.limit]
	}
	out := make([]baseItemDto, 0, len(page))
	for _, it := range page {
		dto := s.baseItemDto(ctx, it)
		dto.PlaylistItemID = string(it.ID)
		out = append(out, shapeListItem(dto, iq))
	}
	writeJSON(w, http.StatusOK, baseItemDtoQueryResult{Items: out, TotalRecordCount: total, StartIndex: iq.startIndex})
}

// passesParental applies the policy's tag rules and parental limits to one
// item, without the library rule.
func (s *Server) passesParental(ctx context.Context, sc itemsScope, item storage.Item) bool {
	has := func(set []string) bool {
		for _, t := range item.Tags {
			for _, b := range set {
				if strings.EqualFold(t, b) {
					return true
				}
			}
		}
		return false
	}
	if has(sc.blockedTags) || (len(sc.allowedTags) > 0 && !has(sc.allowedTags)) {
		return false
	}
	return s.ratingAllows(ctx, sc, item)
}

// handleAddToPlaylist is POST /Playlists/{playlistId}/Items. Ids that are
// not GUIDs or name nothing readable are ignored; position (default: the
// end) is clamped into the list.
func (s *Server) handleAddToPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	item, _, ok := s.loadPlaylist(w, r, true)
	if !ok {
		return
	}
	q := caseInsensitiveQuery(r)
	add, _, err := s.expandPlaylistEntries(ctx, itemsListParam(q, "ids"), false)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	entries, err := s.store.PlaylistEntries(ctx, item.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	pos := len(entries)
	if raw := strings.TrimSpace(q.Get("position")); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil {
			writeValidationProblem(w, "position", "The value '"+raw+"' is not valid.")
			return
		}
		if n < 0 {
			n = 0
		}
		if n < pos {
			pos = n
		}
	}
	merged := make([]storage.ItemID, 0, len(entries)+len(add))
	merged = append(merged, entries[:pos]...)
	merged = append(merged, add...)
	merged = append(merged, entries[pos:]...)
	if err := s.writePlaylistEntries(ctx, item.ID, merged); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writePlaylistEntries(ctx context.Context, id storage.ItemID, entries []storage.ItemID) error {
	if err := s.store.SetPlaylistEntries(ctx, id, entries); err != nil {
		return err
	}
	_, err := playlist.Refresh(ctx, s.store, id)
	return err
}

// handleRemoveFromPlaylist is DELETE /Playlists/{playlistId}/Items: every
// entry of an item named in entryIds goes (an entry is identified by its
// item's id).
func (s *Server) handleRemoveFromPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	item, _, ok := s.loadPlaylist(w, r, true)
	if !ok {
		return
	}
	drop := map[storage.ItemID]bool{}
	for _, tok := range itemsListParam(caseInsensitiveQuery(r), "entryids") {
		if g, valid := normalizeGUID(tok); valid {
			drop[storage.ItemID(g)] = true
		}
	}
	entries, err := s.store.PlaylistEntries(ctx, item.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	kept := make([]storage.ItemID, 0, len(entries))
	for _, e := range entries {
		if !drop[e] {
			kept = append(kept, e)
		}
	}
	if len(kept) != len(entries) {
		if err := s.writePlaylistEntries(ctx, item.ID, kept); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMovePlaylistItem is POST /Playlists/{playlistId}/Items/{itemId}/Move/{newIndex}:
// the first entry of the item moves to newIndex. An item that is not in the
// playlist changes nothing.
func (s *Server) handleMovePlaylistItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	item, _, ok := s.loadPlaylist(w, r, true)
	if !ok {
		return
	}
	rawID := r.PathValue("itemId")
	entryID, valid := normalizeGUID(rawID)
	if !valid {
		writeInvalidID(w, "itemId", rawID)
		return
	}
	rawIdx := r.PathValue("newIndex")
	idx, err := strconv.Atoi(rawIdx)
	if err != nil {
		writeInvalidID(w, "newIndex", rawIdx)
		return
	}
	entries, err := s.store.PlaylistEntries(ctx, item.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if idx < 0 || idx >= len(entries) {
		writeBadRequest(w)
		return
	}
	from := -1
	for i, e := range entries {
		if string(e) == entryID {
			from = i
			break
		}
	}
	if from < 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	moved := entries[from]
	rest := append(append([]storage.ItemID{}, entries[:from]...), entries[from+1:]...)
	out := make([]storage.ItemID, 0, len(entries))
	out = append(out, rest[:idx]...)
	out = append(out, moved)
	out = append(out, rest[idx:]...)
	if err := s.store.SetPlaylistEntries(ctx, item.ID, out); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePlaylistUsers is GET /Playlists/{playlistId}/Users: the shares, for
// the owner only.
func (s *Server) handlePlaylistUsers(w http.ResponseWriter, r *http.Request) {
	_, a, ok := s.loadPlaylist(w, r, false)
	if !ok {
		return
	}
	if user := callerUserID(r.Context()); user != "" && user != a.OwnerID {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	out := make([]playlistShareDto, 0, len(a.Shares))
	for _, sh := range a.Shares {
		out = append(out, playlistShareDto{UserID: string(sh.UserID), CanEdit: sh.CanEdit})
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePlaylistUser is GET /Playlists/{playlistId}/Users/{userId}: the
// permission record of a user. The owner and the users who may edit read the
// record of any user (404 when the user has none; the owner has no record
// but is answered as an editor); a user who may only read gets their own
// record (404 when they have none) and is refused the others.
func (s *Server) handlePlaylistUser(w http.ResponseWriter, r *http.Request) {
	_, a, ok := s.loadPlaylist(w, r, false)
	if !ok {
		return
	}
	raw := r.PathValue("userId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "userId", raw)
		return
	}
	user := callerUserID(r.Context())
	target := storage.UserID(norm)
	if target == a.OwnerID {
		if user != "" && !canEditPlaylist(a, user) && user != target {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, playlistShareDto{UserID: string(a.OwnerID), CanEdit: true})
		return
	}
	if user != "" && !canEditPlaylist(a, user) && user != target {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	for _, sh := range a.Shares {
		if sh.UserID == target {
			writeJSON(w, http.StatusOK, playlistShareDto{UserID: string(target), CanEdit: sh.CanEdit})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, "User permissions not found")
}

// handleSetPlaylistUser is POST /Playlists/{playlistId}/Users/{userId}: the
// owner (or an editor) shares the playlist with the user or changes their
// permission; an omitted CanEdit means false.
func (s *Server) handleSetPlaylistUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	item, a, ok := s.loadPlaylist(w, r, true)
	if !ok {
		return
	}
	raw := r.PathValue("userId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "userId", raw)
		return
	}
	var body struct {
		CanEdit *bool `json:"CanEdit"`
	}
	if _, err := readBody(r, &body); err != nil {
		writeInvalidBody(w)
		return
	}
	edit := body.CanEdit != nil && *body.CanEdit
	found := false
	for i := range a.Shares {
		if string(a.Shares[i].UserID) == norm {
			a.Shares[i].CanEdit, found = edit, true
		}
	}
	if !found {
		a.Shares = append(a.Shares, storage.PlaylistShare{UserID: storage.UserID(norm), CanEdit: edit})
	}
	if err := s.store.SetPlaylistAccess(ctx, a); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = item
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePlaylistUser is DELETE /Playlists/{playlistId}/Users/{userId}.
func (s *Server) handleDeletePlaylistUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, a, ok := s.loadPlaylist(w, r, true)
	if !ok {
		return
	}
	raw := r.PathValue("userId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "userId", raw)
		return
	}
	kept := make([]storage.PlaylistShare, 0, len(a.Shares))
	for _, sh := range a.Shares {
		if string(sh.UserID) != norm {
			kept = append(kept, sh)
		}
	}
	if len(kept) == len(a.Shares) {
		writeJSON(w, http.StatusNotFound, "User permissions not found")
		return
	}
	a.Shares = kept
	if err := s.store.SetPlaylistAccess(ctx, a); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// playlistMediaType is the media type a playlist reports.
func (s *Server) playlistMediaType(ctx context.Context, id storage.ItemID) string {
	if a, ok := s.playlistAccessFor(ctx, id); ok && a.MediaType != "" {
		return a.MediaType
	}
	return "Audio"
}

// applyPlaylistInfo completes the DTO of a playlist: its media type, the
// number of entries and whether the caller owns it.
func (s *Server) applyPlaylistInfo(ctx context.Context, dto *baseItemDto, item storage.Item) {
	a, ok := s.playlistAccessFor(ctx, item.ID)
	if ok && a.MediaType != "" {
		dto.MediaType = a.MediaType
	} else {
		dto.MediaType = "Audio"
	}
	if entries, err := s.store.PlaylistEntries(ctx, item.ID); err == nil {
		n := len(entries)
		dto.ChildCount = &n
	}
	if user := callerUserID(ctx); ok && user != "" {
		dto.CanDelete = a.OwnerID == user
		dto.CanDownload = false
	}
}

// applyPlaylistDetail completes the single-item form of a playlist: its
// counts are of the distinct items it holds (the list form counts entries),
// and it reports the running time of everything in it.
func (s *Server) applyPlaylistDetail(ctx context.Context, dto *baseItemDto, item storage.Item) {
	entries, err := s.store.PlaylistEntries(ctx, item.ID)
	if err != nil {
		return
	}
	seen := map[storage.ItemID]bool{}
	for _, e := range entries {
		seen[e] = true
	}
	n := len(seen)
	dto.ChildCount, dto.RecursiveItemCount = &n, &n
	var ticks int64
	if item.RunTimeTicks != nil {
		ticks = *item.RunTimeTicks
	}
	dto.CumulativeRunTimeTicks = &ticks
}

// playlistUserData fills the played counters of a playlist's UserData from
// its distinct entries.
func (s *Server) playlistUserData(ctx context.Context, userID storage.UserID, item storage.Item, dto *userItemDataDto) {
	if s.playlistMediaType(ctx, item.ID) != "Video" {
		return
	}
	entries, err := s.store.PlaylistEntries(ctx, item.ID)
	if err != nil {
		return
	}
	seen := map[storage.ItemID]bool{}
	total, unplayed := 0, 0
	for _, e := range entries {
		if seen[e] {
			continue
		}
		seen[e] = true
		total++
		if st, err := s.store.GetPlaystate(ctx, userID, e); err != nil || !st.Played {
			unplayed++
		}
	}
	if total == 0 {
		return
	}
	dto.UnplayedItemCount = &unplayed
	pct := float64(total-unplayed) * 100 / float64(total)
	dto.PlayedPercentage = &pct
	dto.Played = unplayed == 0
}

// deletePlaylist handles DELETE /Items/{playlistId}: only the owner may.
func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request, item storage.Item) {
	ctx := r.Context()
	a, ok := s.playlistAccessFor(ctx, item.ID)
	user := callerUserID(ctx)
	if !ok || !canReadPlaylist(a, user) {
		writeItemsProblem(w, http.StatusNotFound, "Not Found")
		return
	}
	if user != "" && a.OwnerID != user {
		writeUnauthorizedAccess(w)
		return
	}
	if err := s.store.DeleteItem(ctx, item.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errPlaylistNotFound = errors.New("playlist not found")

// userHasPlaylists reports whether the scope's user may read at least one
// playlist (the Playlists view is listed only then).
func (s *Server) userHasPlaylists(ctx context.Context, sc itemsScope) bool {
	f := storage.ItemFilter{ItemTypes: []storage.ItemType{storage.ItemTypePlaylist}, Limit: 1}
	sc.apply(&f)
	page, err := s.store.QueryItems(ctx, f)
	return err == nil && page.TotalCount > 0
}

// playlistsViewDto is the user's Playlists view: the entry of the views
// listing that opens the playlists.
func (s *Server) playlistsViewDto(ctx context.Context) baseItemDto {
	dto := baseItemDto{
		Name:           playlist.LibraryName,
		ServerID:       s.cfg.ServerID,
		ID:             playlistsViewID,
		SortName:       "playlists",
		IsFolder:       true,
		Type:           string(storage.ItemTypeUserView),
		CollectionType: string(storage.CollectionTypePlaylists),
		MediaType:      "Unknown",
		LocationType:   "FileSystem",
	}
	if ac, ok := AuthFromContext(ctx); ok && ac != nil {
		dto.UserData = s.userDataFor(ctx, ac.User.ID, storage.Item{ID: storage.ItemID(playlistsViewID), Type: storage.ItemTypeUserView})
	}
	return dto
}
