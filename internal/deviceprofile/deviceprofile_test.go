package deviceprofile

import (
	"encoding/json"
	"testing"
)

func TestLookupKnownClients(t *testing.T) {
	for _, client := range []string{"Jellyfin Web", "Jellyfin Android", "Jellyfin iOS"} {
		p, ok := Lookup(client)
		if !ok {
			t.Errorf("Lookup(%q) not found", client)
			continue
		}
		if len(p.DirectPlayProfiles) == 0 {
			t.Errorf("Lookup(%q).DirectPlayProfiles is empty", client)
		}
	}
}

func TestLookupUnknownClientFalls(t *testing.T) {
	_, ok := Lookup("Some Random Client 3000")
	if ok {
		t.Fatal("Lookup(unknown client) = ok, want not found")
	}
}

func TestDefaultIsConservative(t *testing.T) {
	d := Default()
	if len(d.DirectPlayProfiles) != 1 {
		t.Fatalf("len(Default().DirectPlayProfiles) = %d, want 1", len(d.DirectPlayProfiles))
	}
	if d.DirectPlayProfiles[0].Container != "mp4" {
		t.Errorf("Default() container = %q, want %q", d.DirectPlayProfiles[0].Container, "mp4")
	}
}

func TestLookupKnownClientsHaveTranscodingFallback(t *testing.T) {
	for _, client := range []string{"Jellyfin Web", "Jellyfin Android", "Jellyfin iOS"} {
		p, ok := Lookup(client)
		if !ok {
			t.Errorf("Lookup(%q) not found", client)
			continue
		}
		if len(p.TranscodingProfiles) == 0 {
			t.Errorf("Lookup(%q).TranscodingProfiles is empty, want at least one entry", client)
			continue
		}
		tp := p.TranscodingProfiles[0]
		if tp.Container != "ts" || tp.Protocol != "hls" || tp.VideoCodec != "h264" || tp.AudioCodec != "aac" {
			t.Errorf("Lookup(%q).TranscodingProfiles[0] = %+v, want h264/aac over hls/ts baseline", client, tp)
		}
	}
}

func TestDefaultHasTranscodingFallback(t *testing.T) {
	d := Default()
	if len(d.TranscodingProfiles) == 0 {
		t.Fatal("Default().TranscodingProfiles is empty, want at least one entry")
	}
	tp := d.TranscodingProfiles[0]
	if tp.Container != "ts" || tp.Protocol != "hls" || tp.VideoCodec != "h264" || tp.AudioCodec != "aac" {
		t.Errorf("Default().TranscodingProfiles[0] = %+v, want h264/aac over hls/ts baseline", tp)
	}
}

func TestSubtitleProfileMethod(t *testing.T) {
	ok := map[string]string{
		`{"Method":"external"}`: "External", `{"Method":"HLS"}`: "Hls", `{"Method":0}`: "Encode", `{"Method":1}`: "Embed",
		`{"Method":2}`: "External", `{"Method":3}`: "Hls", `{"Method":4}`: "Drop", `{"Method":"2"}`: "External",
		`{"Method":9}`: "", `{}`: "Encode", `{"Format":"srt"}`: "Encode",
	}
	for in, want := range ok {
		var p SubtitleProfile
		if err := json.Unmarshal([]byte(in), &p); err != nil || p.Method != want {
			t.Errorf("%s: %q, %v (want %q)", in, p.Method, err, want)
		}
	}
	for _, in := range []string{`{"Method":""}`, `{"Method":null}`, `{"Method":"Bogus"}`} {
		var p SubtitleProfile
		if err := json.Unmarshal([]byte(in), &p); err == nil {
			t.Errorf("%s: no error", in)
		}
	}
}
