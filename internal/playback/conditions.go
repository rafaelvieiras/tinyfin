package playback

import (
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// conditionReason names what a failed CodecProfile condition on property
// reports in a transcode reason list.
var conditionReason = map[string]string{
	"width":           "VideoResolutionNotSupported",
	"height":          "VideoResolutionNotSupported",
	"videobitdepth":   "VideoBitDepthNotSupported",
	"videorangetype":  "VideoRangeTypeNotSupported",
	"videoprofile":    "VideoProfileNotSupported",
	"videolevel":      "VideoLevelNotSupported",
	"reframes":        "RefFramesNotSupported",
	"videoframerate":  "VideoFramerateNotSupported",
	"videobitrate":    "VideoBitrateNotSupported",
	"videocodectag":   "VideoCodecTagNotSupported",
	"isinterlaced":    "InterlacedVideoNotSupported",
	"isanamorphic":    "AnamorphicVideoNotSupported",
	"packetlength":    "",
	"numvideostreams": "",
}

// videoProperty is the value of a CodecProfile property of a video stream;
// ok is false when the stream has no value for it.
func videoProperty(v storage.MediaStreamRef, prop string) (string, bool) {
	num := func(n *int) (string, bool) {
		if n == nil {
			return "", false
		}
		return strconv.Itoa(*n), true
	}
	switch strings.ToLower(prop) {
	case "width":
		return num(v.Width)
	case "height":
		return num(v.Height)
	case "videobitdepth":
		return num(v.BitDepth)
	case "reframes":
		return num(v.RefFrames)
	case "videorangetype":
		_, t := storage.VideoRangeOf(v)
		return t, true
	case "videoprofile":
		return v.Profile, v.Profile != ""
	case "videolevel":
		if v.Level == nil {
			return "", false
		}
		return strconv.FormatFloat(*v.Level, 'f', -1, 64), true
	case "videoframerate":
		if v.FrameRate == nil {
			return "", false
		}
		return strconv.FormatFloat(*v.FrameRate, 'f', -1, 64), true
	case "videobitrate":
		if v.BitRate == nil {
			return "", false
		}
		return strconv.FormatInt(*v.BitRate, 10), true
	case "videocodectag":
		return v.CodecTag, v.CodecTag != ""
	case "isinterlaced":
		return strconv.FormatBool(v.IsInterlaced), true
	}
	return "", false
}

// conditionHolds evaluates one ProfileCondition against a stream property.
func conditionHolds(c deviceprofile.ProfileCondition, got string, ok bool) bool {
	if !ok {
		return !c.IsRequired
	}
	switch strings.ToLower(c.Condition) {
	case "equals":
		return strings.EqualFold(got, c.Value)
	case "notequals":
		return !strings.EqualFold(got, c.Value)
	case "equalsany":
		for _, w := range strings.Split(c.Value, "|") {
			if strings.EqualFold(strings.TrimSpace(w), got) {
				return true
			}
		}
		return false
	case "lessthanequal", "greaterthanequal":
		a, e1 := strconv.ParseFloat(got, 64)
		b, e2 := strconv.ParseFloat(c.Value, 64)
		if e1 != nil || e2 != nil {
			return true
		}
		if strings.EqualFold(c.Condition, "LessThanEqual") {
			return a <= b
		}
		return a >= b
	}
	return true
}

// videoConditionFailures lists the reasons for the conditions of the Video
// CodecProfiles of profile (those whose codec and container match, and whose
// ApplyConditions hold) that the video stream does not meet.
func videoConditionFailures(v storage.MediaStreamRef, container string, profile deviceprofile.DeviceProfile) []string {
	var out []string
	add := func(r string) {
		if r == "" {
			return
		}
		for _, o := range out {
			if o == r {
				return
			}
		}
		out = append(out, r)
	}
	for _, cp := range profile.CodecProfiles {
		if !strings.EqualFold(cp.Type, "Video") {
			continue
		}
		if cp.Codec != "" && !containsToken(cp.Codec, normalizeToken(v.Codec)) {
			continue
		}
		if cp.Container != "" && !containsToken(cp.Container, container) {
			continue
		}
		applies := true
		for _, c := range cp.ApplyConditions {
			got, ok := videoProperty(v, c.Property)
			if !conditionHolds(c, got, ok) {
				applies = false
				break
			}
		}
		if !applies {
			continue
		}
		for _, c := range cp.Conditions {
			got, ok := videoProperty(v, c.Property)
			if !conditionHolds(c, got, ok) {
				add(conditionReason[strings.ToLower(c.Property)])
			}
		}
	}
	return out
}

// secondaryAudio reports whether the selected audio stream is not the first
// audio stream of the file, whatever its default flag or codec.
func secondaryAudio(streams []storage.MediaStreamRef, audio storage.MediaStreamRef) bool {
	for _, s := range streams {
		if s.Type == storage.StreamTypeAudio {
			return s.Index != audio.Index
		}
	}
	return false
}

// audioConditionFailed reports whether a condition of the VideoAudio
// CodecProfiles of profile (those whose codec, when set, lists the audio
// codec) on IsSecondaryAudio is not met by the selected audio stream.
func audioConditionFailed(streams []storage.MediaStreamRef, audio storage.MediaStreamRef, profile deviceprofile.DeviceProfile) bool {
	secondary := strconv.FormatBool(secondaryAudio(streams, audio))
	for _, cp := range profile.CodecProfiles {
		if !strings.EqualFold(cp.Type, "VideoAudio") {
			continue
		}
		if cp.Codec != "" && !containsToken(cp.Codec, normalizeToken(audio.Codec)) {
			continue
		}
		for _, c := range cp.Conditions {
			if strings.EqualFold(c.Property, "IsSecondaryAudio") && !conditionHolds(c, secondary, true) {
				return true
			}
		}
	}
	return false
}
