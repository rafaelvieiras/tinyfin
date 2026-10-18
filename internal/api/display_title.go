// display_title.go implements MediaStream.DisplayTitle — the formatted
// per-track label a real client's video/audio track-selector panel needs to
// render at all (found missing, not just unpolished: without it, the
// selector simply doesn't appear — see docs/12's "Episode/movie detail
// page — MediaStream.DisplayTitle" for the gap this resolves and the full
// algorithm this file implements from, extracted from the reference
// server's own source under this project's clean-room two-phase process,
// AGENTS.md/D12).
//
// A pure, response-build-time formatting function over fields
// storage.MediaStreamRef already carries — never persisted (docs/12's scope
// note), so it stays correct if the underlying probe data is ever corrected
// without needing a rescan. Called from dto.go's mediaStreamDtos, once per
// stream, alongside the rest of mediaStreamDto's construction.
package api

import (
	"fmt"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// resolutionBreakpoint is one entry of the width/height -> resolution-label
// table docs/12 documents (its "Video streams" point 1). suffixed is false
// only for the two largest entries (4K/8K), which the spec calls out as
// never carrying an i/p interlace suffix.
type resolutionBreakpoint struct {
	maxWidth, maxHeight int
	label               string
	suffixed            bool
}

// resolutionBreakpoints is matched in order, first-fit — a width/height
// pair past the last entry (8192x6144) yields no resolution label at all
// rather than guessing, same as a pair that doesn't fit any single
// breakpoint's width AND height bound together (e.g. a very wide but short
// frame): docs/12 explicitly calls out "rather than guessing" for the
// off-the-end case, and the same caution applies here.
var resolutionBreakpoints = []resolutionBreakpoint{
	{256, 144, "144", true},
	{426, 240, "240", true},
	{640, 360, "360", true},
	{682, 384, "384", true},
	{720, 404, "404", true},
	{854, 480, "480", true},
	{960, 544, "540", true},
	{1024, 576, "576", true},
	{1280, 962, "720", true},
	{2560, 1440, "1080", true},
	{4096, 3072, "4K", false},
	{8192, 6144, "8K", false},
}

// resolutionLabel implements docs/12's "Video streams" point 1.
func resolutionLabel(width, height *int, interlaced bool) string {
	if width == nil || height == nil || *width <= 0 || *height <= 0 {
		return ""
	}
	for _, bp := range resolutionBreakpoints {
		if *width <= bp.maxWidth && *height <= bp.maxHeight {
			if !bp.suffixed {
				return bp.label
			}
			if interlaced {
				return bp.label + "i"
			}
			return bp.label + "p"
		}
	}
	return ""
}

// hdrColorTransfers are the ffprobe color_transfer values this project
// recognizes as HDR content — docs/12's "Video streams" point 3, plus the
// task's own explicit scoping: any other recognized value, or the field
// being absent entirely, classifies as SDR rather than being omitted (no
// Dolby Vision detection — a documented future refinement, not attempted
// here).
var hdrColorTransfers = map[string]bool{
	"smpte2084":    true, // HDR10/HDR10+ (PQ transfer function)
	"arib-std-b67": true, // HLG
}

// dynamicRangeLabel implements docs/12's "Video streams" point 3.
func dynamicRangeLabel(colorTransfer string) string {
	if hdrColorTransfers[strings.ToLower(colorTransfer)] {
		return "HDR"
	}
	return "SDR"
}

// friendlyAudioCodecNames maps a raw codec name to the reference server's
// own friendly display name — docs/12's "Audio streams" point 2 — used only
// when the stream has no usable Profile to show instead.
var friendlyAudioCodecNames = map[string]string{
	"ac3":  "Dolby Digital",
	"eac3": "Dolby Digital+",
	"dca":  "DTS",
	"dts":  "DTS",
}

// friendlyAudioCodecName implements the "else" branch of docs/12's "Audio
// streams" point 2 — any codec with no entry above falls back to its own
// code, uppercased.
func friendlyAudioCodecName(codec string) string {
	if name, ok := friendlyAudioCodecNames[strings.ToLower(codec)]; ok {
		return name
	}
	return strings.ToUpper(codec)
}

// languageNames maps an ISO 639-2/B three-letter language code (the format
// ffprobe emits in a stream's `language` tag) to its English display name —
// docs/12's "Audio streams" point 1 ("its localized display name"; Tinyfin
// has no localization system, so the English name is what's used regardless
// of client locale). Covers the codes common in media libraries; found
// missing via a live comparison against the reference server (see docs/12):
// Tinyfin was using the raw three-letter code ("por") as the attribute
// instead of the language's name ("Portuguese").
var languageNames = map[string]string{
	"por": "Portuguese",
	"eng": "English",
	"jpn": "Japanese",
	"spa": "Spanish",
	"fre": "French",
	"fra": "French",
	"ger": "German",
	"deu": "German",
	"ita": "Italian",
	"kor": "Korean",
	"chi": "Chinese",
	"zho": "Chinese",
	"rus": "Russian",
	"ara": "Arabic",
	"hin": "Hindi",
	"dan": "Danish",
	"dut": "Dutch",
	"nld": "Dutch",
	"swe": "Swedish",
	"nor": "Norwegian",
	"fin": "Finnish",
	"pol": "Polish",
	"tur": "Turkish",
	"gre": "Greek",
	"ell": "Greek",
	"heb": "Hebrew",
	"tha": "Thai",
	"vie": "Vietnamese",
	"ind": "Indonesian",
	"ukr": "Ukrainian",
	"ces": "Czech",
	"cze": "Czech",
	"hun": "Hungarian",
}

// languageName implements docs/12's "Audio streams" point 1 "localized
// display name" — same map-plus-fallback pattern as friendlyAudioCodecName:
// any code with no entry above falls back to its own code, uppercased,
// rather than locking up or guessing at an unknown code's name.
func languageName(code string) string {
	if name, ok := languageNames[strings.ToLower(code)]; ok {
		return name
	}
	return strings.ToUpper(code)
}

// isUsableLanguage reports whether lang is a real language code rather than
// an "unset" placeholder — docs/12's "Audio streams" point 1 ("if set and
// not a placeholder/unset marker"). "und"/"unk" are ffprobe/Matroska's own
// undefined-language codes.
func isUsableLanguage(lang string) bool {
	l := strings.ToLower(strings.TrimSpace(lang))
	return l != "" && l != "und" && l != "unk" && l != "unknown"
}

// isUsableProfile reports whether p is a real, specific codec profile
// rather than the generic value docs/12's "Audio streams" point 2 calls out
// by its own abbreviation ("lc" — AAC's Low Complexity profile, the default
// essentially every AAC stream carries and therefore not worth surfacing on
// its own).
func isUsableProfile(p string) bool {
	p = strings.TrimSpace(p)
	return p != "" && !strings.EqualFold(p, "lc")
}

// capitalizeFirst upper-cases s's first rune, leaving the rest unchanged —
// docs/12's "Audio streams" point 3 calls for a ChannelLayout value like
// ffprobe's own "stereo" to render "Stereo"; a layout like "5.1" (no
// letters) passes through unchanged, same function either way.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// mergeWithTitle implements docs/12's shared "explicit Title" merge rule
// (spelled out under "Video streams" and reused, per its own text, by
// "Audio streams" and "Subtitle streams" too): start from title verbatim,
// then append " - " + each attribute in attrs, skipping any attribute whose
// text is already a substring of title (case-insensitive) — avoids a
// redundant "1080p - 1080p"-style label when the file's own metadata
// already says what an attribute would add.
func mergeWithTitle(title string, attrs []string) string {
	out := title
	lowerTitle := strings.ToLower(title)
	for _, a := range attrs {
		if a == "" {
			continue
		}
		if strings.Contains(lowerTitle, strings.ToLower(a)) {
			continue
		}
		out += " - " + a
	}
	return out
}

// joinAttrs joins the non-empty entries of attrs with sep — the "no
// explicit Title" case for both video (space-separated, docs/12's "Video
// streams" intro) and audio (" - "-separated, docs/12's "Audio streams"
// closing line).
func joinAttrs(attrs []string, sep string) string {
	var parts []string
	for _, a := range attrs {
		if a != "" {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, sep)
}

// videoDisplayTitle implements docs/12's "Video streams" section.
func videoDisplayTitle(s storage.MediaStreamRef) string {
	rangeLabel := dynamicRangeLabel(s.ColorTransfer)
	if t := doviTitle(s.DoVi); t != "" {
		// a Dolby Vision record names the range itself
		rangeLabel = t
	} else if rng, _ := videoRangeOf(s); rng != "" {
		rangeLabel = rng
	}
	attrs := []string{
		resolutionLabel(s.Width, s.Height, s.IsInterlaced),
		strings.ToUpper(s.Codec),
		rangeLabel,
	}
	if s.Title != "" {
		return mergeWithTitle(s.Title, attrs)
	}
	return joinAttrs(attrs, " ")
}

// audioDisplayTitle implements docs/12's "Audio streams" section.
func audioDisplayTitle(s storage.MediaStreamRef) string {
	var attrs []string

	if isUsableLanguage(s.Language) {
		attrs = append(attrs, languageName(s.Language))
	}

	if isUsableProfile(s.Profile) {
		attrs = append(attrs, s.Profile)
	} else {
		attrs = append(attrs, friendlyAudioCodecName(s.Codec))
	}

	switch {
	case s.ChannelLayout != "":
		attrs = append(attrs, capitalizeFirst(s.ChannelLayout))
	case s.Channels != nil:
		attrs = append(attrs, fmt.Sprintf("%d ch", *s.Channels))
	}

	if s.IsDefault {
		attrs = append(attrs, "Default")
	}
	if s.IsExternal {
		attrs = append(attrs, "External")
	}
	if s.IsOriginal {
		attrs = append(attrs, "Original")
	}

	if s.Title != "" {
		return mergeWithTitle(s.Title, attrs)
	}
	return joinAttrs(attrs, " - ")
}

// subtitleDisplayTitle implements docs/12's "Subtitle streams" section —
// recorded there for completeness (same field/mechanism) though out of
// scope for the movie/episode gaps that motivated this file. The attribute
// order is language, hearing impaired, default, forced, codec, external
// (verified against the reference with external files).
func subtitleDisplayTitle(s storage.MediaStreamRef) string {
	var attrs []string

	if isUsableLanguage(s.Language) {
		attrs = append(attrs, subtitle.DisplayName(s.Language))
	} else {
		attrs = append(attrs, "Undefined")
	}
	if s.IsHearingImpaired {
		attrs = append(attrs, "Hearing Impaired")
	}
	if s.IsDefault {
		attrs = append(attrs, "Default")
	}
	if s.IsForced {
		attrs = append(attrs, "Forced")
	}
	if s.Codec != "" {
		attrs = append(attrs, strings.ToUpper(s.Codec))
	}
	if s.IsExternal {
		attrs = append(attrs, "External")
	}

	if s.Title != "" {
		return mergeWithTitle(s.Title, attrs)
	}
	return joinAttrs(attrs, " - ")
}

// displayTitleFor computes MediaStream.DisplayTitle for s — dispatched by
// stream type, see the per-type functions above for docs/12's exact
// algorithm each implements.
func displayTitleFor(s storage.MediaStreamRef) string {
	switch s.Type {
	case storage.StreamTypeVideo:
		return videoDisplayTitle(s)
	case storage.StreamTypeAudio:
		return audioDisplayTitle(s)
	case storage.StreamTypeSubtitle:
		return subtitleDisplayTitle(s)
	default:
		return s.Title
	}
}
