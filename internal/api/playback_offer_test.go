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

func TestBuildTranscodeOfferProfileFlagsAndCopyLimits(t *testing.T) {
	off := false
	o := buildTranscodeOffer(offerInput{
		item: offerItem(), playSession: "ps", noVideoCopy: true, noAudioCopy: true,
		tp: deviceprofile.TranscodingProfile{Container: "ts", Protocol: "hls", BreakOnNonKeyFrames: true, EstimateContentLength: true,
			TranscodeSeekInfo: "Bytes", CopyTimestamps: true, EnableAudioVbrEncoding: &off},
		reasons: []string{"ContainerNotSupported"},
	})
	want := []string{"SegmentContainer=ts", "EstimateContentLength=True", "TranscodeSeekInfo=Bytes", "CopyTimestamps=True", "RequireAvc=false",
		"EnableAudioVbrEncoding=false", "TranscodeReasons=ContainerNotSupported", "allowVideoStreamCopy=false", "allowAudioStreamCopy=false"}
	pos := 0
	for _, w := range want {
		i := strings.Index(o.URL[pos:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order after %d in %s", w, pos, o.URL)
		}
		pos += i + len(w)
	}
	if strings.Contains(o.URL, "BreakOnNonKeyFrames") {
		t.Errorf("the reference never writes BreakOnNonKeyFrames: %s", o.URL)
	}
	o = buildTranscodeOffer(offerInput{item: offerItem(), playSession: "ps", tp: deviceprofile.TranscodingProfile{Container: "ts", Protocol: "hls", TranscodeSeekInfo: "Auto"}})
	if strings.Contains(o.URL, "TranscodeSeekInfo") || strings.Contains(o.URL, "allow") || !strings.Contains(o.URL, "EnableAudioVbrEncoding=true") {
		t.Errorf("defaults: %s", o.URL)
	}
}

func TestMP4SourceName(t *testing.T) {
	prof := deviceprofile.DeviceProfile{DirectPlayProfiles: []deviceprofile.DirectPlayProfile{{Container: "mkv", Type: "Video"}}}
	if got := mp4SourceName("mp4", prof, true); got != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Errorf("no match: %q", got)
	}
	if got := mp4SourceName("mp4", prof, false); got != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Errorf("no profile: %q", got)
	}
	prof.DirectPlayProfiles[0].Container = "mkv,mov"
	if got := mp4SourceName("mp4", prof, true); got != "mov" {
		t.Errorf("match: %q", got)
	}
	if !isMP4Family("M4V") || isMP4Family("mkv") {
		t.Error("family")
	}
}

func TestExtFormatSourceName(t *testing.T) {
	webm := deviceprofile.DeviceProfile{DirectPlayProfiles: []deviceprofile.DirectPlayProfile{{Container: "webm", Type: "Video"}}}
	for ext, want := range map[string]string{"mkv": "mkv", "m2ts": "ts", "ts": "ts", "wmv": "asf", "mpg": "mpeg", "ogv": "ogg", "mp4": ""} {
		if got := extFormatSourceName(ext, deviceprofile.DeviceProfile{}); got != want {
			t.Errorf("%s: %q, want %q", ext, got, want)
		}
	}
	if got := extFormatSourceName("webm", deviceprofile.DeviceProfile{}); got != "mkv,webm" {
		t.Errorf("webm: %q", got)
	}
	if got := extFormatSourceName("webm", webm); got != "webm" {
		t.Errorf("webm with profile: %q", got)
	}
}

func TestFormatSourceName(t *testing.T) {
	webm := deviceprofile.DeviceProfile{DirectPlayProfiles: []deviceprofile.DirectPlayProfile{{Container: "webm", Type: "Video"}}}
	if got := formatSourceName("mkv,webm", deviceprofile.DeviceProfile{}); got != "mkv,webm" {
		t.Errorf("no profile: %q", got)
	}
	if got := formatSourceName("mkv,webm", webm); got != "webm" {
		t.Errorf("webm with profile: %q", got)
	}
	if got := formatSourceName("ts", webm); got != "ts" {
		t.Errorf("single name: %q", got)
	}
	if got := formatSourceName("mov,mp4,m4a,3gp,3g2,mj2", webm); got != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Errorf("no listed name: %q", got)
	}
}
