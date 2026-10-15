// admin_system.go implements the dashboard's administration routes that
// have a real counterpart in Tinyfin: the log files (GET /System/Logs,
// /System/Logs/Log) and snapshot backups (/Backup/*), plus the plugin
// catalog routes, which answer with honest empty lists because Tinyfin has
// no plugin system (divergent by design, docs/contract/specs/admin.md).
package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// logDir is where cmd/tinyfin tees its log output (internal/logfile).
func (s *Server) logDir() string { return filepath.Join(s.cfg.DataDir, "log") }

func (s *Server) backupDir() string { return filepath.Join(s.cfg.DataDir, "backups") }

type logFileInfo struct {
	DateCreated  string `json:"DateCreated"`
	DateModified string `json:"DateModified"`
	Size         int64  `json:"Size"`
	Name         string `json:"Name"`
}

// handleSystemLogs is GET /System/Logs: the available log files, newest
// first (DateModified descending).
func (s *Server) handleSystemLogs(w http.ResponseWriter, r *http.Request) {
	out := []logFileInfo{}
	entries, _ := os.ReadDir(s.logDir())
	type fi struct {
		info logFileInfo
		mod  time.Time
	}
	var files []fi
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fi{logFileInfo{
			DateCreated:  formatWireTime(st.ModTime()),
			DateModified: formatWireTime(st.ModTime()),
			Size:         st.Size(),
			Name:         e.Name(),
		}, st.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files {
		out = append(out, f.info)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSystemLogFile is GET /System/Logs/Log?name=: the file as
// text/plain. A name that is not a plain file name in the log directory is a
// 404 with the bare string body the reference sends.
func (s *Server) handleSystemLogFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" || name != filepath.Base(name) || !strings.HasSuffix(name, ".log") {
		writeJSON(w, http.StatusNotFound, "Log file not found.")
		return
	}
	f, err := os.Open(filepath.Join(s.logDir(), name))
	if err != nil {
		writeJSON(w, http.StatusNotFound, "Log file not found.")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// ---- backups ----

type backupOptions struct {
	Metadata  bool `json:"Metadata"`
	Trickplay bool `json:"Trickplay"`
	Subtitles bool `json:"Subtitles"`
	Database  bool `json:"Database"`
}

type backupManifest struct {
	ServerVersion       string        `json:"ServerVersion"`
	BackupEngineVersion string        `json:"BackupEngineVersion"`
	DateCreated         string        `json:"DateCreated"`
	Path                string        `json:"Path"`
	Options             backupOptions `json:"Options"`
}

// snapshotter is implemented by the SQLite store (VACUUM INTO).
type snapshotter interface {
	SnapshotTo(ctx context.Context, path string) error
}

// handleBackupCreate is POST /Backup/Create: a consistent snapshot of the
// database written as a zip (manifest.json + database/tinyfin.db) under
// <data>/backups. Only the database exists to be saved: Metadata, Trickplay
// and Subtitles are echoed in Options but have no effect, and Database is
// always true.
func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.store.(snapshotter)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	var opts backupOptions
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil && err != io.EOF {
			writeBadRequest(w)
			return
		}
	}
	opts.Database = true
	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	zipPath := filepath.Join(s.backupDir(), "tinyfin-backup-"+now.Format("20060102150405")+".zip")
	tmpDB := zipPath + ".db"
	defer os.Remove(tmpDB)
	if err := snap.SnapshotTo(r.Context(), tmpDB); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	man := backupManifest{
		ServerVersion: Version, BackupEngineVersion: "0.1.0",
		DateCreated: formatWireTime(now), Path: zipPath, Options: opts,
	}
	if err := writeBackupZip(zipPath, tmpDB, man); err != nil {
		os.Remove(zipPath)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, man)
}

func writeBackupZip(zipPath, dbPath string, man backupManifest) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(mw).Encode(man); err != nil {
		return err
	}
	dw, err := zw.Create("database/tinyfin.db")
	if err != nil {
		return err
	}
	in, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := io.Copy(dw, in); err != nil {
		return err
	}
	return zw.Close()
}

func readBackupManifest(path string) (backupManifest, bool) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return backupManifest{}, false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return backupManifest{}, false
		}
		defer rc.Close()
		var m backupManifest
		if json.NewDecoder(rc).Decode(&m) != nil {
			return backupManifest{}, false
		}
		m.Path = path
		return m, true
	}
	return backupManifest{}, false
}

// handleBackupList is GET /Backup: the manifests of the archives found.
func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	out := []backupManifest{}
	entries, _ := os.ReadDir(s.backupDir())
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		if m, ok := readBackupManifest(filepath.Join(s.backupDir(), e.Name())); ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DateCreated < out[j].DateCreated })
	writeJSON(w, http.StatusOK, out)
}

// handleBackupManifest is GET /Backup/Manifest?path=: the archive is
// addressed by its file name (a full path is reduced to its base name).
func (s *Server) handleBackupManifest(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("path"))
	if name == "." || name == "/" || !strings.HasSuffix(name, ".zip") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	m, ok := readBackupManifest(filepath.Join(s.backupDir(), name))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleBackupRestore is POST /Backup/Restore. Restoring replaces the live
// database under a running server, which Tinyfin does not do; the route
// validates like the reference (400 without ArchiveFileName, 404 for an
// unknown archive) and otherwise answers 501.
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ArchiveFileName *string `json:"ArchiveFileName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ArchiveFileName == nil {
		writeValidationErrors(w, map[string][]string{"archiveRestoreDto": {"A value for the 'archiveRestoreDto' parameter or property was not provided."}})
		return
	}
	name := filepath.Base(*body.ArchiveFileName)
	if _, err := os.Stat(filepath.Join(s.backupDir(), name)); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNotImplemented)
}

// ---- plugins (no plugin system: empty, compatible shapes) ----

func (s *Server) handleEmptyList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Server) handleNotFoundAdmin(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
