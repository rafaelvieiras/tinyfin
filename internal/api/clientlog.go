package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// clientLogBodyLimit is the largest document this server accepts, in bytes.
// Measured on the reference: 1,000,000 bytes are taken (200) and 1,000,001 are
// refused (413) with clientLogTooLarge as the body — where the message says
// "less than 1,000,000" but the check lets exactly 1,000,000 through.
const clientLogBodyLimit = 1_000_000

// clientLogTooLarge is the reference's own wording, sent as a JSON string.
const clientLogTooLarge = "Payload must be less than 1,000,000 bytes"

// clientLogKeep is how many uploaded documents this server holds before the
// oldest are deleted. The reference keeps every one of them (measured: two
// uploads, two files, nothing pruned), which lets any authenticated client put
// a megabyte at a time into the data directory. Thirty-two documents is more
// than the crash reports a server will ever need to read (DIV-41).
const clientLogKeep = 32

// clientLogResponse mirrors the reference's answer: the name of the file the
// document was stored as, which is also what a user opens in the dashboard's
// log list.
type clientLogResponse struct {
	FileName string `json:"FileName"`
}

// handleClientLogDocument is POST /ClientLog/Document: an authenticated client
// uploads its own log after something went wrong there, and the server keeps it
// as a file beside its own logs (GET /System/Logs lists it).
//
// The Android TV client posts this right after it crashes — measured in a
// production log, where the route answered 404 and the crash's stack trace went
// with it, which is why the route exists here.
func (s *Server) handleClientLogDocument(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, clientLogBodyLimit+1))
	if err != nil {
		http.Error(w, "cannot read the document", http.StatusBadRequest)
		return
	}
	if len(body) > clientLogBodyLimit {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(clientLogTooLarge)
		return
	}

	// The name carries who uploaded it, the way the reference's does: a user
	// reading the log list can tell a crash report from a server log by its
	// name alone. An API key carries no client or version, and the reference
	// writes "apikey" for both.
	client, version := "apikey", "apikey"
	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		if v := clientLogNamePart(ac.Client); v != "" {
			client = v
		}
		if v := clientLogNamePart(ac.Version); v != "" {
			version = v
		}
	}

	name := "upload_" + client + "_" + version + "_" + time.Now().Format("20060102150405") + "_" + clientLogSuffix() + ".log"
	dir := s.logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "cannot store the document", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		http.Error(w, "cannot store the document", http.StatusInternalServerError)
		return
	}
	pruneClientLogs(dir, clientLogKeep)

	writeJSON(w, http.StatusOK, clientLogResponse{FileName: name})
}

// clientLogNamePart makes a name safe to put into a file name. The value comes
// from the client's Authorization header (Client=, Version=), so a client can
// choose it: anything but a letter, digit, dash, underscore or dot becomes an
// underscore, so no value can name a directory, climb out with "..", or start
// the name with a dot and disappear from the list.
func clientLogNamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_.")
	if len(out) > 40 {
		out = out[:40]
	}
	return strings.Trim(out, "_.")
}

// clientLogSuffix is the 32 hex characters the reference puts before ".log".
func clientLogSuffix() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A crash report is worth keeping even without a random name; the
		// timestamp in the name already makes a collision unlikely, and this
		// branch cannot be reached on a working system.
		return strings.Repeat("0", 32)
	}
	return hex.EncodeToString(b[:])
}

// pruneClientLogs deletes the oldest uploads so a client cannot fill the data
// directory one document at a time. Only files this server named are touched:
// server logs and the crash reports of other names stay.
func pruneClientLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type upload struct {
		name string
		mod  time.Time
	}
	var uploads []upload
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "upload_") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		uploads = append(uploads, upload{name: e.Name(), mod: info.ModTime()})
	}
	if len(uploads) <= keep {
		return
	}
	sort.Slice(uploads, func(i, j int) bool { return uploads[i].mod.After(uploads[j].mod) })
	for _, u := range uploads[keep:] {
		_ = os.Remove(filepath.Join(dir, u.name))
	}
}
