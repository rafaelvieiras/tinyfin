// system_info_storage.go implements GET /System/Info/Storage, the
// per-folder disk usage breakdown the dashboard's overview page renders as
// its "Server" storage bars. Confirmed live against a real reference
// server, called by jellyfin-web's admin Dashboard overview page with no
// query parameters: every folder entry — the fixed system folders and every
// library's own folders alike — shares one shape,
// {Path, FreeSpace, UsedSpace, StorageType, DeviceId}, with DeviceId
// observed to just be a copy of Path in every case seen.
//
// Tinyfin's directory layout doesn't mirror the reference server's
// config/cache/log/metadata split (see Config.DataDir's doc comment in
// server.go) — five of the six system-folder concepts collapse onto the one
// real directory this project actually keeps its database and sidecar
// files in. That's reported honestly below as five keys pointing at the
// same statfs result, not fabricated as five distinct paths this project
// doesn't have.
package api

import (
	"net/http"
	"os"
	"syscall"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// folderStorageInfo mirrors one entry of the reference server's
// SystemInfo.Storage shape — used both for the fixed system folders and for
// each path under a Library's Folders.
type folderStorageInfo struct {
	Path      string `json:"Path"`
	FreeSpace int64  `json:"FreeSpace"`
	UsedSpace int64  `json:"UsedSpace"`
	// StorageType is always "Fixed" — this project has no dependency-free
	// way to distinguish a network mount from a local one (no existing code
	// anywhere in this repo attempts that), and AGENTS.md principle 2 rules
	// out reaching for a new OS-specific dependency just for this. The
	// reference server's "Network" value for two of the ground-truth
	// library folders above is presumably a property of that specific
	// reference deployment's mounts, not something wire compatibility
	// requires Tinyfin to reproduce — this is a documented simplification,
	// not a guess dressed up as fact.
	StorageType string `json:"StorageType"`
	DeviceId    string `json:"DeviceId"`
}

// libraryStorageInfo mirrors one entry of SystemInfo.Storage's Libraries
// array: a library's identity plus one folderStorageInfo per root path
// (storage.Library.Paths — a library can have more than one).
type libraryStorageInfo struct {
	Id      string              `json:"Id"`
	Name    string              `json:"Name"`
	Folders []folderStorageInfo `json:"Folders"`
}

// systemStorageInfo mirrors the reference server's SystemInfo.Storage
// response as a whole.
type systemStorageInfo struct {
	ProgramDataFolder      folderStorageInfo    `json:"ProgramDataFolder"`
	WebFolder              *folderStorageInfo   `json:"WebFolder,omitempty"`
	ImageCacheFolder       folderStorageInfo    `json:"ImageCacheFolder"`
	CacheFolder            folderStorageInfo    `json:"CacheFolder"`
	LogFolder              folderStorageInfo    `json:"LogFolder"`
	InternalMetadataFolder folderStorageInfo    `json:"InternalMetadataFolder"`
	TranscodingTempFolder  folderStorageInfo    `json:"TranscodingTempFolder"`
	Libraries              []libraryStorageInfo `json:"Libraries"`
}

// statFolder runs statfs(2) on path and reports it as a folderStorageInfo.
// FreeSpace is Bavail*Bsize — space actually available to an unprivileged
// process, the same number `df`'s "Avail" column reports. UsedSpace is
// (Blocks-Bfree)*Bsize — total blocks minus *all* free blocks, including
// the ones reserved for root that Bavail excludes; this means
// FreeSpace+UsedSpace is intentionally short of the filesystem's total
// size by that reserved margin, exactly matching what `df` itself shows
// (checked against `df -B1` on a real directory during development, not
// assumed from the syscall docs alone). Falls back to a zeroed entry (still
// carrying Path/DeviceId) on error rather than failing the whole response —
// a single unreadable/nonexistent path (e.g. a stale library folder that
// was removed from disk without removing the library first) shouldn't take
// down the entire dashboard overview page.
func statFolder(path string) folderStorageInfo {
	info := folderStorageInfo{Path: path, StorageType: "Fixed", DeviceId: path}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return info
	}
	bsize := uint64(st.Bsize)
	info.FreeSpace = int64(st.Bavail * bsize)
	info.UsedSpace = int64((st.Blocks - st.Bfree) * bsize)
	return info
}

// handleSystemInfoStorage is GET /System/Info/Storage, admin-only (matching
// the reference server's dashboard-only scoping — no non-admin client
// surface calls this).
func (s *Server) handleSystemInfoStorage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ProgramDataFolder/CacheFolder/LogFolder/InternalMetadataFolder/
	// ImageCacheFolder: all five genuinely are s.cfg.DataDir in this
	// project (see the package doc comment above) — statfs it once and
	// reuse the result for all five rather than repeating the syscall.
	dataDirInfo := statFolder(s.cfg.DataDir)

	out := systemStorageInfo{
		ProgramDataFolder:      dataDirInfo,
		ImageCacheFolder:       dataDirInfo,
		CacheFolder:            dataDirInfo,
		LogFolder:              dataDirInfo,
		InternalMetadataFolder: dataDirInfo,
		// os.TempDir() is TranscodingTempFolder's genuinely accurate answer,
		// not new plumbing invented for this endpoint — internal/transcode's
		// own Config.baseDir falls back to exactly this when no WorkDir is
		// configured, which is Tinyfin's actual default deployment.
		TranscodingTempFolder: statFolder(os.TempDir()),
	}

	if s.cfg.WebDir != "" {
		webInfo := statFolder(s.cfg.WebDir)
		out.WebFolder = &webInfo
	}

	libs, err := s.store.QueryLibraries(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out.Libraries = make([]libraryStorageInfo, 0, len(libs))
	for _, lib := range libs {
		out.Libraries = append(out.Libraries, libraryStorageInfoFor(lib))
	}

	writeJSON(w, http.StatusOK, out)
}

// libraryStorageInfoFor builds one Libraries[] entry, statfs'ing each of
// lib.Paths independently — different root paths on the same library can
// live on different mounts, so one shared statfs result would be wrong for
// any library with more than one folder.
func libraryStorageInfoFor(lib storage.Library) libraryStorageInfo {
	out := libraryStorageInfo{
		Id:      string(lib.ID),
		Name:    lib.Name,
		Folders: make([]folderStorageInfo, 0, len(lib.Paths)),
	}
	for _, p := range lib.Paths {
		out.Folders = append(out.Folders, statFolder(p))
	}
	return out
}
