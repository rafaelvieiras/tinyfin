// Package playback implements the Direct Play decision from
// docs/07-transcoding-pipeline.md's "The decision" section, narrowed to
// this slice's scope (ROADMAP.md Phase 1: "Direct Play only ... No
// transcoding yet"). It has no HTTP or storage.Store coupling — it takes a
// storage.Item and a deviceprofile.DeviceProfile and returns a pure
// decision, so it can be unit tested without a running server or database.
//
// The full Remux/Direct Stream/Transcode ladder (docs/07) is out of scope
// here; deciding "not Direct Play" is as far as this package goes.
package playback

import (
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Decision is the result of comparing an item's actual container/codecs
// against a device profile's DirectPlayProfiles.
type Decision struct {
	DirectPlaySupported bool

	// Container is the item's container when DirectPlaySupported is true
	// (the value the client should expect to receive byte-for-byte, per
	// Direct Play's definition in docs/07 — "file served as-is"). Empty
	// when DirectPlaySupported is false.
	Container string
}

// DecideDirectPlay reports whether item can be served as-is to a client
// described by profile. It looks for a "Video"-type DirectPlayProfile entry
// whose Container list includes the item's container and whose
// VideoCodec/AudioCodec lists (if any — empty means unrestricted, per
// DirectPlayProfile's doc comment) cover the item's selected video and
// audio streams.
//
// "Selected" stream, not "every embedded stream": a real client plays one
// video track and one audio track at a time (its default, or whichever the
// user picks), so a profile mismatch on a non-default extra audio track
// (e.g. a commentary track in a codec the client can't decode) does not by
// itself rule out Direct Play — matching what a client would actually
// attempt. The default-marked stream of each type is used when present,
// otherwise the first stream of that type; an item with no streams of a
// given type (MediaStreams not yet populated — no scanner exists yet, see
// docs/08) is treated as unconstrained on that axis rather than rejected.
func DecideDirectPlay(item storage.Item, profile deviceprofile.DeviceProfile) Decision {
	container := normalizeToken(item.Container)
	if container == "" {
		return Decision{}
	}

	video, hasVideo := selectStream(item.MediaStreams, storage.StreamTypeVideo)
	audio, hasAudio := selectStream(item.MediaStreams, storage.StreamTypeAudio)

	for _, dp := range profile.DirectPlayProfiles {
		if dp.Type != "" && dp.Type != "Video" {
			continue
		}
		if !containsToken(dp.Container, container) {
			continue
		}
		if hasVideo && dp.VideoCodec != "" && !containsToken(dp.VideoCodec, normalizeToken(video.Codec)) {
			continue
		}
		if hasAudio && dp.AudioCodec != "" && !containsToken(dp.AudioCodec, normalizeToken(audio.Codec)) {
			continue
		}
		return Decision{DirectPlaySupported: true, Container: container}
	}
	return Decision{}
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
