// Package playback implements docs/07-transcoding-pipeline.md's "The
// decision": comparing a storage.Item's actual container/codecs against a
// deviceprofile.DeviceProfile and picking the cheapest of the four modes
// (Direct Play, Remux, Direct Stream, Transcode) that satisfies both. It
// has no HTTP or storage.Store coupling beyond the storage.Item/
// deviceprofile.DeviceProfile types themselves — Decide is a pure function,
// so it can be unit tested without a running server or database, and
// internal/api is the only caller that turns its result into an actual
// HTTP response or a internal/transcode.Request.
package playback

import (
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Mode identifies which point on docs/07's ladder Decide selected.
type Mode int

const (
	// ModeNone means no compatible playback method exists: item can't be
	// played as-is against profile, and profile offers no TranscodingProfiles
	// entry to fall back to either (deviceprofile.Default() is the only
	// built-in profile that could ever produce this, and only if it too
	// had no transcoding fallback — see internal/api/playback.go's
	// "NoCompatibleStream" handling).
	ModeNone Mode = iota
	// ModeDirectPlay — file served as-is, no transcode worker involved.
	ModeDirectPlay
	// ModeRemux — container changed, both streams copied untouched.
	ModeRemux
	// ModeDirectStream — video copied untouched; only audio is transcoded.
	ModeDirectStream
	// ModeTranscode — video (and audio) re-encoded, packaged as HLS.
	ModeTranscode
)

// Decision is the result of comparing an item's actual container/codecs
// against a device profile.
type Decision struct {
	Mode Mode

	// Container is the container Decide selected: item's own container for
	// Direct Play, the matching DirectPlayProfile entry's first container
	// for Remux/Direct Stream, or the chosen TranscodingProfile's container
	// for Transcode. Empty when Mode is ModeNone.
	Container string

	// VideoCodec/AudioCodec are internal/transcode.Request's own
	// vocabulary: "copy" when a stream passes through untouched (both, for
	// Remux; video only, for Direct Stream), or a concrete target codec
	// name for Transcode. Both empty when Mode is ModeDirectPlay or
	// ModeNone — Direct Play needs no transcode-worker involvement at all
	// (docs/07), so there is nothing for these fields to describe.
	VideoCodec string
	AudioCodec string

	// AudioStream is the Index of the audio stream the decision was made
	// for (the requested one when it names an audio stream, else the
	// default pick); nil when the item has no audio.
	AudioStream *int

	// Reasons are the TranscodeReasons the reference reports for a
	// non-Direct-Play decision, in its order: ContainerNotSupported,
	// VideoCodecNotSupported, AudioCodecNotSupported,
	// SubtitleCodecNotSupported. Empty for Direct Play.
	Reasons []string
}

// Options narrows a decision beyond the device profile.
type Options struct {
	// AudioStreamIndex asks for a particular audio stream (an Index of
	// item.MediaStreams). An index that is not an audio stream of the item
	// is ignored, like the reference does.
	AudioStreamIndex *int
	// BurnSubtitle: a subtitle is delivered by Encode, so the picture has
	// to be re-encoded and neither Direct Play, Remux nor Direct Stream can
	// serve it.
	BurnSubtitle bool
	// NoDirectPlay: the file cannot be served as is (a subtitle delivered
	// inside the HLS playlist), but the picture may still be copied.
	NoDirectPlay bool
}

// Decide reports how item should be played back against profile, per
// docs/07's ladder (cheapest mode first):
//
//  1. Direct Play — some profile.DirectPlayProfiles entry's Container/
//     VideoCodec/AudioCodec lists all already cover item's actual values.
//  2. Remux — some entry's VideoCodec/AudioCodec lists cover item's actual
//     codecs, just not its container; that entry's own container becomes
//     the repackaging target, both streams copied.
//  3. Direct Stream — some entry's VideoCodec list covers item's actual
//     video codec (even though nothing covered the full combination above);
//     video is copied, audio is re-encoded to that same entry's preferred
//     audio codec.
//  4. Transcode — nothing above applied; falls back to the first Video-type
//     entry in profile.TranscodingProfiles, re-encoding both streams.
//
// No per-condition narrowing (bitrate/resolution/level, via CodecProfile/
// ContainerProfile's ProfileCondition rules) is applied at any step — see
// deviceprofile.DeviceProfile's doc comment for why that's a deliberate,
// documented simplification rather than an oversight. This also means
// Decide only ever reasons about video items (docs/01's video-first scope,
// same convention DirectPlayProfile.Type already documents): an item with
// no video stream at all skips the Remux/Direct Stream steps entirely and
// goes straight to Transcode-or-None.
func Decide(item storage.Item, profile deviceprofile.DeviceProfile) Decision {
	return DecideWith(item, profile, Options{})
}

// DecideWith is Decide for a request that names an audio track and/or needs
// a subtitle burned in (see Options).
func DecideWith(item storage.Item, profile deviceprofile.DeviceProfile, opts Options) Decision {
	d := decide(item, profile, opts)
	if a, ok := pickAudio(item.MediaStreams, opts.AudioStreamIndex); ok {
		idx := a.Index
		d.AudioStream = &idx
	}
	if d.Mode != ModeDirectPlay && d.Mode != ModeNone {
		d.Reasons = reasons(item, profile, opts, d)
	}
	return d
}

// pickAudio is selectStream for audio, honoring a requested stream Index.
func pickAudio(streams []storage.MediaStreamRef, idx *int) (storage.MediaStreamRef, bool) {
	if idx != nil {
		for _, s := range streams {
			if s.Type == storage.StreamTypeAudio && s.Index == *idx {
				return s, true
			}
		}
	}
	return selectStream(streams, storage.StreamTypeAudio)
}

// reasons explains why d is not Direct Play, by what the profile's
// direct-play entries cannot take.
func reasons(item storage.Item, profile deviceprofile.DeviceProfile, opts Options, d Decision) []string {
	var out []string
	video, hasVideo := selectStream(item.MediaStreams, storage.StreamTypeVideo)
	audio, hasAudio := pickAudio(item.MediaStreams, opts.AudioStreamIndex)
	vc, ac := normalizeToken(video.Codec), normalizeToken(audio.Codec)
	anyEntry := func(ok func(dp deviceprofile.DirectPlayProfile) bool) bool {
		for _, dp := range profile.DirectPlayProfiles {
			if (dp.Type == "" || dp.Type == "Video") && ok(dp) {
				return true
			}
		}
		return false
	}
	if !anyEntry(func(dp deviceprofile.DirectPlayProfile) bool {
		return containsToken(dp.Container, normalizeToken(item.Container))
	}) {
		out = append(out, "ContainerNotSupported")
	}
	if hasVideo && !anyEntry(func(dp deviceprofile.DirectPlayProfile) bool {
		return dp.VideoCodec == "" || containsToken(dp.VideoCodec, vc)
	}) {
		out = append(out, "VideoCodecNotSupported")
	}
	if hasAudio && !anyEntry(func(dp deviceprofile.DirectPlayProfile) bool {
		return dp.AudioCodec == "" || containsToken(dp.AudioCodec, ac)
	}) || d.Mode == ModeDirectStream && len(out) == 0 {
		out = append(out, "AudioCodecNotSupported")
	}
	if opts.BurnSubtitle || opts.NoDirectPlay {
		out = append(out, "SubtitleCodecNotSupported")
	}
	if len(out) == 0 {
		// the entries accept each part but no single entry accepts the
		// combination: the reference names the container
		out = append(out, "ContainerNotSupported")
	}
	return out
}

func decide(item storage.Item, profile deviceprofile.DeviceProfile, opts Options) Decision {
	container := normalizeToken(item.Container)
	video, hasVideo := selectStream(item.MediaStreams, storage.StreamTypeVideo)
	audio, hasAudio := pickAudio(item.MediaStreams, opts.AudioStreamIndex)
	videoCodec := normalizeToken(video.Codec)
	audioCodec := normalizeToken(audio.Codec)

	if container != "" && !opts.BurnSubtitle {
		if !opts.NoDirectPlay && matchDirectPlay(profile.DirectPlayProfiles, container, videoCodec, audioCodec, hasVideo, hasAudio) {
			return Decision{Mode: ModeDirectPlay, Container: container}
		}
		if hasVideo && hasAudio {
			if c, ok := matchRemux(profile.DirectPlayProfiles, videoCodec, audioCodec); ok {
				return Decision{Mode: ModeRemux, Container: c, VideoCodec: "copy", AudioCodec: "copy"}
			}
		}
		if hasVideo {
			if c, a, ok := matchDirectStream(profile.DirectPlayProfiles, videoCodec); ok {
				return Decision{Mode: ModeDirectStream, Container: c, VideoCodec: "copy", AudioCodec: a}
			}
		}
	}

	if tp, ok := firstTranscodingProfile(profile.TranscodingProfiles); ok {
		return Decision{
			Mode:       ModeTranscode,
			Container:  tp.Container,
			VideoCodec: firstToken(tp.VideoCodec),
			AudioCodec: firstToken(tp.AudioCodec),
		}
	}

	return Decision{Mode: ModeNone}
}

// matchDirectPlay reports whether some Video-type entry in profiles already
// covers container/videoCodec/audioCodec exactly (Phase 1's original
// DecideDirectPlay logic, unchanged): an item with no stream of a given
// type is treated as unconstrained on that axis rather than rejected (a
// scanner-less item, docs/08), same as a "selected stream" being the
// default-marked one of each type, or the first if none is marked default
// — see selectStream.
func matchDirectPlay(profiles []deviceprofile.DirectPlayProfile, container, videoCodec, audioCodec string, hasVideo, hasAudio bool) bool {
	for _, dp := range profiles {
		if dp.Type != "" && dp.Type != "Video" {
			continue
		}
		if !containsToken(dp.Container, container) {
			continue
		}
		if hasVideo && dp.VideoCodec != "" && !containsToken(dp.VideoCodec, videoCodec) {
			continue
		}
		if hasAudio && dp.AudioCodec != "" && !containsToken(dp.AudioCodec, audioCodec) {
			continue
		}
		return true
	}
	return false
}

// matchRemux returns the first Video-type entry whose VideoCodec/AudioCodec
// lists (if any) both accept item's actual codecs — Remux only repackages,
// so unlike matchDirectPlay this never checks Container, and the entry's
// own first container value becomes the target (Remux docs/07: "container
// changed, both streams copied untouched").
func matchRemux(profiles []deviceprofile.DirectPlayProfile, videoCodec, audioCodec string) (container string, ok bool) {
	for _, dp := range profiles {
		if dp.Type != "" && dp.Type != "Video" {
			continue
		}
		if dp.VideoCodec != "" && !containsToken(dp.VideoCodec, videoCodec) {
			continue
		}
		if dp.AudioCodec != "" && !containsToken(dp.AudioCodec, audioCodec) {
			continue
		}
		if c := firstToken(dp.Container); c != "" {
			return c, true
		}
	}
	return "", false
}

// matchDirectStream returns the first Video-type entry whose VideoCodec
// list (if any) accepts item's actual video codec, regardless of its audio
// codec — Direct Stream copies video and re-encodes audio to whatever that
// same entry names first as its preferred audio codec (docs/07: "video
// copied untouched; only audio is transcoded").
func matchDirectStream(profiles []deviceprofile.DirectPlayProfile, videoCodec string) (container, audioTarget string, ok bool) {
	for _, dp := range profiles {
		if dp.Type != "" && dp.Type != "Video" {
			continue
		}
		if dp.VideoCodec != "" && !containsToken(dp.VideoCodec, videoCodec) {
			continue
		}
		c := firstToken(dp.Container)
		a := firstToken(dp.AudioCodec)
		if c != "" && a != "" {
			return c, a, true
		}
	}
	return "", "", false
}

// firstTranscodingProfile returns the first Video-type entry in profiles,
// if any — profile.TranscodingProfiles' declared order is the server's own
// preference ordering (matching how DirectPlayProfiles is already treated
// as ordered by the matchers above), so Decide doesn't second-guess it with
// its own ranking heuristic.
func firstTranscodingProfile(profiles []deviceprofile.TranscodingProfile) (deviceprofile.TranscodingProfile, bool) {
	for _, tp := range profiles {
		if tp.Type != "" && tp.Type != "Video" {
			continue
		}
		return tp, true
	}
	return deviceprofile.TranscodingProfile{}, false
}

// selectStream returns the default-marked stream of type t, or the first
// stream of that type if none is marked default. ok is false if item has
// no stream of that type at all.
func selectStream(streams []storage.MediaStreamRef, t storage.StreamType) (stream storage.MediaStreamRef, ok bool) {
	for _, s := range streams {
		if s.Type != t {
			continue
		}
		if !ok {
			stream = s
			ok = true
		}
		if s.IsDefault {
			return s, true
		}
	}
	return stream, ok
}

// normalizeToken lowercases and trims a single container/codec value for
// case-insensitive comparison — the wire format doesn't guarantee casing
// consistency between what a scanner recorded and what a client's device
// profile spells out.
func normalizeToken(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// containsToken reports whether csv (a comma-separated list, e.g.
// "h264,hevc") contains token, comparing case-insensitively. An empty csv
// never matches (callers check DirectPlayProfile's "empty means
// unrestricted" rule themselves before calling this).
func containsToken(csv, token string) bool {
	if token == "" {
		return false
	}
	for _, part := range strings.Split(csv, ",") {
		if normalizeToken(part) == token {
			return true
		}
	}
	return false
}

// firstToken returns the first entry of a comma-separated wire-level list
// (e.g. DirectPlayProfile.Container's "mp4,mkv,webm"), normalized, or ""
// if csv is empty. Used wherever Decide needs an actual target value (a
// container/codec to hand internal/transcode), not just a yes/no match —
// containsToken alone is enough for matching, but Remux/Direct
// Stream/Transcode all need to pick one concrete value out of the list
// they matched against.
func firstToken(csv string) string {
	if csv == "" {
		return ""
	}
	parts := strings.SplitN(csv, ",", 2)
	return normalizeToken(parts[0])
}
