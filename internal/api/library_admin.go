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
// per-path add/remove/update on an existing library — the reference
// server's Paths sub-endpoints. Nothing this project's own dashboard flow
// (add a library, scan it, rename it, save its options) needs today; add
// it only once a real client interaction motivates it, per this project's
// "minimal over guessed" convention.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// virtualFolderInfo mirrors the reference server's VirtualFolderInfo, kept
// to the fields this project actually populates with real data
// (PrimaryImageItemId/RefreshProgress/RefreshStatus are left off rather
// than guessed — see the package doc comment). LibraryOptions is the raw
// blob POST /Library/VirtualFolders/LibraryOptions last saved, echoed back
// as-is (Tinyfin doesn't interpret it — see storage.Library.LibraryOptions).
type virtualFolderInfo struct {
	Name           string          `json:"Name"`
	Locations      []string        `json:"Locations"`
	CollectionType string          `json:"CollectionType"`
	ItemId         string          `json:"ItemId"`
	LibraryOptions json.RawMessage `json:"LibraryOptions,omitempty"`
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
			LibraryOptions: json.RawMessage(lib.LibraryOptions),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddVirtualFolder is POST /Library/VirtualFolders — creates a
// library and, if refreshLibrary=true, queues a scan for it in the
// background (Server.runScan). Paths come from the request body when one
// is present: the real dashboard's "Add Media Library" flow (confirmed
// live, byte for byte) sends collectionType/refreshLibrary/name on the
// query string but paths inside the body as
// LibraryOptions.PathInfos[].Path, not as a "paths" query param at all.
// The "paths" query param (repeated, "paths=a&paths=b", how axios —
// @jellyfin/sdk's HTTP client — serializes an array param; or a single
// comma-delimited value) is kept as a fallback for direct callers (curl,
// this project's own tests) when the body is absent or has no PathInfos.
func (s *Server) handleAddVirtualFolder(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("name"))
	if name == "" {
		writeBadRequest(w)
		return
	}

	opts := libraryOptionsFromBody(r)
	paths := q["paths"]
	if len(paths) == 1 && strings.Contains(paths[0], ",") {
		paths = strings.Split(paths[0], ",")
	}
	if bodyPaths := pathInfosFromBody(r); len(bodyPaths) > 0 {
		paths = bodyPaths
	}

	lib := storage.Library{
		ID:             storage.LibraryID(randomID()),
		Name:           name,
		CollectionType: storage.CollectionType(q.Get("collectionType")),
		Paths:          paths,
		LibraryOptions: opts,
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

// libraryOptionsFromBody returns the LibraryOptions object of a create
// request, as sent (nil when the body has none), so options such as
// AutomaticallyAddToCollection take effect from the first scan.
func libraryOptionsFromBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		LibraryOptions json.RawMessage `json:"LibraryOptions"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.LibraryOptions) == 0 || string(body.LibraryOptions) == "null" {
		return nil
	}
	return body.LibraryOptions
}

// pathInfosFromBody extracts LibraryOptions.PathInfos[].Path from r's JSON
// body, if any. A missing/empty body, or one that doesn't decode, yields
// nil rather than an error — callers treat that as "fall back to the
// paths query param," which is how curl/tests without a body still work.
func pathInfosFromBody(r *http.Request) []string {
	if r.Body == nil {
		return nil
	}
	var body struct {
		LibraryOptions struct {
			PathInfos []struct {
				Path string `json:"Path"`
			} `json:"PathInfos"`
		} `json:"LibraryOptions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil
	}
	paths := make([]string, 0, len(body.LibraryOptions.PathInfos))
	for _, pi := range body.LibraryOptions.PathInfos {
		if pi.Path != "" {
			paths = append(paths, pi.Path)
		}
	}
	return paths
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

// handleSaveLibraryOptions is POST /Library/VirtualFolders/LibraryOptions —
// saves a library's LibraryOptions blob, addressed by Id (unlike
// DELETE/rename, which address by Name). The reference server's
// LibraryOptions has ~35 fields (metadata providers, trickplay, etc.) this
// project doesn't act on — see the package doc comment — so the blob is
// persisted opaquely (storage.Library.LibraryOptions) and simply echoed
// back by GET /Library/VirtualFolders rather than modeled field-by-field.
func (s *Server) handleSaveLibraryOptions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Id             string          `json:"Id"`
		LibraryOptions json.RawMessage `json:"LibraryOptions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInvalidBody(w)
		return
	}
	lib, err := s.store.GetLibrary(r.Context(), storage.LibraryID(body.Id))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	lib.LibraryOptions = body.LibraryOptions
	if err := s.store.UpsertLibrary(r.Context(), lib); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameVirtualFolder is POST /Library/VirtualFolders/Name — renames
// a library, addressed by its current name (?name=<current>&newName=<new>),
// same addressing scheme as DELETE /Library/VirtualFolders. Not confirmed
// live against the reference server (the dashboard itself warns renaming
// drops metadata, too destructive to test against a persistent
// test fixture) — route shape is from static analysis of @jellyfin/sdk's
// generated client, same sanctioned source as the rest of this file.
func (s *Server) handleRenameVirtualFolder(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("name")
	newName := strings.TrimSpace(q.Get("newName"))
	if newName == "" {
		writeBadRequest(w)
		return
	}

	libs, err := s.store.QueryLibraries(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for _, lib := range libs {
		if lib.Name != name {
			continue
		}
		lib.Name = newName
		if err := s.store.UpsertLibrary(r.Context(), lib); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if q.Get("refreshLibrary") == "true" {
			go s.runScan(lib)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// handleLibraryRefresh is POST /Library/Refresh — queues a background scan
// for every existing library (refreshAllLibraries), matching a dashboard's
// "Scan All Libraries" action.
func (s *Server) handleLibraryRefresh(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshAllLibraries(r.Context()); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refreshAllLibraries queues a background scan (Server.runScan) for every
// existing library. Shared by handleLibraryRefresh above and
// handleScheduledTaskStart (scheduled_tasks.go) — jellyfin-web's Dashboard
// "Scan All Libraries" button drives the latter (POST /ScheduledTasks/
// Running/{taskId}), not this package's own /Library/Refresh route, but
// both mean the same thing: scan everything now.
func (s *Server) refreshAllLibraries(ctx context.Context) error {
	libs, err := s.store.QueryLibraries(ctx)
	if err != nil {
		return err
	}
	for _, lib := range libs {
		go s.runScan(lib)
	}
	return nil
}
