package api

import (
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// mayDeleteContent reports whether the caller's policy allows deleting item's
// media: EnableContentDeletion (any item), or the item's library listed in
// EnableContentDeletionFromFolders. There is no administrator bypass, and an
// API key (no user) never qualifies.
func mayDeleteContent(ac *AuthContext, item storage.Item) bool {
	if ac == nil || !ac.Authenticated || ac.IsAPIKey {
		return false
	}
	p := storedUserPolicy(ac.User)
	if p.EnableContentDeletion {
		return true
	}
	for _, id := range p.EnableContentDeletionFromFolders {
		if strings.EqualFold(id, string(item.LibraryID)) {
			return true
		}
	}
	return false
}

// mayDownloadContent reports whether the caller's policy allows downloading.
func mayDownloadContent(ac *AuthContext) bool {
	if ac == nil || !ac.Authenticated || ac.IsAPIKey {
		return false
	}
	return storedUserPolicy(ac.User).EnableContentDownloading
}

// mayTranscodeVideo reports whether PlaybackInfo may offer a stream other than
// Direct Play for a video: any of EnableVideoPlaybackTranscoding,
// EnableAudioPlaybackTranscoding and EnablePlaybackRemuxing is enough (the
// reference only refuses when all three are off). Callers without a user are
// not restricted.
func mayTranscodeVideo(ac *AuthContext) bool {
	if ac == nil || !ac.Authenticated || ac.IsAPIKey {
		return true
	}
	p := storedUserPolicy(ac.User)
	return p.EnableVideoPlaybackTranscoding || p.EnableAudioPlaybackTranscoding || p.EnablePlaybackRemuxing
}

// mayTranscodeAudio reports whether PlaybackInfo may offer a transcode of a
// track: EnableAudioPlaybackTranscoding alone decides.
func mayTranscodeAudio(ac *AuthContext) bool {
	if ac == nil || !ac.Authenticated || ac.IsAPIKey {
		return true
	}
	return storedUserPolicy(ac.User).EnableAudioPlaybackTranscoding
}

// handleItemDownload is GET /Items/{itemId}/Download: the original file as an
// attachment, with range support. Policy: 401 for an API key, 403 for a user
// without EnableContentDownloading (400 for an administrator without it, as
// the reference answers), 400 for an item that is not a single file.
func (s *Server) handleItemDownload(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("itemId")
	norm, valid := normalizeGUID(raw)
	if !valid {
		writeInvalidID(w, "itemId", raw)
		return
	}
	ac, _ := AuthFromContext(r.Context())
	if ac == nil || ac.IsAPIKey {
		writeUnauthorizedAccess(w)
		return
	}
	item, err := s.store.GetItem(r.Context(), storage.ItemID(norm))
	if err != nil {
		// A library root is a folder, not an item row: not downloadable.
		if libs, lerr := s.store.QueryLibraries(r.Context()); lerr == nil {
			for _, l := range libs {
				if strings.EqualFold(string(l.ID), norm) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !s.requireReadable(w, r, item) {
		return
	}
	if !mayDownloadContent(ac) {
		if ac.User.IsAdministrator {
			w.WriteHeader(http.StatusBadRequest)
		} else {
			w.WriteHeader(http.StatusForbidden)
		}
		return
	}
	if !item.HasMediaFile() || isFolderType(item.Type) || item.RemoteURL != "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f, err := os.Open(item.Path)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	name := filepath.Base(item.Path)
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"; filename*=UTF-8''"+
		strings.ReplaceAll(url.PathEscape(name), "(", "%28"))
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// playbackFlags are the three transcode permissions of a user. A caller
// without a user (API key) is not restricted.
type playbackFlags struct{ video, remux, audio bool }

func playbackFlagsFor(ac *AuthContext) playbackFlags {
	if ac == nil || !ac.Authenticated || ac.IsAPIKey {
		return playbackFlags{true, true, true}
	}
	p := storedUserPolicy(ac.User)
	return playbackFlags{p.EnableVideoPlaybackTranscoding, p.EnablePlaybackRemuxing, p.EnableAudioPlaybackTranscoding}
}

// remoteBitrateCap applies the caller's RemoteClientBitrateLimit to the
// bitrate the client asked for (0 = no limit asked): a remote caller never
// gets more than the limit (policy.remote.R1). Local callers, API keys and a
// limit of 0 leave it alone.
func (s *Server) remoteBitrateCap(r *http.Request, requested int64) int64 {
	ac, _ := AuthFromContext(r.Context())
	if ac == nil || !ac.Authenticated || ac.IsAPIKey || s.fromLocalNetwork(r) {
		return requested
	}
	limit := int64(storedUserPolicy(ac.User).RemoteClientBitrateLimit)
	if limit > 0 && (requested <= 0 || limit < requested) {
		return limit
	}
	return requested
}

// requestedMaxBitrate is the bitrate ceiling a PlaybackInfo request states:
// the body member, else the query parameter, else the device profile's.
func requestedMaxBitrate(r *http.Request, body playbackInfoRequestBody, profile deviceprofile.DeviceProfile) int64 {
	if body.MaxStreamingBitrate != nil {
		return *body.MaxStreamingBitrate
	}
	if n, err := strconv.ParseInt(caseInsensitiveQuery(r).Get("maxstreamingbitrate"), 10, 64); err == nil {
		return n
	}
	return profile.MaxStreamingBitrate
}

// allowedByRequest reads a boolean the PlaybackInfo request may carry in its
// body or its query (the body first) and that defaults to true.
func allowedByRequest(r *http.Request, query string, body *bool) bool {
	if body != nil {
		return *body
	}
	if v, err := strconv.ParseBool(caseInsensitiveQuery(r).Get(query)); err == nil {
		return v
	}
	return true
}
