package playback

import (
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestPickTranscodingProfile(t *testing.T) {
	e := func(c, v, a string) deviceprofile.TranscodingProfile {
		return deviceprofile.TranscodingProfile{Container: c, Type: "Video", VideoCodec: v, AudioCodec: a}
	}
	for _, c := range []struct {
		name     string
		entries  []deviceprofile.TranscodingProfile
		vc, ac   string
		wantCont string
	}{
		{"first takes both", []deviceprofile.TranscodingProfile{e("mp4", "h264", "aac"), e("ts", "h264", "aac,mp3")}, "h264", "aac", "mp4"},
		{"sound picks the later", []deviceprofile.TranscodingProfile{e("mp4", "h264", "aac"), e("ts", "h264", "aac,mp3")}, "h264", "mp3", "ts"},
		{"picture before sound", []deviceprofile.TranscodingProfile{e("mp4", "h264", "opus"), e("ts", "hevc", "aac")}, "h264", "aac", "mp4"},
		{"no picture match keeps the first", []deviceprofile.TranscodingProfile{e("mp4", "vp9", "aac"), e("ts", "av1", "aac")}, "h264", "aac", "mp4"},
	} {
		got, ok := PickTranscodingProfile(c.entries, c.vc, c.ac)
		if !ok || got.Container != c.wantCont {
			t.Errorf("%s: got %q ok=%v, want %q", c.name, got.Container, ok, c.wantCont)
		}
	}
	if _, ok := PickTranscodingProfile([]deviceprofile.TranscodingProfile{{Type: "Video", Context: "Static"}}, "h264", "aac"); ok {
		t.Error("a Static entry must be skipped")
	}
}

func TestSecondaryAudioCondition(t *testing.T) {
	streams := []storage.MediaStreamRef{
		{Index: 0, Type: storage.StreamTypeVideo, Codec: "h264"},
		{Index: 1, Type: storage.StreamTypeAudio, Codec: "aac"},
		{Index: 2, Type: storage.StreamTypeAudio, Codec: "mp3", IsDefault: true},
	}
	profile := func(codec, cond, val string) deviceprofile.DeviceProfile {
		return deviceprofile.DeviceProfile{CodecProfiles: []deviceprofile.CodecProfile{{Type: "VideoAudio", Codec: codec,
			Conditions: []deviceprofile.ProfileCondition{{Condition: cond, Property: "IsSecondaryAudio", Value: val}}}}}
	}
	if audioConditionFailed(streams, streams[1], profile("", "Equals", "false")) {
		t.Error("the first track is not secondary")
	}
	if !audioConditionFailed(streams, streams[2], profile("", "Equals", "false")) {
		t.Error("a later track is secondary, even when flagged default")
	}
	if audioConditionFailed(streams, streams[2], profile("aac", "Equals", "false")) {
		t.Error("an entry for another codec does not apply")
	}
	if !audioConditionFailed(streams, streams[1], profile("", "Equals", "True")) {
		t.Error("Equals true refuses the first track, value in any case")
	}
}
