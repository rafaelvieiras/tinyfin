package playback

import (
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/deviceprofile"
	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func sub(index int, lang string, ext, def, forced bool) storage.MediaStreamRef {
	return storage.MediaStreamRef{Index: index, Type: storage.StreamTypeSubtitle, Codec: "subrip", Language: lang,
		IsExternal: ext, IsDefault: def, IsForced: forced}
}

func idx(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// The selections below were observed on the reference with the same stream
// layouts.
func TestDefaultSubtitleIndex(t *testing.T) {
	mixed := []storage.MediaStreamRef{ // external de forced, en; embedded eng default, por, spa forced
		sub(0, "deu", true, false, true), sub(1, "eng", true, false, false),
		{Index: 2, Type: storage.StreamTypeVideo}, {Index: 3, Type: storage.StreamTypeAudio, Language: "eng"},
		sub(4, "eng", false, true, false), sub(5, "por", false, false, false), sub(6, "spa", false, false, true),
	}
	v8 := []storage.MediaStreamRef{sub(0, "aar", true, false, true), sub(1, "eng", true, true, true), sub(2, "", true, false, false)}
	onlyForced := []storage.MediaStreamRef{sub(0, "eng", true, false, true)}
	embedded := []storage.MediaStreamRef{sub(2, "eng", false, true, false), sub(3, "por", false, false, false), sub(4, "spa", false, false, true)}
	plain := []storage.MediaStreamRef{sub(0, "deu", true, false, false), sub(1, "eng", true, false, false)}
	cases := []struct {
		name      string
		streams   []storage.MediaStreamRef
		mode      string
		langs     []string
		audioLang string
		want      int
	}{
		{"default: non-forced external over forced", mixed, "Default", nil, "eng", 1},
		{"default: default+forced", v8, "Default", nil, "", 1},
		{"default: only forced", onlyForced, "Default", nil, "", 0},
		{"default: embedded default", embedded, "Default", nil, "", 2},
		{"default: first external", plain, "Default", nil, "", 0},
		{"default with language: forced external first", mixed, "Default", []string{"spa"}, "eng", 0},
		{"none", mixed, "None", nil, "", -1},
		{"always", mixed, "Always", nil, "", 1},
		{"always with language", mixed, "Always", []string{"spa"}, "", 6},
		{"always, language absent", plain, "Always", []string{"spa"}, "", -1},
		{"onlyforced", mixed, "OnlyForced", nil, "", 0},
		{"onlyforced with language", mixed, "OnlyForced", []string{"spa"}, "", 6},
		{"smart with audio in the preferred language", mixed, "Smart", []string{"eng"}, "eng", -1},
		{"smart, other audio", plain, "Smart", []string{"eng"}, "jpn", 1},
	}
	for _, c := range cases {
		if got := idx(DefaultSubtitleIndex(c.streams, c.mode, c.langs, c.audioLang)); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestDeliverSubtitle(t *testing.T) {
	srt := sub(0, "eng", true, false, false)
	embSRT := sub(4, "eng", false, false, false)
	ass := storage.MediaStreamRef{Index: 5, Type: storage.StreamTypeSubtitle, Codec: "ass", Language: "por"}
	pgs := storage.MediaStreamRef{Index: 6, Type: storage.StreamTypeSubtitle, Codec: "PGSSUB", IsExternal: true}
	vtt := storage.MediaStreamRef{Index: 7, Type: storage.StreamTypeSubtitle, Codec: "webvtt", IsExternal: true}
	ext := func(f string) deviceprofile.SubtitleProfile {
		return deviceprofile.SubtitleProfile{Format: f, Method: "External"}
	}
	emb := func(f string) deviceprofile.SubtitleProfile {
		return deviceprofile.SubtitleProfile{Format: f, Method: "Embed"}
	}
	cases := []struct {
		name     string
		s        storage.MediaStreamRef
		profiles []deviceprofile.SubtitleProfile
		embed    bool
		method   string
		format   string
	}{
		{"no profile", srt, nil, true, "Encode", ""},
		{"srt as vtt", srt, []deviceprofile.SubtitleProfile{ext("vtt"), ext("srt")}, true, "External", "vtt"},
		{"ass keeps ass", ass, []deviceprofile.SubtitleProfile{ext("vtt"), ext("ass")}, true, "External", "ass"},
		{"ass is not converted", ass, []deviceprofile.SubtitleProfile{ext("vtt"), ext("ssa")}, true, "Encode", ""},
		{"bitmap", pgs, []deviceprofile.SubtitleProfile{ext("vtt")}, true, "Encode", ""},
		{"vtt as srt", vtt, []deviceprofile.SubtitleProfile{ext("srt")}, true, "External", "srt"},
		{"embed an embedded track", embSRT, []deviceprofile.SubtitleProfile{emb("srt")}, true, "Embed", ""},
		{"embed does not apply to a file", srt, []deviceprofile.SubtitleProfile{emb("srt")}, true, "Encode", ""},
		{"embed when transcoding", embSRT, []deviceprofile.SubtitleProfile{emb("srt")}, false, "Encode", ""},
		{"hls", srt, []deviceprofile.SubtitleProfile{{Format: "vtt", Method: "Hls"}}, true, "Hls", "vtt"},
		{"language filter", srt, []deviceprofile.SubtitleProfile{{Format: "srt", Method: "External", Language: "por"}}, true, "Encode", ""},
		{"language filter, matching", srt, []deviceprofile.SubtitleProfile{{Format: "srt", Method: "External", Language: "eng"}}, true, "External", "srt"},
	}
	for _, c := range cases {
		got := DeliverSubtitle(c.s, c.profiles, c.embed)
		if got.Method != c.method || got.Format != c.format {
			t.Errorf("%s: got %+v, want %s/%s", c.name, got, c.method, c.format)
		}
	}
}

func TestSubtitleScore(t *testing.T) {
	cases := []struct {
		s    storage.MediaStreamRef
		want int
	}{
		{storage.MediaStreamRef{Codec: "subrip", IsExternal: true}, 111222},
		{storage.MediaStreamRef{Codec: "subrip", IsExternal: true, IsForced: true}, 121222},
		{storage.MediaStreamRef{Codec: "subrip", IsExternal: true, IsDefault: true}, 112222},
		{storage.MediaStreamRef{Codec: "subrip", IsExternal: true, IsDefault: true, IsForced: true}, 122222},
		{storage.MediaStreamRef{Codec: "subrip", IsDefault: true}, 112221},
		{storage.MediaStreamRef{Codec: "PGSSUB", IsExternal: true}, 111212},
	}
	for _, c := range cases {
		if got := SubtitleScore(c.s); got != c.want {
			t.Errorf("%+v: %d, want %d", c.s, got, c.want)
		}
	}
}
