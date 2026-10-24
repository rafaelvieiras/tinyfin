package api

import (
	"net/url"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestSessionRegistryTranscodeLifecycle(t *testing.T) {
	r := newSessionRegistry()
	r.upsertCapabilities("dev", "u", "user", "c", "d", "1", "", sessionCapabilitiesDto{})
	if got := r.list("srv", 0, false); len(got) != 1 || got[0].TranscodingInfo != nil {
		t.Fatalf("before a job: %+v", got)
	}
	r.setTranscode("dev", "k", "item", "ps", &transcodingInfoDto{VideoCodec: "h264"})
	if got := r.list("srv", 0, false); got[0].TranscodingInfo == nil || got[0].TranscodingInfo.VideoCodec != "h264" {
		t.Fatalf("after a job: %+v", got[0].TranscodingInfo)
	}
	if _, ok := r.transcodeKey("dev", "item", "other"); ok {
		t.Fatal("another play session must not match")
	}
	r.clearTranscodeInfo("dev")
	if got := r.list("srv", 0, false); got[0].TranscodingInfo != nil {
		t.Fatal("cleared info must be hidden")
	}
	if k, ok := r.transcodeKey("dev", "item", "PS"); !ok || k != "k" {
		t.Fatalf("the job stays known after the info is hidden: %q %v", k, ok)
	}
	r.dropTranscode("dev", storage.ItemID("another"))
	if _, ok := r.transcodeKey("dev", "item", ""); !ok {
		t.Fatal("stopping another item keeps the stream")
	}
	r.dropTranscode("dev", "item")
	if _, ok := r.transcodeKey("dev", "item", ""); ok {
		t.Fatal("stopped item drops the stream")
	}
}

func TestTranscodingInfoReasons(t *testing.T) {
	q := url.Values{"transcodereasons": {"A, B,,C"}}
	info := transcodingInfoFor(hlsRequest{}, q)
	if len(info.TranscodeReasons) != 3 || info.TranscodeReasons[1] != "B" {
		t.Fatalf("%+v", info)
	}
}
