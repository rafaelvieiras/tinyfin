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
		{"range missing", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("EqualsAny", "VideoRangeType", "SDR|HLG", true)}}, []string{"VideoRangeTypeNotSupported"}, ModeTranscode},
		{"width", deviceprofile.CodecProfile{Type: "Video", Codec: "hevc", Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "Width", "1280", true)}}, []string{"VideoResolutionNotSupported"}, ModeTranscode},
		{"other codec", deviceprofile.CodecProfile{Type: "Video", Codec: "h264", Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "Width", "100", true)}}, nil, ModeDirectPlay},
		{"bit depth", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("LessThanEqual", "VideoBitDepth", "8", true)}}, []string{"VideoBitDepthNotSupported"}, ModeTranscode},
		{"absent optional", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("Equals", "VideoProfile", "Main", false)}}, nil, ModeDirectPlay},
		{"absent required", deviceprofile.CodecProfile{Type: "Video", Conditions: []deviceprofile.ProfileCondition{cond("Equals", "VideoProfile", "Main", true)}}, []string{"VideoProfileNotSupported"}, ModeTranscode},
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
	if d.Mode != ModeTranscode || !reflect.DeepEqual(d.Reasons, []string{"ContainerNotSupported", "VideoCodecNotSupported", "AudioCodecNotSupported"}) {
		// the video codec is listed, so only container and audio are named
		if !reflect.DeepEqual(d.Reasons, []string{"ContainerNotSupported", "AudioCodecNotSupported"}) {
			t.Fatalf("mode %v reasons %v", d.Mode, d.Reasons)
		}
	}
}
