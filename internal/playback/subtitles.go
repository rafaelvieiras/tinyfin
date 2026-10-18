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

// Delivery is the situation a subtitle is delivered in.
type Delivery struct {
	// AllowEmbed is false when the source is not served as it is: an
	// embedded track cannot stay in the container then. AllowHls is false
	// when it is: only a transcode has an HLS manifest to carry a text
	// track. The stream a player selects is first tried with both true, to
	// learn which of the two situations it puts the source in.
	AllowEmbed, AllowHls bool
	// Container is the extension of the media file (lower case, no dot);
	// an Embed entry that lists containers applies only if it is among them.
	Container string
}

func isAssCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "ass", "ssa":
		return true
	}
	return false
}

// externalDenied are the formats a text track is never offered as: the
// SSA family (kept for tracks of that family) and the picture formats.
var externalDenied = map[string]bool{
	"ass": true, "ssa": true, "sub": true, "sup": true, "pgs": true, "pgssub": true,
	"vobsub": true, "dvdsub": true, "dvbsub": true, "hdmv_pgs_subtitle": true,
}

// languageMatches reports whether the profile entry's Language (a comma
// separated list of codes, compared as they are, any case) admits a stream
// of the language lang. An empty list admits every stream.
func languageMatches(list, lang string) bool {
	if list == "" {
		return true
	}
	for _, l := range strings.Split(list, ",") {
		if lang != "" && strings.EqualFold(l, lang) {
			return true
		}
	}
	return false
}

// containerMatches is the same test for the Container list.
func containerMatches(list, container string) bool {
	if list == "" {
		return true
	}
	for _, c := range strings.Split(list, ",") {
		if strings.EqualFold(c, container) {
			return true
		}
	}
	return false
}

// DeliverSubtitle picks the first entry of profiles that can serve s. An
// entry whose Language list does not admit the stream is skipped, and so are
// Encode and Drop entries (a stream no entry serves is Encode). By method:
//
//   - External: a text track other than SSA/ASS in any format except the
//     picture and SSA/ASS names (the stream is converted to the entry's
//     format, whatever it is called; the URL ends in the format as it is
//     written); an SSA/ASS track in "ass"; a picture track in the name of
//     its own codec (PGSSUB, DVDSUB, DVBSUB, XSUB);
//   - Hls: only when the source is transcoded (Delivery.AllowHls), a text track in any format
//     but SSA/ASS, or an SSA/ASS track in "ass"; picture tracks never;
//   - Embed: only when the source is served as it is, a track embedded in
//     the media file, whose container the entry admits (when it lists
//     any), under the same format rules as External except that a text
//     track takes any format but "ass" and "ssa".
func DeliverSubtitle(s storage.MediaStreamRef, profiles []deviceprofile.SubtitleProfile, d Delivery) SubtitleDelivery {
	text := subtitle.IsTextCodec(s.Codec)
	ass := isAssCodec(s.Codec)
	for _, p := range profiles {
		if !languageMatches(p.Language, s.Language) {
			continue
		}
		f := strings.ToLower(p.Format)
		switch p.Method {
		case "External":
			if formatServes(s, text, ass, f, externalDenied[f]) {
				return SubtitleDelivery{Method: "External", Format: p.Format}
			}
		case "Hls":
			if d.AllowHls && text && formatServes(s, text, ass, f, f == "ass" || f == "ssa") {
				return SubtitleDelivery{Method: "Hls", Format: p.Format}
			}
		case "Embed":
			if d.AllowEmbed && !s.IsExternal && containerMatches(p.Container, d.Container) &&
				formatServes(s, text, ass, f, f == "ass" || f == "ssa") {
				return SubtitleDelivery{Method: "Embed"}
			}
		}
	}
	return SubtitleDelivery{Method: "Encode"}
}

// formatServes tests format f (lower case) against the kind of track s is:
// a text track takes any format not in the denied set, an SSA/ASS track
// only "ass", a picture track only the name of its own codec.
func formatServes(s storage.MediaStreamRef, text, ass bool, f string, denied bool) bool {
	switch {
	case ass:
		return f == "ass"
	case text:
		return !denied
	}
	return strings.EqualFold(f, s.Codec)
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
	score := 100000 + 100
	if subtitle.SupportsExternal(s.Codec) {
		score += 100
	}
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
