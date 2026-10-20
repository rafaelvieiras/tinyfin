package playback

import (
	"reflect"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func ip(n int) *int { return &n }

func condProfile(cps ...deviceprofile.CodecProfile) deviceprofile.DeviceProfile {
	return deviceprofile.DeviceProfile{
		DirectPlayProfiles:  []deviceprofile.DirectPlayProfile{{Container: "mkv", Type: "Video", VideoCodec: "hevc", AudioCodec: "aac"}},
		TranscodingProfiles: []deviceprofile.TranscodingProfile{{Container: "ts", Type: "Video", Protocol: "hls", VideoCodec: "h264", AudioCodec: "aac"}},
		CodecProfiles:       cps,
	}
}

func condItem() storage.Item {
	return storage.Item{Container: "mkv", MediaStreams: []storage.MediaStreamRef{
		{Index: 0, Type: storage.StreamTypeVideo, Codec: "hevc", Width: ip(1920), BitDepth: ip(10), ColorTransfer: "smpte2084"},
		{Index: 1, Type: storage.StreamTypeAudio, Codec: "aac", IsDefault: true},
	}}
}

func TestVideoConditions(t *testing.T) {
	cond := func(c, p, v string, req bool) deviceprofile.ProfileCondition {
		return deviceprofile.ProfileCondition{Condition: c, Property: p, Value: v, IsRequired: req}
	}
	cases := []struct {
		name string
		cp   deviceprofile.CodecProfile
		want []string
		mode Mode
	}{
		{"range listed", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("EqualsAny", "VideoRangeType", "SDR|HDR10", true)}}, nil, ModeDirectPlay},
		{"range missing", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("EqualsAny", "VideoRangeType", "SDR|HLG", true)}}, []string{"VideoCodecNotSupported", "VideoRangeTypeNotSupported"}, ModeTranscode},
		{"width", deviceprofile.CodecProfile{Type: "Video", Codec: "hevc", Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "Width", "1280", true)}}, []string{"VideoCodecNotSupported", "VideoResolutionNotSupported"}, ModeTranscode},
		{"other codec", deviceprofile.CodecProfile{Type: "Video", Codec: "h264", Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "Width", "100", true)}}, nil, ModeDirectPlay},
		{"bit depth", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "VideoBitDepth", "8", true)}}, []string{"VideoCodecNotSupported", "VideoBitDepthNotSupported"}, ModeTranscode},
		{"absent optional", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("Equals", "VideoProfile", "Main", false)}}, nil, ModeDirectPlay},
		{"absent required", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("Equals", "VideoProfile", "Main", true)}}, []string{"VideoCodecNotSupported", "VideoProfileNotSupported"}, ModeTranscode},
		{"apply conditions off", deviceprofile.CodecProfile{Type: "Video", ApplyConditions: []deviceprofile.ProfileCondition{cond("Equals", "Width", "640", true)}, Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "Width", "100", true)}}, nil, ModeDirectPlay},
	}
	for _, c := range cases {
		d := Decide(condItem(), condProfile(c.cp))
		if d.Mode != c.mode || !reflect.DeepEqual(d.Reasons, c.want) {
			t.Errorf("%s: mode %v reasons %v, want %v %v", c.name, d.Mode, d.Reasons, c.mode, c.want)
		}
	}
}

func TestNoDefaultAudioDoesNotRestrictDirectPlay(t *testing.T) {
	it := condItem()
	it.MediaStreams[1].IsDefault = false
	p := condProfile()
	p.DirectPlayProfiles[0].AudioCodec = "ac3"
	if d := Decide(it, p); d.Mode != ModeDirectPlay {
		t.Fatalf("mode %v, want direct play", d.Mode)
	}
	p.DirectPlayProfiles[0].Container = "mp4"
	d := Decide(it, p)
	// the transcoding profile makes h264, the source is hevc: the picture is named too
	if want := []string{"ContainerNotSupported", "VideoCodecNotSupported", "AudioCodecNotSupported"}; !reflect.DeepEqual(d.Reasons, want) {
		t.Fatalf("mode %v reasons %v, want %v", d.Mode, d.Reasons, want)
	}
}

// A transcode also names a codec that no transcoding profile meant for
// streaming produces (the reference does, although the direct play entry lists
// it); the sound is looked up among the profiles that take the picture.
func TestTranscodeCodecReasons(t *testing.T) {
	width := deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{{Condition: "LessThanEqual", Property: "Width", Value: "100", IsRequired: true}}}
	tp := func(ctx, vc, ac string) deviceprofile.TranscodingProfile {
		return deviceprofile.TranscodingProfile{Container: "ts", Type: "Video", Protocol: "hls", Context: ctx, VideoCodec: vc, AudioCodec: ac}
	}
	for name, c := range map[string]struct {
		tcs  []deviceprofile.TranscodingProfile
		want []string
	}{
		"produced":             {[]deviceprofile.TranscodingProfile{tp("Streaming", "h264,hevc", "aac")}, []string{"VideoResolutionNotSupported"}},
		"empty lists":          {[]deviceprofile.TranscodingProfile{tp("Streaming", "", "")}, []string{"VideoResolutionNotSupported"}},
		"other picture":        {[]deviceprofile.TranscodingProfile{tp("Streaming", "vp9", "aac")}, []string{"VideoCodecNotSupported", "VideoResolutionNotSupported"}},
		"static ignored":       {[]deviceprofile.TranscodingProfile{tp("Static", "hevc", "aac"), tp("Streaming", "vp9", "aac")}, []string{"VideoCodecNotSupported", "VideoResolutionNotSupported"}},
		"other sound":          {[]deviceprofile.TranscodingProfile{tp("Streaming", "hevc", "opus")}, []string{"AudioCodecNotSupported", "VideoResolutionNotSupported"}},
		"sound of other entry": {[]deviceprofile.TranscodingProfile{tp("Streaming", "hevc", "opus"), tp("Streaming", "h264", "aac")}, []string{"AudioCodecNotSupported", "VideoResolutionNotSupported"}},
		"picture and sound":    {[]deviceprofile.TranscodingProfile{tp("Streaming", "vp9", "opus")}, []string{"VideoCodecNotSupported", "AudioCodecNotSupported", "VideoResolutionNotSupported"}},
	} {
		p := condProfile(width)
		p.TranscodingProfiles = c.tcs
		if d := Decide(condItem(), p); !reflect.DeepEqual(d.Reasons, c.want) {
			t.Errorf("%s: %v, want %v", name, d.Reasons, c.want)
		}
	}
}
