// playback.go implements the Direct-Play-only slice of docs/04's
// "playback/streaming" group: PlaybackInfo negotiation (docs/07's
// decision, narrowed to Direct Play per ROADMAP.md Phase 1) and the actual
// media-file byte stream. Remux/Direct Stream/Transcode, HLS, media
// segments, trickplay, and playstate reporting are explicitly out of scope
// here — later increments, not silently expanded into.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/playback"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// mediaSourceInfoDto mirrors the OpenAPI schema's MediaSourceInfo, trimmed
// to what a Direct-Play-only response needs. There is no "stream URL"
// field on the real schema at all (checked against the OpenAPI
// MediaSourceInfo properties) — a real client builds GET
// /Videos/{itemId}/stream(.{container}) itself from Id + Container, it
// isn't handed a ready-made URL. That's why /Videos/{itemId}/stream exists
// as its own route (below) rather than being referenced by a field here.
type mediaSourceInfoDto struct {
	ID                   string           `json:"Id"`
	Protocol             string           `json:"Protocol"`
	Path                 string           `json:"Path,omitempty"`
	Type                 string           `json:"Type"`
	Container            string           `json:"Container,omitempty"`
	Size                 int64            `json:"Size,omitempty"`
	Name                 string           `json:"Name,omitempty"`
	IsRemote             bool             `json:"IsRemote"`
	RunTimeTicks         *int64           `json:"RunTimeTicks,omitempty"`
	SupportsTranscoding  bool             `json:"SupportsTranscoding"`
	SupportsDirectStream bool             `json:"SupportsDirectStream"`
	SupportsDirectPlay   bool             `json:"SupportsDirectPlay"`
	RequiresOpening      bool             `json:"RequiresOpening"`
	RequiresClosing      bool             `json:"RequiresClosing"`
	SupportsProbing      bool             `json:"SupportsProbing"`
	MediaStreams         []mediaStreamDto `json:"MediaStreams,omitempty"`
}

// playbackInfoResponse mirrors the OpenAPI schema's PlaybackInfoResponse.
// PlaySessionId is a fresh opaque id per request — Tinyfin has no
// session/realtime state tracking a play session's lifecycle yet (docs/06
// is out of scope here), so this is just an identifier a client can echo
// back, not backed by any server-side session record.
type playbackInfoResponse struct {
	MediaSources  []mediaSourceInfoDto `json:"MediaSources"`
	PlaySessionID string               `json:"PlaySessionId"`
	ErrorCode     string               `json:"ErrorCode,omitempty"`
}

// playbackInfoRequestBody mirrors the OpenAPI schema's PlaybackInfoDto
// request body, trimmed to the one field this slice's decision actually
// consumes. Every other field on the real schema (EnableDirectPlay,
// MaxStreamingBitrate, AudioStreamIndex, ...) is a transcode-negotiation
// knob outside this slice's scope (no transcode worker exists yet).
type playbackInfoRequestBody struct {
	DeviceProfile *deviceprofile.DeviceProfile `json:"DeviceProfile"`
}

// resolveDeviceProfile implements docs/07 O2's negotiation order: a
// client-submitted inline profile (POST body) is authoritative whenever
// present; otherwise fall back to the built-in registry keyed by the
// Client string off X-Emby-Authorization; otherwise the conservative
// Default().
func (s *Server) resolveDeviceProfile(r *http.Request) deviceprofile.DeviceProfile {
	if r.Method == http.MethodPost {
		var body playbackInfoRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.DeviceProfile != nil {
			return *body.DeviceProfile
		}
	}

	if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
		if p, found := deviceprofile.Lookup(ac.Client); found {
			return p
		}
	}

	return deviceprofile.Default()
}

// handlePlaybackInfo serves both GET and POST /Items/{itemId}/PlaybackInfo.
// The GET form has no request body to carry a profile, so it always falls
// through to the registry/default lookup; the POST form is how a real
// client normally negotiates (docs/04).
func (s *Server) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	itemID := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	profile := s.resolveDeviceProfile(r)
	decision := playback.DecideDirectPlay(item, profile)

	resp := playbackInfoResponse{PlaySessionID: randomID()}
	if decision.DirectPlaySupported {
		resp.MediaSources = []mediaSourceInfoDto{{
			ID:                   string(item.ID),
			Protocol:             "File",
			Path:                 item.Path,
			Type:                 "Default",
			Container:            decision.Container,
			Size:                 item.Size,
			Name:                 item.Name,
			IsRemote:             false,
			RunTimeTicks:         item.RunTimeTicks,
			SupportsDirectPlay:   true,
			SupportsDirectStream: false,
			SupportsTranscoding:  false,
			RequiresOpening:      false,
			RequiresClosing:      false,
			SupportsProbing:      true,
			MediaStreams:         mediaStreamDtos(item.MediaStreams),
		}}
	} else {
		// No transcode worker exists yet (out of scope — see the package
		// doc comment), so an incompatible source can't be offered any
		// playable MediaSource at all. ErrorCode: NoCompatibleStream
		// matches the reference server's own PlaybackErrorCode for this
		// situation rather than inventing a new one, and MediaSources
		// stays empty rather than pointing the client at a stream URL
		// that wouldn't actually work.
		resp.ErrorCode = "NoCompatibleStream"
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleVideoStream serves GET /Videos/{itemId}/stream and
// /Videos/{itemId}/stream.{container} — the Direct Play data path. Both
// forms are registered as a single "GET /Videos/{itemId}/{file}" mux
// pattern (server.go): http.ServeMux wildcards must occupy a whole path
// segment, so "stream.{container}" can't be its own pattern — this handler
// validates the {file} segment itself instead of relying on the router to
// split "stream" from ".{container}".
//
// No re-encoding, no ffmpeg subprocess (that's the transcode worker's job,
// docs/07, out of scope here): just the file bytes, via http.ServeFile so
// Range/conditional requests and HEAD are handled correctly for seeking.
//
// Path-traversal trust boundary: item.Path comes from storage.Item, set by
// whatever wrote that row (the future scanner, docs/08) — never built by
// concatenating any request-supplied path fragment (itemId is only ever
// used as an opaque lookup key into storage.ItemStore, and {file} is only
// ever compared against a literal prefix, never joined into a filesystem
// path). This is the same trust boundary the rest of this codebase already
// relies on for Item.Path/ImageRef.Path; it does not need its own new
// sandboxing layer to stay consistent with that.
func (s *Server) handleVideoStream(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if file != "stream" && !strings.HasPrefix(file, "stream.") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	itemID := storage.ItemID(r.PathValue("itemId"))
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if item.Path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, item.Path)
}
