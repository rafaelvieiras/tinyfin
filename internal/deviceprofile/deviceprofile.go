// Package deviceprofile implements the O2 decision from
// docs/07-transcoding-pipeline.md: a small, compiled-into-the-binary table
// of DeviceProfile values keyed by the Client string parsed out of
// the Authorization header (docs/04), used only as a fallback for requests that
// don't carry an inline profile of their own.
//
// DeviceProfile is deliberately the same Go type a client-submitted profile
// decodes into (docs/07: "there's no separate internal format to keep in
// sync with the wire one") — see the PlaybackInfo request body handling in
// internal/api.
package deviceprofile

// DeviceProfile mirrors the OpenAPI DeviceProfile schema. Phase 1 modeled
// only Name/DirectPlayProfiles (Direct-Play-only decisions); this is the
// Phase 2 follow-up (see ROADMAP.md) that adds the fields the Remux/Direct
// Stream/Transcode ladder (docs/07) needs.
//
// TranscodingProfiles is the field that actually drives the new decision
// logic (internal/playback/decision.go, being extended separately): a
// client is only transcode-capable at all if this slice has at least one
// entry, since it's what tells the decision layer which
// container/codec/protocol combination to fall back to once Direct Play,
// Remux, and Direct Stream have all been ruled out.
//
// ContainerProfiles, CodecProfiles (and the ProfileCondition rules they
// carry), and SubtitleProfiles are modeled here for wire completeness only
// — a client's submitted profile may legitimately include them, and Go's
// JSON decoder needs a field to decode into rather than silently dropping
// them — but their *conditional* semantics are deliberately not evaluated
// by the decision logic yet. In the reference server these narrow Direct
// Play on a per-condition basis (e.g. "h264 is direct-playable only up to
// VideoLevel 4.1"); Tinyfin's decision logic does not yet apply that
// narrowing. This is a scoped-out simplification, not an oversight —
// matching this file's existing "fields TBD"/deferred precedent (see
// docs/05) — and it means Tinyfin's Direct Play decisions are currently
// coarser (container/codec-list only) than what a full CodecProfile
// evaluation would produce. A future engineer implementing that narrowing
// should start here.
type DeviceProfile struct {
	Name string `json:"Name,omitempty"`
	// MaxStreamingBitrate bounds what the client takes (bits/s); a body-level
	// MaxStreamingBitrate of the request takes precedence over it.
	MaxStreamingBitrate int64 `json:"MaxStreamingBitrate,omitempty"`
	// MusicStreamingTranscodingBitrate is the rate audio is transcoded to.
	MusicStreamingTranscodingBitrate int64                `json:"MusicStreamingTranscodingBitrate,omitempty"`
	DirectPlayProfiles               []DirectPlayProfile  `json:"DirectPlayProfiles"`
	TranscodingProfiles              []TranscodingProfile `json:"TranscodingProfiles"`
	ContainerProfiles                []ContainerProfile   `json:"ContainerProfiles,omitempty"`
	CodecProfiles                    []CodecProfile       `json:"CodecProfiles,omitempty"`
	SubtitleProfiles                 []SubtitleProfile    `json:"SubtitleProfiles,omitempty"`
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

// TranscodingProfile mirrors the OpenAPI TranscodingProfile schema: the
// target container/codec/protocol Tinyfin's transcode worker (docs/07)
// falls back to once Direct Play, Remux, and Direct Stream have all been
// ruled out for a given item. See the DeviceProfile doc comment above for
// why this is the one Phase 2 field the new decision logic actually
// consumes.
type TranscodingProfile struct {
	Container string `json:"Container"`
	// Type is the DlnaProfileType this entry applies to ("Audio", "Video",
	// "Photo", "Subtitle", "Lyric"), same convention as DirectPlayProfile.Type.
	Type                      string             `json:"Type"`
	VideoCodec                string             `json:"VideoCodec,omitempty"`
	AudioCodec                string             `json:"AudioCodec,omitempty"`
	Protocol                  string             `json:"Protocol,omitempty"` // "http" | "hls"
	EstimateContentLength     bool               `json:"EstimateContentLength"`
	EnableMpegtsM2TsMode      bool               `json:"EnableMpegtsM2TsMode"`
	TranscodeSeekInfo         string             `json:"TranscodeSeekInfo,omitempty"` // "Auto" | "Bytes"
	CopyTimestamps            bool               `json:"CopyTimestamps"`
	Context                   string             `json:"Context,omitempty"` // EncodingContext: "Streaming" | "Static"
	EnableSubtitlesInManifest bool               `json:"EnableSubtitlesInManifest"`
	MaxAudioChannels          string             `json:"MaxAudioChannels,omitempty"`
	MinSegments               int                `json:"MinSegments,omitempty"`
	SegmentLength             int                `json:"SegmentLength,omitempty"`
	BreakOnNonKeyFrames       bool               `json:"BreakOnNonKeyFrames"`
	Conditions                []ProfileCondition `json:"Conditions,omitempty"`
	// EnableAudioVbrEncoding defaults to true when the profile omits it.
	EnableAudioVbrEncoding *bool `json:"EnableAudioVbrEncoding,omitempty"`
}

// ContainerProfile mirrors the OpenAPI ContainerProfile schema. Not yet
// evaluated by the decision logic — see the DeviceProfile doc comment.
type ContainerProfile struct {
	Type         string             `json:"Type"`
	Conditions   []ProfileCondition `json:"Conditions,omitempty"`
	Container    string             `json:"Container,omitempty"`
	SubContainer string             `json:"SubContainer,omitempty"`
}

// CodecProfile mirrors the OpenAPI CodecProfile schema. Not yet evaluated
// by the decision logic — see the DeviceProfile doc comment.
type CodecProfile struct {
	Type            string             `json:"Type"` // CodecType: "Video"/"VideoAudio"/"Audio"
	Conditions      []ProfileCondition `json:"Conditions,omitempty"`
	ApplyConditions []ProfileCondition `json:"ApplyConditions,omitempty"`
	Codec           string             `json:"Codec,omitempty"`
	Container       string             `json:"Container,omitempty"`
	SubContainer    string             `json:"SubContainer,omitempty"`
}

// ProfileCondition mirrors the OpenAPI ProfileCondition schema — a single
// (property, comparison, value) rule, e.g. "VideoBitrate LessThanEqual
// 8000000" or "Width LessThanEqual 1920". Property is kept as a plain
// string rather than a closed Go enum, matching this file's existing
// pattern for wire vocabularies (see DirectPlayProfile.Type): known values
// include "AudioChannels", "Width", "Height", "VideoBitrate",
// "VideoLevel", "VideoProfile", "VideoBitDepth", "AudioBitrate",
// "AudioSampleRate", "RefFrames", "VideoFramerate", "IsAnamorphic",
// "IsInterlaced", "IsAvc", "NumAudioStreams", "NumVideoStreams",
// "VideoRangeType", among others.
type ProfileCondition struct {
	Condition  string `json:"Condition"` // "Equals"|"NotEquals"|"LessThanEqual"|"GreaterThanEqual"|"EqualsAny"
	Property   string `json:"Property"`
	Value      string `json:"Value,omitempty"`
	IsRequired bool   `json:"IsRequired"`
}

// SubtitleProfile mirrors the OpenAPI SubtitleProfile schema. Not yet
// evaluated by the decision logic — see the DeviceProfile doc comment.
type SubtitleProfile struct {
	Format    string `json:"Format,omitempty"`
	Method    string `json:"Method,omitempty"` // "Encode"|"Embed"|"External"|"Hls"|"Drop"
	DidlMode  string `json:"DidlMode,omitempty"`
	Language  string `json:"Language,omitempty"`
	Container string `json:"Container,omitempty"`
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
// Authorization header Client field as "Jellyfin Web" in every publicly
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

// All three built-in profiles below share the same TranscodingProfiles
// entry: H.264 baseline + AAC packaged as HLS/MPEG-TS, the one combination
// every client capable of HLS at all is expected to decode. This is the
// same "conservative starting point, grows on demand" reasoning this file
// already applies to DirectPlayProfiles (docs/07 O2) — without at least
// one TranscodingProfile entry a client falling back to one of these
// built-ins could never reach Transcode mode when Direct Play doesn't
// apply, which would silently regress Phase 2's goal for exactly the
// clients that don't submit an inline profile of their own.

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
	TranscodingProfiles: []TranscodingProfile{
		{
			Container:     "ts",
			Type:          "Video",
			VideoCodec:    "h264",
			AudioCodec:    "aac",
			Protocol:      "hls",
			Context:       "Streaming",
			MinSegments:   1,
			SegmentLength: 6,
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
	// Two entries rather than webProfile/defaultProfile's one: mobileProfile's
	// own DirectPlayProfiles already lists hevc as natively decodable by the
	// platform media framework (ExoPlayer/AVFoundation), so it's reasonable to
	// also offer hevc as a transcode target, not just the universally-safe
	// h264 baseline — a lower-bitrate hevc encode is cheaper to produce and
	// stream when the client can actually play it back.
	TranscodingProfiles: []TranscodingProfile{
		{
			Container:     "ts",
			Type:          "Video",
			VideoCodec:    "h264",
			AudioCodec:    "aac",
			Protocol:      "hls",
			Context:       "Streaming",
			MinSegments:   1,
			SegmentLength: 6,
		},
		{
			Container:     "ts",
			Type:          "Video",
			VideoCodec:    "hevc",
			AudioCodec:    "aac",
			Protocol:      "hls",
			Context:       "Streaming",
			MinSegments:   1,
			SegmentLength: 6,
		},
	},
}

// defaultProfile is the O2 fallback for a Client string with no builtins
// entry and no inline profile on the request: Direct Play limited to a
// narrow, safe baseline (docs/07: "a single built-in conservative default
// profile ... rather than failing the request outright"), with the same
// H.264+AAC/HLS TranscodingProfile as webProfile/mobileProfile as the
// fallback's own fallback — an item outside the Direct Play baseline now
// reaches the decision logic's Transcode mode (docs/07's
// Remux/Direct-Stream/Transcode ladder) instead of reporting no compatible
// playback method.
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
	TranscodingProfiles: []TranscodingProfile{
		{
			Container:     "ts",
			Type:          "Video",
			VideoCodec:    "h264",
			AudioCodec:    "aac",
			Protocol:      "hls",
			Context:       "Streaming",
			MinSegments:   1,
			SegmentLength: 6,
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
