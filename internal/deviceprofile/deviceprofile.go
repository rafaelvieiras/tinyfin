// Package deviceprofile implements the O2 decision from
// docs/07-transcoding-pipeline.md: a small, compiled-into-the-binary table
// of DeviceProfile values keyed by the Client string parsed out of
// X-Emby-Authorization (docs/04), used only as a fallback for requests that
// don't carry an inline profile of their own.
//
// DeviceProfile is deliberately the same Go type a client-submitted profile
// decodes into (docs/07: "there's no separate internal format to keep in
// sync with the wire one") — see the PlaybackInfo request body handling in
// internal/api.
package deviceprofile

// DeviceProfile mirrors the OpenAPI DeviceProfile schema, trimmed to what a
// Direct-Play-only decision needs (this project's Phase 1 — see
// ROADMAP.md). TranscodingProfiles, ContainerProfiles, CodecProfiles, and
// SubtitleProfiles are deferred to Phase 2 (the Remux/Direct
// Stream/Transcode ladder in docs/07) and intentionally not modeled here,
// matching docs/05's "fields TBD" precedent for out-of-scope struct
// members rather than fabricating a shape nothing consumes yet.
type DeviceProfile struct {
	Name               string              `json:"Name,omitempty"`
	DirectPlayProfiles []DirectPlayProfile `json:"DirectPlayProfiles"`
}

// DirectPlayProfile mirrors the OpenAPI DirectPlayProfile schema exactly:
// Container/AudioCodec/VideoCodec are single wire-level strings holding a
// comma-separated list (e.g. "h264,hevc"), not JSON arrays — this matches
// how a real client actually submits (and expects to receive) this object,
// which is what wire compatibility (docs/04) requires. Decision logic
// splits these lazily where needed (see internal/playback) rather than
// this package pre-parsing them into slices, so the type stays a literal
// mirror of the wire shape.
//
// An empty Container/AudioCodec/VideoCodec means "no restriction on this
// axis" — the reference server's own DLNA profile semantics (nullable
// fields), not a Tinyfin invention.
type DirectPlayProfile struct {
	Container  string `json:"Container"`
	AudioCodec string `json:"AudioCodec,omitempty"`
	VideoCodec string `json:"VideoCodec,omitempty"`

	// Type is the DlnaProfileType this entry applies to ("Video", "Audio",
	// ...). This slice is video-first (docs/01), so the decision logic
	// only ever looks at "Video" entries; other Type values are accepted
	// (a client's submitted profile may legitimately carry them) but
	// simply never match a video item's decision.
	Type string `json:"Type"`
}

// builtins is the O2 table: static configuration data compiled into the
// binary, not a row in storage.Store (docs/07 O2, resolved — profiles are
// client-software metadata on a release cadence, not per-deployment user
// data).
//
// Coverage is intentionally minimal (docs/07 O2: "starts with only the
// clients actually in use and grows the set on demand"), not the reference
// server's exhaustive built-in list.
//
// Client-string caveat: "Jellyfin Web" is confirmed by the reference
// server's own web client source conventions (it identifies itself via the
// X-Emby-Authorization Client field as "Jellyfin Web" in every publicly
// documented capture). The official Android/iOS app Client strings below
// are NOT independently confirmed against real client traffic — this
// project's clean-room policy (AGENTS.md) rules out consulting the
// reference server's source to check, and the OpenAPI schema alone doesn't
// name them. "Jellyfin Android" / "Jellyfin iOS" are reasonable, documented
// placeholders (matching the app names as published on their respective
// app stores); confirm the exact strings against real client traffic
// before relying on this fallback in production, per docs/04's
// verification approach. An unmatched Client still works correctly in the
// meantime via Default() below — just not optimally (docs/07 O2).
var builtins = map[string]DeviceProfile{
	"Jellyfin Web": webProfile,

	// The official Android and iOS apps get the same conservative native
	// baseline profile (see mobileProfile below) registered under both of
	// their placeholder Client strings — one DeviceProfile value, two
	// lookup keys, since Lookup is keyed on the exact Client string a
	// request carries and the two platforms are expected to identify
	// themselves differently.
	"Jellyfin Android": mobileProfile,
	"Jellyfin iOS":     mobileProfile,
}

// webProfile is the baseline for Jellyfin Web: containers/codecs a
// browser's native <video> element can decode without a plugin, kept
// conservative rather than maximal (docs/07 O2: "grows on demand"). No mkv
// — mainstream browsers don't support it as a native playback container —
// and no hevc/ac3, which most desktop browser builds still can't decode
// without hardware/OS-level codec support Tinyfin can't detect from here.
var webProfile = DeviceProfile{
	Name: "Jellyfin Web",
	DirectPlayProfiles: []DirectPlayProfile{
		{
			Type:       "Video",
			Container:  "mp4,webm",
			VideoCodec: "h264,vp9",
			AudioCodec: "aac,mp3,flac,opus",
		},
	},
}

// mobileProfile is the baseline for the official native mobile apps, which
// decode via the platform's own media framework (ExoPlayer on Android,
// AVFoundation on iOS) rather than a browser engine — broader native
// container/codec support than webProfile, still a conservative starting
// point per docs/07 O2, not an exhaustive list.
var mobileProfile = DeviceProfile{
	Name: "Jellyfin Mobile",
	DirectPlayProfiles: []DirectPlayProfile{
		{
			Type:       "Video",
			Container:  "mp4,mkv,webm",
			VideoCodec: "h264,hevc,vp9",
			AudioCodec: "aac,ac3,flac,mp3",
		},
	},
}

// defaultProfile is the O2 fallback for a Client string with no builtins
// entry and no inline profile on the request: Direct Play limited to a
// narrow, safe baseline rather than failing the request outright (docs/07:
// "a single built-in conservative default profile ... rather than failing
// the request outright"). Since this slice has no transcode worker yet
// (out of scope, see ROADMAP.md Phase 1), an item outside this baseline
// simply reports no compatible playback method (see internal/playback and
// internal/api's PlaybackInfo handler) instead of the reference server's
// "everything else forced through Transcode" — there is nothing to
// transcode with yet.
var defaultProfile = DeviceProfile{
	Name: "Tinyfin Default",
	DirectPlayProfiles: []DirectPlayProfile{
		{
			Type:       "Video",
			Container:  "mp4",
			VideoCodec: "h264",
			AudioCodec: "aac",
		},
	},
}

// Lookup returns the built-in profile for client, if any. Callers should
// only consult this when the request itself didn't carry an inline device
// profile (docs/07: the inline profile is authoritative whenever present).
func Lookup(client string) (DeviceProfile, bool) {
	p, ok := builtins[client]
	return p, ok
}

// Default returns the conservative fallback profile used when neither an
// inline profile nor a builtins entry is available.
func Default() DeviceProfile {
	return defaultProfile
}
