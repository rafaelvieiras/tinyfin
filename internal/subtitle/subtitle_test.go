package subtitle

import (
	"sort"
	"strings"
	"testing"
)

func TestParseSidecar(t *testing.T) {
	cases := []struct {
		file               string
		ok                 bool
		lang, title, codec string
		forced, deflt, hi  bool
	}{
		{"Movie.srt", true, "", "", "subrip", false, false, false},
		{"movie.EN.SRT", true, "eng", "", "subrip", false, false, false},
		{"Movie.pt-BR.srt", true, "pt-br", "", "subrip", false, false, false},
		{"Movie.por-BR.srt", true, "", "por-BR", "subrip", false, false, false},
		{"Movie.pt_BR.srt", true, "", "pt_BR", "subrip", false, false, false},
		{"Movie.pob.srt", true, "", "pob", "subrip", false, false, false},
		{"Movie.es.forced.srt", true, "spa", "", "subrip", true, false, false},
		{"Movie.de.forced.default.sdh.srt", true, "deu", "", "subrip", true, true, true},
		{"Movie.SDH.en.srt", true, "eng", "", "subrip", false, false, true},
		{"Movie.en.cc.srt", true, "eng", "", "subrip", false, false, true},
		{"Movie.hearing impaired.srt", true, "", "hearing impaired", "subrip", false, false, false},
		{"Movie.en.Signs and Songs.srt", true, "eng", "Signs and Songs", "subrip", false, false, false},
		{"Movie.English.ssa", true, "eng", "", "ass", false, false, false},
		{"Movie.Spanish.srt", true, "", "Spanish", "subrip", false, false, false},
		{"Movie.Dutch.srt", true, "", "Dutch", "subrip", false, false, false},
		{"Movie.French.srt", true, "fra", "", "subrip", false, false, false},
		{"Movie.chi.srt", true, "zho", "", "subrip", false, false, false},
		{"Movie.nb.srt", true, "nob", "", "subrip", false, false, false},
		{"Movie.zh-Hans.srt", true, "zh-hans", "", "subrip", false, false, false},
		{"Movie.en.1.srt", true, "eng", "1", "subrip", false, false, false},
		{"Movie.ru.sup", true, "rus", "", "PGSSUB", false, false, false},
		{"Movie.ko.smi", true, "kor", "", "sami", false, false, false},
		{"Movie.en.vtt", true, "eng", "", "webvtt", false, false, false},
		{"Movie 2.srt", false, "", "", "", false, false, false},
		{"Movie-en.srt", false, "", "", "", false, false, false},
		{"Other.srt", false, "", "", "", false, false, false},
		{"Movie.en.txt", false, "", "", "", false, false, false},
		{"Movie.en.sub", false, "", "", "", false, false, false},
	}
	for _, c := range cases {
		sc, ok := ParseSidecar("Movie", c.file)
		if ok != c.ok {
			t.Errorf("%s: ok = %v", c.file, ok)
			continue
		}
		if !ok {
			continue
		}
		if sc.Language != c.lang || sc.Title != c.title || sc.Codec != c.codec || sc.Forced != c.forced || sc.Default != c.deflt || sc.HI != c.hi {
			t.Errorf("%s: %+v", c.file, sc)
		}
	}
}

func TestCompareOrder(t *testing.T) {
	// The order the reference lists these files in.
	want := []string{
		"M.2.en.srt", "M.Brazilian Portuguese.srt", "M.chi.srt", "M.de.forced.default.sdh.srt", "M.default.srt",
		"M.en.1.srt", "M.en.cc.srt", "M.en.forced.srt", "M.en.Full Name.srt", "M.en.sdh.forced.srt", "M.EN.SRT",
		"M.eng.hi.srt", "M.es-419.srt", "M.forced.srt", "M.fr-CA.srt", "M.fr.srt", "M.pt_BR.srt", "M.pt-PT.srt",
		"M.SDH.en.srt", "M.Spanish.srt", "M.srt", "M.und.srt",
	}
	got := append([]string(nil), want...)
	for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
		got[i], got[j] = got[j], got[i]
	}
	sort.SliceStable(got, func(i, j int) bool { return Compare(got[i], got[j]) < 0 })
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("order:\n got %v\nwant %v", got, want)
	}
}

const srtDoc = "1\n00:00:01,000 --> 00:00:02,500\nHello <i>world</i>\n\n2\n00:00:03,000 --> 00:00:04,000\nSecond line\n"

func TestConvertSRT(t *testing.T) {
	tr := Parse(KindSRT, []byte(srtDoc))
	if len(tr.Cues) != 2 || tr.Cues[0].Text != "Hello <i>world</i>" || tr.Cues[1].End != 40_000_000 {
		t.Fatalf("cues: %+v", tr.Cues)
	}
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello <i>world</i>\n\n00:00:03.000 --> 00:00:04.000\nSecond line"
	if got := string(tr.Write(FormatVTT, Options{})); got != vtt {
		t.Errorf("vtt = %q", got)
	}
	withMap := strings.Replace(vtt, "WEBVTT\n", "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n", 1)
	if got := string(tr.Write(FormatVTT, Options{VTTTimeMap: true})); got != withMap {
		t.Errorf("vtt with map = %q", got)
	}
	if got := string(InsertVTTTimeMap([]byte(vtt))); got != withMap {
		t.Errorf("InsertVTTTimeMap = %q", got)
	}
	js := `{"TrackEvents":[{"Id":"1","Text":"Hello \u003Ci\u003Eworld\u003C/i\u003E","StartPositionTicks":10000000,"EndPositionTicks":25000000},{"Id":"2","Text":"Second line","StartPositionTicks":30000000,"EndPositionTicks":40000000}]}`
	if got := string(tr.Write(FormatJSON, Options{})); got != js {
		t.Errorf("json = %q", got)
	}
	ass := string(tr.Write(FormatASS, Options{}))
	if !strings.Contains(ass, "Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,Hello {\\i1}world{\\i0}\n") {
		t.Errorf("ass = %q", ass)
	}
	ttml := string(tr.Write(FormatTTML, Options{}))
	if !strings.Contains(ttml, `<p begin="1.0s" xml:id="p0" end="2.5s">Hello <span tts:fontStyle="italic">world</span></p>`) {
		t.Errorf("ttml = %q", ttml)
	}
}

func TestJSONEscapes(t *testing.T) {
	got := jsonString("Caf\u00e9 \"q\" & <b> \u266a\n")
	want := `"Caf\u00E9 \u0022q\u0022 \u0026 \u003Cb\u003E \u266A\n"`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestWindow(t *testing.T) {
	tr := Parse(KindSRT, []byte(srtDoc))
	cases := []struct {
		start, end int64
		copy       bool
		want       [][2]int64
	}{
		{0, 0, false, [][2]int64{{10_000_000, 25_000_000}, {30_000_000, 40_000_000}}},
		{20_000_000, 0, false, [][2]int64{{0, 5_000_000}, {10_000_000, 20_000_000}}},
		{30_000_000, 0, false, [][2]int64{{0, 10_000_000}}},
		{0, 20_000_000, false, [][2]int64{{10_000_000, 25_000_000}}},
		{20_000_000, 0, true, [][2]int64{{10_000_000, 25_000_000}, {30_000_000, 40_000_000}}},
		{-5, 0, false, [][2]int64{{10_000_005, 25_000_005}, {30_000_005, 40_000_005}}},
	}
	for i, c := range cases {
		w := tr.Window(c.start, c.end, c.copy)
		if len(w.Cues) != len(c.want) {
			t.Errorf("case %d: %d cues", i, len(w.Cues))
			continue
		}
		for j, cue := range w.Cues {
			if cue.Start != c.want[j][0] || cue.End != c.want[j][1] {
				t.Errorf("case %d cue %d: %d-%d", i, j, cue.Start, cue.End)
			}
		}
	}
}

func TestASSKinds(t *testing.T) {
	doc := "[Script Info]\nTitle: t\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,Hello {\\i1}world{\\i0}\\Nline, two\n"
	ass := Parse(KindASS, []byte(doc))
	if len(ass.Cues) != 1 || ass.Cues[0].Text != `Hello {\i1}world{\i0}\Nline, two` || ass.Style == nil || ass.Style.Font != "Arial" {
		t.Fatalf("ass: %+v", ass)
	}
	if got := string(ass.Write(FormatVTT, Options{})); !strings.Contains(got, "Hello world\nline, two") {
		t.Errorf("ass->vtt = %q", got)
	}
	if got := string(ass.Write(FormatSRT, Options{})); !strings.Contains(got, "Hello {\\i1}world{\\i0}\nline, two") {
		t.Errorf("ass->srt = %q", got)
	}
	ssa := Parse(KindSSA, []byte(doc))
	if got := string(ssa.Write(FormatVTT, Options{})); !strings.Contains(got, "Hello <i>world</i>\nline, two") {
		t.Errorf("ssa->vtt = %q", got)
	}
	if got := string(ass.Write(FormatTTML, Options{})); !strings.Contains(got, `<style xml:id="Default" tts:fontFamily="Arial" tts:fontWeight="normal" tts:fontStyle="normal" tts:color="#ffffff" tts:fontSize="20" />`) {
		t.Errorf("ttml style: %q", got)
	}
}

func TestParseVTTAndSAMI(t *testing.T) {
	v := Parse(KindVTT, []byte("WEBVTT\n\nNOTE x\n\nid1\n00:01.000 --> 00:02.000 align:start\nHi\n\n00:00:03.000 --> 00:00:04.000\nA\nB\n"))
	if len(v.Cues) != 2 || v.Cues[0].Start != 10_000_000 || v.Cues[1].Text != "A\nB" {
		t.Errorf("vtt: %+v", v.Cues)
	}
	s := Parse(KindSAMI, []byte("<SAMI><BODY><SYNC Start=1000><P Class=ENCC>Hello<br>there\n<SYNC Start=2000><P Class=ENCC>&nbsp;\n</BODY></SAMI>"))
	if len(s.Cues) != 1 || s.Cues[0].Text != "Hello\nthere" || s.Cues[0].End != 20_000_000 {
		t.Errorf("sami: %+v", s.Cues)
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"eng": "English", "por": "Portuguese", "pt-br": "Portuguese (Brazil)", "es-419": "Spanish",
		"zh-cn": "Chinese (Simplified)", "und": "Undetermined", "nld": "Dutch", "fr-ca": "French (Canada)",
	} {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}
