package api

import (
	"strings"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func offerItem() storage.Item {
	lvl, ch, sr, fr := 41.0, 2, 48000, 24.0
	return storage.Item{
		ID: "805043736e121100e8276ccc8c873cac", Container: "mkv",
		MediaStreams: []storage.MediaStreamRef{
			{Index: 0, Type: storage.StreamTypeVideo, Codec: "h264", Profile: "Constrained Baseline", Level: &lvl, PixelFormat: "yuv420p", FrameRate: &fr},
			{Index: 1, Type: storage.StreamTypeAudio, Codec: "aac", Profile: "LC", Channels: &ch, SampleRate: &sr},
		},
	}
}

func TestBuildTranscodeOfferHLSOrder(t *testing.T) {
	one := 1
	o := buildTranscodeOffer(offerInput{
		item: offerItem(), tp: deviceprofile.TranscodingProfile{Container: "ts", VideoCodec: "hevc,h264", AudioCodec: "mp3,aac", Protocol: "hls", MinSegments: 2, MaxAudioChannels: "2"},
		reasons: []string{"ContainerNotSupported"}, deviceID: "dev", token: "tok", playSession: "ps", audioIdx: &one, maxBitrate: 120000000,
	})
	if o.SubProtocol != "hls" || o.Container != "ts" {
		t.Fatalf("offer = %+v", o)
	}
	want := []string{"DeviceId=dev", "MediaSourceId=", "VideoCodec=hevc,h264", "AudioCodec=aac", "AudioStreamIndex=1", "VideoBitrate=119808000", "AudioBitrate=192000",
		"AudioSampleRate=48000", "MaxFramerate=24", "SegmentContainer=ts", "MinSegments=2", "PlaySessionId=ps", "ApiKey=tok", "TranscodingMaxAudioChannels=2",
		"RequireAvc=false", "EnableAudioVbrEncoding=true", "Tag=", "h264-level=41", "h264-videobitdepth=8", "h264-profile=constrainedbaseline", "h264-audiochannels=2", "aac-profile=lc",
		"TranscodeReasons=ContainerNotSupported"}
	pos := 0
	for _, w := range want {
		i := strings.Index(o.URL[pos:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order after %d in %s", w, pos, o.URL)
		}
		pos += i + len(w)
	}
	if !strings.HasPrefix(o.URL, "/videos/80504373-6e12-1100-e827-6ccc8c873cac/master.m3u8?") {
		t.Errorf("path = %s", o.URL)
	}
}

func TestBuildTranscodeOfferProgressive(t *testing.T) {
	o := buildTranscodeOffer(offerInput{item: offerItem(), tp: deviceprofile.TranscodingProfile{Container: "mkv", Protocol: "http"}, playSession: "ps"})
	if o.SubProtocol != "http" || o.Container != "mkv" || !strings.Contains(o.URL, "/stream.mkv?") || strings.Contains(o.URL, "SegmentContainer") {
		t.Fatalf("offer = %+v", o)
	}
}
