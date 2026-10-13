package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/guid"
)

// The request-side types of a user Configuration object. They implement the
// validation rules of users.configuration.R7-R11: JSON null is refused for
// booleans, the subtitle mode and the id lists; the subtitle mode is a lenient
// enum; string members take numbers and booleans as their literal text.

var errConfigMember = errors.New("invalid configuration member")

// strictBool accepts only the JSON literals true and false. It is used as a
// value (not a pointer) so that a JSON null reaches UnmarshalJSON and is
// refused instead of being read as "unset".
type strictBool struct {
	Set bool
	Val bool
}

func (b *strictBool) UnmarshalJSON(raw []byte) error {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		*b = strictBool{Set: true, Val: true}
	case "false":
		*b = strictBool{Set: true}
	default:
		return errConfigMember
	}
	return nil
}

// guidList is a JSON array whose entries are GUIDs in any spelling; an
// entry that is JSON null stands for the all-zero GUID, anything else that is
// not a GUID is refused. A null list is refused too.
type guidList struct {
	Set bool
	Val []string
}

func (l *guidList) UnmarshalJSON(raw []byte) error {
	var entries []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errConfigMember
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return errConfigMember
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if bytes.Equal(bytes.TrimSpace(e), []byte("null")) {
			out = append(out, guid.Zero)
			continue
		}
		var s string
		if err := json.Unmarshal(e, &s); err != nil {
			return errConfigMember
		}
		n, ok := parseConfigGUID(s)
		if !ok {
			return errConfigMember
		}
		out = append(out, n)
	}
	*l = guidList{Set: true, Val: out}
	return nil
}

var (
	braceGUIDRe = regexp.MustCompile(`^(?:\{([0-9a-fA-F-]{36})\}|\(([0-9a-fA-F-]{36})\))$`)
	hexGUIDRe   = regexp.MustCompile(`^\{0x([0-9a-fA-F]{1,8}),0x([0-9a-fA-F]{1,4}),0x([0-9a-fA-F]{1,4}),\{0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2}),0x([0-9a-fA-F]{1,2})\}\}$`)
)

// parseConfigGUID folds a GUID in any spelling the reference accepts in a
// configuration list to the canonical form: 32 digits, dashed, dashed inside
// braces or parentheses, or the braced hexadecimal-groups form, with
// surrounding whitespace ignored.
func parseConfigGUID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := braceGUIDRe.FindStringSubmatch(s); m != nil {
		s = m[1] + m[2]
		if len(s) != 36 {
			return "", false
		}
	}
	if m := hexGUIDRe.FindStringSubmatch(s); m != nil {
		widths := []int{8, 4, 4, 2, 2, 2, 2, 2, 2, 2, 2}
		var b strings.Builder
		for i, part := range m[1:] {
			b.WriteString(strings.Repeat("0", widths[i]-len(part)) + part)
		}
		s = b.String()
	}
	return guid.Normalize(s)
}

// subtitleModeNames are the enum's named values, indexed by number.
var subtitleModeNames = []string{"Default", "Always", "OnlyForced", "None", "Smart"}

// subtitleModeValue is the subtitle mode as it is kept and shown: the name
// for a defined value, the plain number for any other int32.
type subtitleModeValue string

func (m subtitleModeValue) MarshalJSON() ([]byte, error) {
	if _, err := strconv.ParseInt(string(m), 10, 32); err == nil {
		return []byte(m), nil
	}
	return json.Marshal(string(m))
}

func subtitleModeFromNumber(n int64) subtitleModeValue {
	if n >= 0 && int(n) < len(subtitleModeNames) {
		return subtitleModeValue(subtitleModeNames[n])
	}
	return subtitleModeValue(strconv.FormatInt(n, 10))
}

var signedIntRe = regexp.MustCompile(`^[+-]?[0-9]+$`)

// parseSubtitleMode implements the lenient enum read of users.configuration.R9.
func parseSubtitleMode(s string) (subtitleModeValue, bool) {
	s = strings.TrimSpace(s)
	if signedIntRe.MatchString(s) {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return "", false
		}
		return subtitleModeFromNumber(n), true
	}
	parts := strings.Split(s, ",")
	if len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	var n int64
	for _, p := range parts {
		p = strings.TrimSpace(p)
		found := false
		for i, name := range subtitleModeNames {
			if strings.EqualFold(p, name) {
				n |= int64(i)
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	return subtitleModeFromNumber(n), true
}

// subtitleModeMember is the request-side subtitle mode (a value, so that a
// JSON null is refused rather than read as unset).
type subtitleModeMember struct {
	Set bool
	Val subtitleModeValue
}

func (m *subtitleModeMember) UnmarshalJSON(raw []byte) error {
	var v subtitleModeValue
	if err := v.UnmarshalJSON(raw); err != nil {
		return err
	}
	*m = subtitleModeMember{Set: true, Val: v}
	return nil
}

func (m *subtitleModeValue) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == 'n' {
		return errConfigMember
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return errConfigMember
		}
		v, ok := parseSubtitleMode(s)
		if !ok {
			return errConfigMember
		}
		*m = v
		return nil
	}
	// A JSON number must be an integer literal in the int32 range.
	if !signedIntRe.Match(raw) || raw[0] == '+' {
		return errConfigMember
	}
	n, err := strconv.ParseInt(string(raw), 10, 32)
	if err != nil {
		return errConfigMember
	}
	*m = subtitleModeFromNumber(n)
	return nil
}

// lenientString reads a string member: a string as is, a number or a boolean
// as its literal text, null as unset; arrays and objects are refused.
type lenientString struct {
	Set bool
	Val string
}

func (s *lenientString) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return errConfigMember
	}
	switch raw[0] {
	case 'n':
		*s = lenientString{}
	case '"':
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return errConfigMember
		}
		*s = lenientString{Set: true, Val: v}
	case '[', '{':
		return errConfigMember
	default: // number, true, false: the literal text
		*s = lenientString{Set: true, Val: string(raw)}
	}
	return nil
}

// userConfigRequest is a Configuration object as posted.
type userConfigRequest struct {
	AudioLanguagePreference    lenientString      `json:"AudioLanguagePreference"`
	PlayDefaultAudioTrack      strictBool         `json:"PlayDefaultAudioTrack"`
	SubtitleLanguagePreference lenientString      `json:"SubtitleLanguagePreference"`
	DisplayMissingEpisodes     strictBool         `json:"DisplayMissingEpisodes"`
	GroupedFolders             guidList           `json:"GroupedFolders"`
	SubtitleMode               subtitleModeMember `json:"SubtitleMode"`
	DisplayCollectionsView     strictBool         `json:"DisplayCollectionsView"`
	EnableLocalPassword        strictBool         `json:"EnableLocalPassword"`
	OrderedViews               guidList           `json:"OrderedViews"`
	LatestItemsExcludes        guidList           `json:"LatestItemsExcludes"`
	MyMediaExcludes            guidList           `json:"MyMediaExcludes"`
	HidePlayedInLatest         strictBool         `json:"HidePlayedInLatest"`
	RememberAudioSelections    strictBool         `json:"RememberAudioSelections"`
	RememberSubtitleSelections strictBool         `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  strictBool         `json:"EnableNextEpisodeAutoPlay"`
	CastReceiverId             lenientString      `json:"CastReceiverId"`
}

// parseUserConfigRequest validates raw (a body, or the Configuration member
// of a user object) and applies it onto the defaults. An empty
// CastReceiverId in the result means "keep the stored receiver".
func parseUserConfigRequest(raw []byte) (userConfigurationDto, bool) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	c := defaultUserConfiguration()
	c.CastReceiverId = ""
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return c, false
	}
	var req userConfigRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return c, false
	}
	setBool := func(dst *bool, v strictBool) {
		if v.Set {
			*dst = v.Val
		}
	}
	setList := func(dst *[]string, v guidList) {
		if v.Set {
			*dst = v.Val
		}
	}
	if req.AudioLanguagePreference.Set {
		v := req.AudioLanguagePreference.Val
		c.AudioLanguagePreference = &v
	}
	c.SubtitleLanguagePreference = req.SubtitleLanguagePreference.Val
	setBool(&c.PlayDefaultAudioTrack, req.PlayDefaultAudioTrack)
	setBool(&c.DisplayMissingEpisodes, req.DisplayMissingEpisodes)
	setBool(&c.DisplayCollectionsView, req.DisplayCollectionsView)
	setBool(&c.EnableLocalPassword, req.EnableLocalPassword)
	setBool(&c.HidePlayedInLatest, req.HidePlayedInLatest)
	setBool(&c.RememberAudioSelections, req.RememberAudioSelections)
	setBool(&c.RememberSubtitleSelections, req.RememberSubtitleSelections)
	setBool(&c.EnableNextEpisodeAutoPlay, req.EnableNextEpisodeAutoPlay)
	setList(&c.GroupedFolders, req.GroupedFolders)
	setList(&c.OrderedViews, req.OrderedViews)
	setList(&c.LatestItemsExcludes, req.LatestItemsExcludes)
	setList(&c.MyMediaExcludes, req.MyMediaExcludes)
	if req.SubtitleMode.Set {
		c.SubtitleMode = req.SubtitleMode.Val
	}
	// users.prefs.R1-R2: CastReceiverId is only taken when it names a known
	// receiver; otherwise it stays empty and the caller keeps the stored one.
	if req.CastReceiverId.Set && knownCastReceiverIDs[req.CastReceiverId.Val] {
		c.CastReceiverId = req.CastReceiverId.Val
	}
	c.normalizeLists()
	return c, true
}
