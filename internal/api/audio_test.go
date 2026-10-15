package api

import (
	"net/url"
	"testing"
)

func TestPickAudioTarget(t *testing.T) {
	cases := []struct{ container, codec, want string }{
		{"", "", "aac"},
		{"", "mp3", "mp3"},
		{"mp3", "", "mp3"},
		{"aac", "mp3", "aac"}, // a known container wins
		{"opus,mp3", "mp3", "mp3"},
		{"", "vorbis", "ogg"},
		{"M4A", "", "m4a"},
		{"weird", "", "aac"},
	}
	for _, c := range cases {
		if got := pickAudioTarget(c.container, c.codec).container; got != c.want {
			t.Errorf("pickAudioTarget(%q, %q) = %q, want %q", c.container, c.codec, got, c.want)
		}
	}
}

func TestUniversalBitrate(t *testing.T) {
	for _, c := range []struct {
		q    string
		want int
	}{
		{"", 128000},
		{"audiobitrate=64000", 64000},
		{"maxstreamingbitrate=96000", 96000},
		{"audiobitrate=256000&maxstreamingbitrate=96000", 96000},
		{"maxstreamingbitrate=500000", 128000},
	} {
		q, _ := url.ParseQuery(c.q)
		if got := universalBitrate(q); got != c.want {
			t.Errorf("universalBitrate(%q) = %d, want %d", c.q, got, c.want)
		}
	}
}
