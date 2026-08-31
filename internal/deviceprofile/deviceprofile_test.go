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
