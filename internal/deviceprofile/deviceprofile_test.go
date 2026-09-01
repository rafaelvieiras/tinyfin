package deviceprofile

import "testing"

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
