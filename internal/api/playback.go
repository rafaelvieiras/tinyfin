// playback.go implements docs/04's "playback/streaming" group: PlaybackInfo
// negotiation across the full Direct Play/Remux/Direct Stream/Transcode
// ladder (docs/07's decision — internal/playback.Decide) and the two data
// paths that follow from it: the raw Direct Play byte stream (this file)
// and the HLS routes the other three modes hand off to (hls.go). Trickplay
// is still explicitly out of scope — a later increment, not silently
// expanded into. Media segments and playstate reporting (resume position,
// watched status) used to be listed here too, but turned out to be
// load-bearing for the native player rather than deferrable — see
// sessions_playback.go.
package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/playback"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// mediaSourceInfoDto mirrors the OpenAPI schema's MediaSourceInfo. There is
// no "stream URL" field for Direct Play on the real schema at all (checked
// against the OpenAPI MediaSourceInfo properties) — a real client builds
// GET /Videos/{itemId}/stream(.{container}) itself from Id + Container, it
// isn't handed a ready-made URL. TranscodingUrl is exactly that "ready-made
// URL" for the other three modes, though (docs/07's Remux/Direct
// Stream/Transcode) — see handlePlaybackInfo.
//
// ReadAtNativeFramerate through HasSegments are NOT omitempty: found
// missing by reading jellyfin-sdk-kotlin's MediaSourceInfo.kt directly
// (not just testing) after the native Android player's PlaybackInfo call
// kept silently failing to resolve a media source with no crash and no
// server-visible error — kotlinx.serialization requires these keys
// present (they have no default), same failure class as
// DisplayPreferencesDto's ScrollDirection/SortOrder below, but this one is
// on the very first call the native player makes, so it blocks Direct
// Play for every item unconditionally.
type mediaSourceInfoDto struct {
	ID                    string `json:"Id"`
	Protocol              string `json:"Protocol"`
	Path                  string `json:"Path,omitempty"`
	Type                  string `json:"Type"`
	Container             string `json:"Container,omitempty"`
	Size                  int64  `json:"Size,omitempty"`
	Name                  string `json:"Name,omitempty"`
	IsRemote              bool   `json:"IsRemote"`
	RunTimeTicks          *int64 `json:"RunTimeTicks,omitempty"`
	ReadAtNativeFramerate bool   `json:"ReadAtNativeFramerate"`
	IgnoreDts             bool   `json:"IgnoreDts"`
	IgnoreIndex           bool   `json:"IgnoreIndex"`
	GenPtsInput           bool   `json:"GenPtsInput"`
	SupportsTranscoding   bool   `json:"SupportsTranscoding"`
	SupportsDirectStream  bool   `json:"SupportsDirectStream"`
	SupportsDirectPlay    bool   `json:"SupportsDirectPlay"`
	IsInfiniteStream      bool   `json:"IsInfiniteStream"`
	RequiresOpening       bool   `json:"RequiresOpening"`
	RequiresClosing       bool   `json:"RequiresClosing"`
	RequiresLooping       bool   `json:"RequiresLooping"`
	SupportsProbing       bool   `json:"SupportsProbing"`
	// TranscodingUrl/TranscodingContainer are set only for Remux/Direct
	// Stream/Transcode (docs/07) — empty (omitted) for Direct Play, which
	// needs no transcode-worker involvement at all.
	TranscodingUrl       string `json:"TranscodingUrl,omitempty"`
	TranscodingContainer string `json:"TranscodingContainer,omitempty"`
	// TranscodingSubProtocol is a required enum with no "none" member even
	// for Direct Play, where nothing is actually transcoding — "http" is
	// the value the reference server itself reports for a Direct Play
	// source; "hls" is reported for the other three modes, matching
	// TranscodingUrl's own protocol (hls.go serves HLS exclusively — no
	// progressive-remux "http" transcoding path exists in this project).
	TranscodingSubProtocol string           `json:"TranscodingSubProtocol"`
	HasSegments            bool             `json:"HasSegments"`
	MediaStreams           []mediaStreamDto `json:"MediaStreams,omitempty"`
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

// baseMediaSourceInfo builds the fields of a mediaSourceInfoDto that don't
// depend on any device-profile transcode negotiation — the plain "what file
// is this" description shared by handlePlaybackInfo (which layers its own
// Mode-driven fields, e.g. SupportsDirectPlay/TranscodingUrl, on top per
// decision.Mode below) and dto.go's mediaSourcesFor (baseItemDto.MediaSources
// on GET /Items/{itemId}, which has no device profile to negotiate against
// at all — see that function's doc comment). container is passed in rather
// than read off item directly since handlePlaybackInfo's caller wants
// decision.Container (a possibly profile-transcoded value) while
// mediaSourcesFor wants the item's own on-disk container.
func baseMediaSourceInfo(item storage.Item, container string) mediaSourceInfoDto {
	return mediaSourceInfoDto{
		ID:              string(item.ID),
		Protocol:        "File",
		Path:            item.Path,
		Type:            "Default",
		Container:       container,
		Size:            item.Size,
		Name:            item.Name,
		RunTimeTicks:    item.RunTimeTicks,
		SupportsProbing: true,
		MediaStreams:    mediaStreamDtos(item.MediaStreams),
	}
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
	decision := playback.Decide(item, profile)
	resp := playbackInfoResponse{PlaySessionID: randomID()}

	source := baseMediaSourceInfo(item, decision.Container)

	switch decision.Mode {
	case playback.ModeDirectPlay:
		source.SupportsDirectPlay = true
		source.TranscodingSubProtocol = "http"
		resp.MediaSources = []mediaSourceInfoDto{source}

	case playback.ModeRemux, playback.ModeDirectStream, playback.ModeTranscode:
		// All three hand off to the transcode worker over HLS (docs/07's
		// "Execution" section, hls.go) — Tinyfin has no separate
		// progressive-remux byte-stream path, only the HLS one
		// internal/transcode already produces, so Remux/Direct Stream
		// reuse it too instead of needing their own serving mechanism.
		source.SupportsDirectStream = decision.Mode != playback.ModeTranscode
		source.SupportsTranscoding = true
		var token string
		if ac, ok := AuthFromContext(r.Context()); ok && ac != nil {
			token = ac.Token
		}
		source.TranscodingUrl = hlsMasterURL(item.ID, resp.PlaySessionID, token)
		source.TranscodingContainer = decision.Container
		source.TranscodingSubProtocol = "hls"
		resp.MediaSources = []mediaSourceInfoDto{source}

	default: // playback.ModeNone
		// Neither Direct Play nor any transcode fallback applies (the
		// resolved profile has no usable TranscodingProfiles entry — see
		// deviceprofile.DeviceProfile's doc comment). ErrorCode:
		// NoCompatibleStream matches the reference server's own
		// PlaybackErrorCode for this situation rather than inventing a new
		// one, and MediaSources stays empty rather than pointing the
		// client at a stream URL that wouldn't actually work.
		resp.ErrorCode = "NoCompatibleStream"
	}

	writeJSON(w, http.StatusOK, resp)
}

// hlsMasterURL builds the TranscodingUrl a client should fetch to begin
// consuming the HLS stream for itemID (handleHLSMaster, hls.go), mirroring
// the reference server's own master-playlist route shape (confirmed
// against the published @jellyfin/sdk client per docs/04's
// secondary-verification allowance — the OpenAPI schema itself omits the
// DynamicHls routes).
//
// apiKey is embedded as a query param for the same reason
// withAuthContext's query-string fallback exists (auth.go): a browser's
// hls.js resolves this URL directly (no ApiClient, no custom headers), so
// without a token in the URL itself every follow-up request — main.m3u8,
// then each segment — 401s. Confirmed by testing an actual transcode
// through jellyfin-web in a real browser: the very first master.m3u8
// fetch came back 401 until this was added. handleHLSMaster forwards its
// own RawQuery into the main.m3u8 link it emits, and handleHLSPlaylist
// rewrites each segment line with that same query — so one token embedded
// here propagates through the whole chain.
func hlsMasterURL(itemID storage.ItemID, playSessionID, apiKey string) string {
	v := url.Values{"PlaySessionId": {playSessionID}}
	if apiKey != "" {
		v.Set("api_key", apiKey)
	}
	return fmt.Sprintf("/Videos/%s/master.m3u8?%s", url.PathEscape(string(itemID)), v.Encode())
}

// handleVideoStream serves GET /Videos/{itemId}/stream and
// /Videos/{itemId}/stream.{container} — the Direct Play data path — and, as
// of hls.go, the HLS segment files the other three modes produce. All are
// registered as a single "GET /Videos/{itemId}/{file}" mux pattern
// (server.go): http.ServeMux wildcards must occupy a whole path segment, so
// neither "stream.{container}" nor a segment's own extension can be its own
// pattern — this handler dispatches on the {file} segment's shape itself
// instead of relying on the router to split it further.
//
// Direct Play: no re-encoding, no ffmpeg subprocess (that's the transcode
// worker's job, docs/07, only reached for the other three modes) — just the
// file bytes, via http.ServeFile so Range/conditional requests and HEAD are
// handled correctly for seeking.
//
// Path-traversal trust boundary: item.Path comes from storage.Item, set by
// whatever wrote that row (the future scanner, docs/08) — never built by
// concatenating any request-supplied path fragment (itemId is only ever
// used as an opaque lookup key into storage.ItemStore, and {file} is only
// ever compared against a literal prefix, never joined into a filesystem
// path). This is the same trust boundary the rest of this codebase already
// relies on for Item.Path/ImageRef.Path; it does not need its own new
// sandboxing layer to stay consistent with that. The HLS-segment branch
// below is the one place {file} genuinely is joined into a filesystem path
// (handleHLSSegment, hls.go) — segmentFilePattern validates it strictly
// before that happens, since unlike item.Path this value comes directly
// off the wire.
func (s *Server) handleVideoStream(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	itemID := storage.ItemID(r.PathValue("itemId"))

	if segmentFilePattern.MatchString(file) {
		s.handleHLSSegment(w, r, itemID, file)
		return
	}
	if file != "stream" && !strings.HasPrefix(file, "stream.") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

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

// bitrateTestDefaultSize is the response size GET /Playback/BitrateTest
// uses when the client omits Size — confirmed against a real Jellyfin
// server (staging, :8097), not assumed: a request with no Size param at
// all came back byte-for-byte the same Content-Length as an explicit
// Size=100000, both landing on 131072 once bitrateTestRoundedSize's
// rounding (below) is applied.
const bitrateTestDefaultSize = 100000

// bitrateTestMinSize/bitrateTestMaxSize are the Size bounds the real
// server enforces — confirmed by binary search against it, not guessed: a
// request carrying an out-of-range Size comes back 400 with a body whose
// validation message spells the bounds out explicitly ("must be greater
// than or equal to 1 and less than or equal to 100000000"), and every
// value up to and including that upper bound succeeds (Size=100000000
// itself returns 200), so the range is inclusive on both ends.
const (
	bitrateTestMinSize = 1
	bitrateTestMaxSize = 100_000_000
)

// bitrateTestFloor is the smallest response bitrateTestRoundedSize will
// ever produce. Confirmed against the real server: every Size from 1
// through 16 inclusive came back with Content-Length 16, not a smaller
// number and not literally echoing Size — this is a genuine floor, not
// coincidental (rounding Size=1 up to the next power of two on its own
// would give 1, not 16).
const bitrateTestFloor = 16

// bitrateTestRoundedSize maps a validated Size query value to the actual
// response length the real server sends — confirmed by requesting a wide
// spread of values against it (staging, :8097) and finding every one of
// them landed exactly on a power of two: 1..16 -> 16, 17..32 -> 32,
// 33..64 -> 64, ..., 100000 -> 131072, 100000000 -> 134217728. Bandwidth
// estimation only needs *some* fixed number of bytes downloaded in a
// measured time, so a real client is never expected to depend on getting
// back exactly the Size it asked for — but this project mirrors the
// rounding anyway (AGENTS.md principle 4: wire compatibility is the
// product) rather than serving the literal requested byte count, in case
// some client does compare Content-Length against what it asked for as a
// sanity check.
func bitrateTestRoundedSize(size int) int {
	n := bitrateTestFloor
	for n < size {
		n <<= 1
	}
	return n
}

// bitrateTestValidationError mirrors the ASP.NET Core ProblemDetails shape
// the real server's own model-binding validation returns for a Size
// outside bitrateTestMinSize/bitrateTestMaxSize or not parseable as an
// integer at all (confirmed against the real server for both cases) — a
// generic ASP.NET Core framework default (RFC 9110 problem-details), not
// anything specific to Jellyfin's own source, so mirroring its shape here
// doesn't cross the clean-room boundary AGENTS.md describes. TraceId is
// left out: it's a per-request diagnostic correlation id with no fixed
// value to reproduce, and nothing in a real client is expected to parse
// it.
type bitrateTestValidationError struct {
	Type   string              `json:"type"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Errors map[string][]string `json:"errors"`
}

func writeBitrateTestValidationError(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, bitrateTestValidationError{
		Type:   "https://tools.ietf.org/html/rfc9110#section-15.5.1",
		Title:  "One or more validation errors occurred.",
		Status: http.StatusBadRequest,
		Errors: map[string][]string{"size": {message}},
	})
}

// handlePlaybackBitrateTest serves GET /Playback/BitrateTest?Size={bytes} —
// the bandwidth probe every official client fires before starting a
// playback session, timing how long the download takes to estimate
// throughput. Found 404ing, not just unpolished, by testing a real
// jellyfin-web build via Playwright.
//
// Response bytes are random — confirmed not to matter to the real server
// either: two back-to-back requests with the same Size came back with
// different bytes. crypto/rand (same package server.go's randomID already
// uses) rather than a non-cryptographic generator: simpler than adding a
// second, separately-seeded RNG source to the project for a value nothing
// about this endpoint's purpose actually needs to be unpredictable, and
// the largest possible response (bitrateTestMaxSize, ~100MB before
// rounding) is small enough that crypto/rand's cost here is negligible
// next to actually writing that many bytes to the response over the
// network. Content-Type is deliberately "application/octet-stream" — a
// browser client parses this as an opaque byte count, not JSON, unlike
// almost every other route in this file.
func (s *Server) handlePlaybackBitrateTest(w http.ResponseWriter, r *http.Request) {
	size := bitrateTestDefaultSize
	if v := r.URL.Query().Get("Size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeBitrateTestValidationError(w, fmt.Sprintf("The value '%s' is not valid.", v))
			return
		}
		size = n
	}
	if size < bitrateTestMinSize || size > bitrateTestMaxSize {
		writeBitrateTestValidationError(w, fmt.Sprintf(
			"The requested size must be greater than or equal to %d and less than or equal to %d",
			bitrateTestMinSize, bitrateTestMaxSize))
		return
	}

	out := make([]byte, bitrateTestRoundedSize(size))
	if _, err := rand.Read(out); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
