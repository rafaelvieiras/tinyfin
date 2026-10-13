// environment.go implements the wire-compatible Environment/* endpoints the
// dashboard's library-folder browser (the "Add"/folder-picker dialog inside
// "Add Media Library") calls to let an admin navigate the server's real
// filesystem instead of typing a path from memory. Route shapes and
// response field names were confirmed against a running reference server
// (the reference server — AGENTS.md's clean-room requirement) and
// cross-checked against the embedded jellyfin-apiclient bundle inside a
// real jellyfin-web build (query param spelling: lowercase "path", camelCase
// "includeDirectories"/"includeFiles"; ValidatePath's body field is spelled
// "ValidateWriteable", not "ValidateWritable").
//
// Unlike the rest of this package, these handlers read the real OS
// filesystem the Tinyfin process itself runs on (os.ReadDir/os.Stat), the
// same primitives internal/scan uses to walk library paths — there's no
// storage-layer concept of "directory" to query instead.
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// fileSystemEntryInfo mirrors the reference server's FileSystemEntryInfo,
// confirmed live: {"Name":"...","Path":"...","Type":"Directory"|"File"}.
type fileSystemEntryInfo struct {
	Name string `json:"Name"`
	Path string `json:"Path"`
	Type string `json:"Type"`
}

// handleDefaultDirectoryBrowser is GET /Environment/DefaultDirectoryBrowser.
// Confirmed live against the reference server: a bare {} with no Path
// field at all (jellyfin-web's own client reads it as `e.Path||""`, so it
// tolerates the field being absent) — Tinyfin has no meaningful "last used"
// or "most likely" starting path to suggest either, so this mirrors that
// exactly rather than guessing "/" or some other default.
func (s *Server) handleDefaultDirectoryBrowser(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Path string `json:"Path,omitempty"`
	}{})
}

// handleEnvironmentDrives is GET /Environment/Drives. The reference server
// (Windows) returns one entry per drive letter plus a few container mount
// points; on a single-root Linux/container filesystem the only meaningful
// entry is "/" itself.
func (s *Server) handleEnvironmentDrives(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []fileSystemEntryInfo{{Name: "/", Path: "/", Type: "Directory"}})
}

// handleDirectoryContents is GET /Environment/DirectoryContents. Confirmed
// live: with neither includeDirectories nor includeFiles set to "true", the
// reference server returns an empty list rather than everything — each flag
// opts its kind in, they aren't a "both if unset" default.
func (s *Server) handleDirectoryContents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path := q.Get("path")
	if path == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	includeDirectories := q.Get("includeDirectories") == "true"
	includeFiles := q.Get("includeFiles") == "true"

	// os.ReadDir already returns entries sorted by filename, matching the
	// ordering observed from the reference server — no extra sort needed.
	entries, err := os.ReadDir(path)
	if err != nil {
		// Confirmed live: a nonexistent/inaccessible path 404s rather than
		// 400s, whether it doesn't exist or isn't a directory at all.
		w.WriteHeader(http.StatusNotFound)
		return
	}

	out := make([]fileSystemEntryInfo, 0, len(entries))
	for _, e := range entries {
		isDir := e.IsDir()
		if isDir && !includeDirectories {
			continue
		}
		if !isDir && !includeFiles {
			continue
		}
		typ := "File"
		if isDir {
			typ = "Directory"
		}
		out = append(out, fileSystemEntryInfo{
			Name: e.Name(),
			Path: filepath.Join(path, e.Name()),
			Type: typ,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleParentPath is GET /Environment/ParentPath. Confirmed live: the
// response body is a bare JSON string (e.g. "/media"), not an object — and
// the filesystem root has no parent, so that case returns JSON null rather
// than "/" (querying ParentPath of "/" against the reference server came
// back null). filepath.Dir alone can't express that distinction (Dir("/")
// is "/", not "no parent"), hence the explicit root check below.
func (s *Server) handleParentPath(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	clean := filepath.Clean(path)
	var parent *string
	if clean != string(filepath.Separator) {
		p := filepath.Dir(clean)
		parent = &p
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(parent) // nil *string encodes as JSON null
}

// validatePathRequest mirrors the request body the reference server's own
// dashboard sends (confirmed against the embedded jellyfin-apiclient
// bundle): {"Path":"...","ValidateWriteable":false}. ValidateWriteable is
// accepted for wire compatibility but not acted on — Tinyfin has no
// writability-specific check to run (docs' "minimal over guessed"
// convention), so a path that exists is treated as valid regardless of
// this flag.
type validatePathRequest struct {
	Path              string `json:"Path"`
	ValidateWriteable bool   `json:"ValidateWriteable"`
}

// handleValidatePath is POST /Environment/ValidatePath. Confirmed live
// against the reference server on both a directory (/etc) and a plain file
// (/etc/hostname): both came back 204, so this checks mere existence via
// os.Stat, not os.Stat().IsDir() — a directory-only check would reject
// paths (e.g. a TLS certificate file, which jellyfin-web's own certificate
// path picker also drives through this same endpoint) the real server
// accepts.
func (s *Server) handleValidatePath(w http.ResponseWriter, r *http.Request) {
	var body validatePathRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(body.Path); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
