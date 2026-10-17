package api

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/guid"
	"github.com/rafaelvieiras/tinyfin/internal/playback"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// orderedQuery is a query string that keeps the order its members were added
// in (url.Values sorts them); the reference writes a fixed order.
type orderedQuery struct{ pairs [][2]string }

func (o *orderedQuery) add(k, v string) { o.pairs = append(o.pairs, [2]string{k, v}) }

// encode writes k=v&k=v with the lists keeping their commas, like the
// reference writes VideoCodec and TranscodeReasons.
func (o *orderedQuery) encode() string {
	var b strings.Builder
	for i, p := range o.pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p[0]) + "=" + strings.ReplaceAll(url.QueryEscape(p[1]), "%2C", ","))
	}
	return b.String()
}

// get returns the first value of k ("" when absent).
func (o *orderedQuery) get(k string) string {
	for _, p := range o.pairs {
		if p[0] == k {
			return p[1]
		}
	}
	return ""
}

// transcodeOffer is what PlaybackInfo hands out for a source that is not
// played as it is: the URL, the container and the protocol of the profile's
// transcoding entry.
type transcodeOffer struct {
	URL, Container, SubProtocol string
}

// offerInput is everything the offer of a transcode depends on.
type offerInput struct {
	item        storage.Item
	profile     deviceprofile.DeviceProfile
	tp          deviceprofile.TranscodingProfile
	reasons     []string
	deviceID    string
	token       string
	playSession string
	audioIdx    *int
	subIdx      *int
	subMethod   string // "Encode", "Hls" or "" (not asked for in the URL)
	maxBitrate  int64
}

// splitCodecs lowercases a comma separated codec list.
func splitCodecs(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// bitDepthOf reads the bit depth of a pixel format name (yuv420p10le -> 10).
func bitDepthOf(pixFmt string) int {
	switch {
	case pixFmt == "":
		return 0
	case strings.Contains(pixFmt, "12le"), strings.Contains(pixFmt, "12be"):
		return 12
	case strings.Contains(pixFmt, "10le"), strings.Contains(pixFmt, "10be"), strings.Contains(pixFmt, "p010"):
		return 10
	}
	return 8
}

// profileSlug is a codec profile as the URL spells it: lower case, no spaces.
func profileSlug(p string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), " ", ""))
}

// conditionLimit finds the value of a LessThanEqual condition on property in
// the codec profiles of type typ that apply to codec (or to any codec).
func conditionLimit(profiles []deviceprofile.CodecProfile, typ, codec, property string) string {
	for _, cp := range profiles {
		if cp.Type != typ {
			continue
		}
		if cp.Codec != "" && !inList(splitCodecs(cp.Codec), codec) {
			continue
		}
		for _, c := range cp.Conditions {
			if strings.EqualFold(c.Property, property) && strings.EqualFold(c.Condition, "LessThanEqual") && c.Value != "" {
				return c.Value
			}
		}
	}
	return ""
}

// buildTranscodeOffer writes the TranscodingUrl of a source the profile
// cannot play as it is, with the members and the order the reference uses.
func buildTranscodeOffer(in offerInput) transcodeOffer {
	hls := strings.EqualFold(in.tp.Protocol, "hls")
	container := splitCodecs(in.tp.Container)
	cont := ""
	if len(container) > 0 {
		cont = container[0]
	}
	if cont == "" {
		cont = map[bool]string{true: "ts", false: "mp4"}[hls]
	}

	var video, audio storage.MediaStreamRef
	var hasAudio bool
	for _, st := range in.item.MediaStreams {
		if st.Type == storage.StreamTypeVideo && video.Codec == "" {
			video = st
		}
	}
	if in.audioIdx != nil {
		for _, st := range in.item.MediaStreams {
			if st.Type == storage.StreamTypeAudio && st.Index == *in.audioIdx {
				audio, hasAudio = st, true
			}
		}
	}
	vc, ac := strings.ToLower(video.Codec), strings.ToLower(audio.Codec)

	vcodecs, acodecs := splitCodecs(in.tp.VideoCodec), splitCodecs(in.tp.AudioCodec)
	if len(vcodecs) == 0 {
		vcodecs = []string{"h264"}
	}
	if len(acodecs) == 0 {
		acodecs = []string{"aac"}
	}
	audioKept := hasAudio && inList(acodecs, ac)
	audioParam := strings.Join(acodecs, ",")
	if audioKept {
		audioParam = ac
	}

	id := string(in.item.ID)
	q := &orderedQuery{}
	if in.deviceID != "" {
		q.add("DeviceId", in.deviceID)
	}
	q.add("MediaSourceId", id)
	q.add("VideoCodec", strings.Join(vcodecs, ","))
	q.add("AudioCodec", audioParam)
	if in.audioIdx != nil {
		q.add("AudioStreamIndex", strconv.Itoa(*in.audioIdx))
	}
	if in.subIdx != nil && (in.subMethod == "Encode" || in.subMethod == "Hls") {
		q.add("SubtitleStreamIndex", strconv.Itoa(*in.subIdx))
	}
	audioBitrate := int64(192000)
	if hasAudio && audio.BitRate != nil && *audio.BitRate > 0 {
		audioBitrate = *audio.BitRate
	}
	if in.maxBitrate > 0 {
		if v := in.maxBitrate - audioBitrate; v > 0 {
			q.add("VideoBitrate", strconv.FormatInt(v, 10))
		}
	}
	if hasAudio || in.audioIdx == nil {
		q.add("AudioBitrate", strconv.FormatInt(audioBitrate, 10))
	}
	if audioKept && audio.SampleRate != nil {
		q.add("AudioSampleRate", strconv.Itoa(*audio.SampleRate))
	}
	if video.FrameRate != nil {
		q.add("MaxFramerate", strconv.FormatFloat(*video.FrameRate, 'f', -1, 64))
	}
	if hls {
		q.add("SegmentContainer", cont)
		if in.tp.SegmentLength > 0 {
			q.add("SegmentLength", strconv.Itoa(in.tp.SegmentLength))
		}
		if in.tp.MinSegments > 0 {
			q.add("MinSegments", strconv.Itoa(in.tp.MinSegments))
		}
	}
	q.add("PlaySessionId", in.playSession)
	if in.token != "" {
		q.add("ApiKey", in.token)
	}
	if hls && in.tp.BreakOnNonKeyFrames {
		q.add("BreakOnNonKeyFrames", "True")
	}
	if in.tp.MaxAudioChannels != "" {
		q.add("TranscodingMaxAudioChannels", in.tp.MaxAudioChannels)
	}
	if in.tp.EnableSubtitlesInManifest {
		q.add("EnableSubtitlesInManifest", "True")
	}
	if in.tp.EnableMpegtsM2TsMode {
		q.add("EnableMpegtsM2TsMode", "True")
	}
	if in.tp.CopyTimestamps {
		q.add("CopyTimestamps", "True")
	}
	q.add("RequireAvc", "false")
	q.add("EnableAudioVbrEncoding", "true")
	sum := md5.Sum([]byte(id + "|" + in.item.Path + "|" + strconv.FormatInt(in.item.Size, 10)))
	q.add("Tag", hex.EncodeToString(sum[:]))
	if in.subIdx != nil && (in.subMethod == "Encode" || in.subMethod == "Hls") {
		q.add("SubtitleMethod", in.subMethod)
	}
	if vc != "" {
		if video.Level != nil {
			q.add(vc+"-level", strconv.Itoa(int(*video.Level)))
		}
		if d := bitDepthOf(video.PixelFormat); d > 0 {
			q.add(vc+"-videobitdepth", strconv.Itoa(d))
		}
		if video.Profile != "" {
			q.add(vc+"-profile", profileSlug(video.Profile))
		}
		if audioKept && audio.Channels != nil {
			q.add(vc+"-audiochannels", strconv.Itoa(*audio.Channels))
		}
	}
	if audioKept {
		if audio.Profile != "" {
			q.add(ac+"-profile", profileSlug(audio.Profile))
		}
		if lim := conditionLimit(in.profile.CodecProfiles, "VideoAudio", ac, "AudioChannels"); lim != "" {
			q.add(ac+"-audiochannels", lim)
		}
	}
	if len(in.reasons) > 0 {
		q.add("TranscodeReasons", strings.Join(in.reasons, ","))
	}

	path := "/videos/" + guid.Dashed(id)
	if hls {
		return transcodeOffer{URL: path + "/master.m3u8?" + q.encode(), Container: cont, SubProtocol: "hls"}
	}
	return transcodeOffer{URL: path + "/stream." + cont + "?" + q.encode(), Container: cont, SubProtocol: "http"}
}

// pickTranscodingProfile is the entry of the profile a video is offered; when
// the profile has none the decision's own container and codecs stand in for it.
func pickTranscodingProfile(profile deviceprofile.DeviceProfile, d playback.Decision) deviceprofile.TranscodingProfile {
	if tp, ok := playback.FirstTranscodingProfile(profile.TranscodingProfiles); ok {
		return tp
	}
	return deviceprofile.TranscodingProfile{Container: d.Container, Type: "Video", Protocol: "hls"}
}
