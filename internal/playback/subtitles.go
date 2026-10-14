package playback

import (
	"sort"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// SubtitleDelivery is how one subtitle stream reaches the player.
type SubtitleDelivery struct {
	// Method is "External", "Embed", "Hls" or "Encode".
	Method string
	// Format is the extension the external stream URL ends in (External
	// only).
	Format string
}

// subtitleFormat maps a stream codec to the name a SubtitleProfile uses
// for it.
func subtitleFormat(codec string) string {
	switch strings.ToLower(codec) {
	case "subrip", "srt", "mov_text", "text":
		return "srt"
	case "webvtt", "vtt":
		return "vtt"
	case "ass":
		return "ass"
	case "ssa":
		return "ssa"
	case "sami", "smi":
		return "smi"
	}
	return strings.ToLower(codec)
}

// convertible reports whether a text stream of source format src can be
// delivered as dst: the same format, or any text format other than SSA/ASS
// as SubRip or WebVTT.
func convertible(src, dst string) bool {
	if src == dst {
		return true
	}
	return (dst == "vtt" || dst == "srt") && src != "ass" && src != "ssa"
}

// DeliverSubtitle picks the first entry of profiles that can serve s: an
// entry whose Language filter admits the stream (when set) and, by method,
//
//   - External: the stream is a text stream and its format is the entry's
//     format or convertible to it (the URL ends in the entry's format);
//   - Hls: the same test, delivered through the HLS manifest;
//   - Embed: the stream is embedded in the media file and its format is
//     the entry's format;
//   - Encode: always (the subtitle is burned into the video).
//
// The entry's Language filter is one language code. A stream that no entry
// serves is Encode.
//
// allowEmbed is false when the source is going to be transcoded: an
// embedded track cannot stay in the container then, so Embed entries do not
// apply.
func DeliverSubtitle(s storage.MediaStreamRef, profiles []deviceprofile.SubtitleProfile, allowEmbed bool) SubtitleDelivery {
	src := subtitleFormat(s.Codec)
	text := subtitle.IsTextCodec(s.Codec)
	for _, p := range profiles {
		if p.Language != "" && !sameLanguage(s.Language, p.Language) {
			continue
		}
		format := subtitleFormat(strings.TrimSpace(p.Format))
		switch strings.ToLower(p.Method) {
		case "external":
			if text && convertible(src, format) {
				return SubtitleDelivery{Method: "External", Format: format}
			}
		case "hls":
			if text && convertible(src, format) {
				return SubtitleDelivery{Method: "Hls", Format: format}
			}
		case "embed":
			if allowEmbed && !s.IsExternal && src == format {
				return SubtitleDelivery{Method: "Embed"}
			}
		case "encode":
			return SubtitleDelivery{Method: "Encode"}
		}
	}
	return SubtitleDelivery{Method: "Encode"}
}

func sameLanguage(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, oka := subtitle.ResolveLanguage(a)
	rb, okb := subtitle.ResolveLanguage(b)
	return oka && okb && ra == rb
}

// SubtitleScore is the Score the reference reports for a subtitle stream.
func SubtitleScore(s storage.MediaStreamRef) int {
	score := 100000 + 200
	score += 10000
	if s.IsForced {
		score += 10000
	}
	score += 1000
	if s.IsDefault {
		score += 1000
	}
	if subtitle.IsTextCodec(s.Codec) {
		score += 20
	} else {
		score += 10
	}
	if s.IsExternal {
		score += 2
	} else {
		score++
	}
	return score
}

// DefaultSubtitleIndex is the stream a user's subtitle preferences select
// for playback of a source, or nil for none. mode is the user's
// SubtitleMode (Default, Always, OnlyForced, None, Smart); langs the
// SubtitleLanguagePreference codes; audioLang the language of the audio
// stream that will play.
//
// Streams are ranked external first, then default ones, then (with no
// language preference) the ones that are not forced, or (with one) the
// forced ones, then those in a preferred language, then by index; the
// modes pick from that ranking.
func DefaultSubtitleIndex(streams []storage.MediaStreamRef, mode string, langs []string, audioLang string) *int {
	var subs []storage.MediaStreamRef
	for _, s := range streams {
		if s.Type == storage.StreamTypeSubtitle {
			subs = append(subs, s)
		}
	}
	if len(subs) == 0 || strings.EqualFold(mode, "None") {
		return nil
	}
	inPrefs := func(s storage.MediaStreamRef) bool {
		for _, l := range langs {
			if sameLanguage(s.Language, l) {
				return true
			}
		}
		return false
	}
	hasPrefs := len(langs) > 0
	sort.SliceStable(subs, func(i, j int) bool {
		a, b := subs[i], subs[j]
		if a.IsExternal != b.IsExternal {
			return a.IsExternal
		}
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if a.IsForced != b.IsForced {
			if hasPrefs {
				return a.IsForced
			}
			return !a.IsForced
		}
		if hasPrefs && inPrefs(a) != inPrefs(b) {
			return inPrefs(a)
		}
		return a.Index < b.Index
	})
	first := func(pred func(storage.MediaStreamRef) bool) *int {
		for _, s := range subs {
			if pred(s) {
				i := s.Index
				return &i
			}
		}
		return nil
	}
	byDefault := func() *int {
		return first(func(s storage.MediaStreamRef) bool { return s.IsExternal || s.IsForced || s.IsDefault })
	}
	forcedInAudio := func() *int {
		return first(func(s storage.MediaStreamRef) bool { return s.IsForced && sameLanguage(s.Language, audioLang) })
	}
	switch strings.ToLower(mode) {
	case "always":
		var r *int
		if hasPrefs {
			r = first(inPrefs)
		} else {
			r = first(func(s storage.MediaStreamRef) bool { return !s.IsForced })
		}
		if r == nil {
			r = forcedInAudio()
		}
		return r
	case "onlyforced":
		return first(func(s storage.MediaStreamRef) bool {
			return s.IsForced && (!hasPrefs || inPrefs(s))
		})
	case "smart":
		if !hasPrefs {
			if r := byDefault(); r != nil {
				return r
			}
			return forcedInAudio()
		}
		for _, l := range langs {
			if sameLanguage(audioLang, l) {
				return forcedInAudio()
			}
		}
		if r := first(inPrefs); r != nil {
			return r
		}
		return forcedInAudio()
	default: // Default
		if r := byDefault(); r != nil {
			return r
		}
		return forcedInAudio()
	}
}

// DefaultAudioIndex is the audio stream a source plays by default: the
// first one in the preferred language when a preference is set, otherwise
// the one flagged default, otherwise the first.
func DefaultAudioIndex(streams []storage.MediaStreamRef, preferredLang string) *int {
	var firstAudio, def *storage.MediaStreamRef
	for i := range streams {
		s := &streams[i]
		if s.Type != storage.StreamTypeAudio {
			continue
		}
		if preferredLang != "" && sameLanguage(s.Language, preferredLang) {
			idx := s.Index
			return &idx
		}
		if firstAudio == nil {
			firstAudio = s
		}
		if def == nil && s.IsDefault {
			def = s
		}
	}
	switch {
	case def != nil:
		return &def.Index
	case firstAudio != nil:
		return &firstAudio.Index
	}
	return nil
}
