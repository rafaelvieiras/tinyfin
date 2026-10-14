package storage

import "testing"

func TestNormalizeSortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The Matrix", "matrix"},
		{"the lowercase", "lowercase"},
		{"THE UPPER", "upper"},
		{"A Yankee Sort (2013)", "yankee sort (0000002013)"},
		{"An Apple", "apple"},
		{"Theater Movie", "theater movie"},
		// Articles anywhere as whole words (observed reference output).
		{"Return of the King", "return of king"},
		{"2001 A Space Odyssey", "0000002001 space odyssey"},
		{"A Tale of an Apple", "tale of apple"},
		{"The The The", "the"},
		{"The Man in the The", "man in"},
		{"Out of The", "out of"},
		{"Hello - A World", "hello  world"},
		{"Hello The  World", "hello  world"},
		{"Hello.The.World", "hello the world"},
		{"Hello 'The' World", "hello the world"},
		{"Rock & The Roll", "rock  roll"},
		{"One An Two A Three The Four", "one two three four"},
		{"A The B", "b"},
		{"X A", "x"},
		{"Thea The", "thea"},
		{"HELLO THE WORLD", "hello world"},
		{"Die Hard", "die hard"},
		{"The", "the"},
		{"A", "a"},
		{"An", "an"},
		{"The  Double", " double"},
		{"The-Thing", "thething"},
		{"The.Dot", "the dot"},
		{"A-Team", "ateam"},
		{"Rock & Roll", "rock  roll"},
		{"Rock-n-Roll", "rocknroll"},
		{"Dr. Who", "dr  who"},
		{"Mr. Brown's", "mr  browns"},
		{"L'Avion", "lavion"},
		{"Plus+Plus", "plus plus"},
		{"100% Wolf", "0000000100  wolf"},
		{"Comma, Comma {Brace}", "comma comma brace"},
		{"Movie 2", "movie 0000000002"},
		{"Movie 10", "movie 0000000010"},
		{"Movie 1.5", "movie 0000000001 0000000005"},
		{"3 Idiots", "0000000003 idiots"},
		{"The 5th Element", "0000000005th element"},
		{"12345678901234 long", "12345678901234 long"},
		{"Éclair Sort", "eclair sort"},
		{"Ångström", "angstrom"},
		{"Ünder Ñandú", "under nandu"},
		{"Zoë Ççç", "zoe ccc"},
		{"Straße", "strasse"},
		{"ÆON Flux", "aeon flux"},
		{"Œuvre", "oeuvre"},
		{"Øresund Łódź", "oresund lodz"},
		{"Đorđe Þór", "dorde thor"},
		{"Ĳsselmeer", "ijsselmeer"},
		{"Under_Score #Hash @At ~Tilde", "under_score #hash @at ~tilde"},
		{"Wow! What? Yes: No;", "wow! what? yes: no;"},
		{"\"Quote\" (Paren) [Bracket]", "\"quote\" (paren) [bracket]"},
		{"", ""},

		// Non-Latin scripts: the cases below are observed reference output.
		// Greek, Cyrillic and the Slavic letters with a Latin carrier.
		{"Привет", "privet"},
		{"Привет мир", "privet mir"},
		{"Ж", "z"},
		{"Щастие Чай Шум Юля Яма", "sastie cai sum ula ama"},
		{"Ђорђе Їжак", "dorde izak"},
		{"Ъ", ""},
		{"Ыы", "yy"},
		{"Ελληνικά", "ellenika"},
		{"ΑΒΓ Δ", "abg d"},
		{"Θ Φ Χ Ψ", "th ph ch ps"},
		{"Άγγελος", "angelos"},
		{"γκ γξ γχ γμ", "nk nx nch gm"},
		{"αυ ευ ου", "au eu ou"},
		{"ὈΔΥΣΣΕΎΣ", "odysseus"},
		// Other alphabets and abjads.
		{"გამარჯობა", "gamarjoba"},
		{"Բարեւ", "barew"},
		{"שלום", "slwm"},
		{"مرحبا", "mrhba"},
		{"नमस्ते", "namaste"},
		{"से", "se"},
		{"สวัสดี", "swasdi"},
		// Kana and Hangul.
		{"ガギグ", "gagigu"},
		{"あぁっ", "a~a~tsu"},
		{"ｱｲｳ ｶﾞ", "aiu ga"},
		{"ﾊﾟｰ ｳﾞ", "pa vu"},
		{"コーヒー ラーメン", "kohi ramen"},
		{"xーy", "xーy"},
		{"한국어 제목", "hangugeo jemog"},
		{"힣", "hih"},
		// Han: one reading per ideograph, a space between neighbours.
		{"中文", "zhong wen"},
		{"日本語のテスト", "ri ben yunotesuto"},
		{"漢字ひらがな", "han zihiragana"},
		{"北京abc", "bei jingabc"},
		{"abc北京", "abc bei jing"},
		{"1北京", "0000000001bei jing"},
		{"中\u3002文", "zhongwen"},
		{"中、文", "zhong wen"},
		{"東京 2020", "dong jing 0000002020"},
		// Digits of other scripts are padded like ASCII digits.
		{"\u0663\u0664", "0000000034"},
		{"12\u0663\u0664", "0000001234"},
		{"1\u0662\u0033", "0000000123"},
		{"\u0660\u0661\u0662\u0663\u0664\u0665\u0666\u0667\u0668\u0669\u0660\u0661", "012345678901"},
		{"\uff21\uff22\uff23 \uff11\uff12", "abc 0000000012"},
		// Latin-set strings keep ASCII punctuation; other scripts drop it.
		{"Caf\u00e9! (2)", "cafe! (0000000002)"},
		{"Привет, мир! (1)", "privet mir 0000000001"},
		{"Привет!", "privet"},
		{"Ж$x", "z$x"},
		{"Ж+З", "z z"},
		{"Straße!", "strasse"},
		{"\u0130stanbul", "Istanbul"},
		{"x\u2013y", "xy"},
		{"\u00bc cup", " 14 cup"},
		{"\u2167 2", "viii 0000000002"},
		// Unmapped characters keep their lower-cased form.
		{"\u2460", "\u2460"},
		{"\U0001d7ce1", "\U0001d7ce0000000001"},
	}
	for _, c := range cases {
		if got := NormalizeSortName(c.in); got != c.want {
			t.Errorf("NormalizeSortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestItemSortKey(t *testing.T) {
	zero, one, two := 0, 1, 2
	ten := 10
	cases := []struct {
		name string
		it   Item
		want string
	}{
		{"movie from sort name", Item{Type: ItemTypeMovie, Name: "The Zeta", SortName: "The Zeta"}, "zeta"},
		{"movie falls back to name", Item{Type: ItemTypeMovie, Name: "A Yankee"}, "yankee"},
		{"series", Item{Type: ItemTypeSeries, Name: "The Sort Show", SortName: "The Sort Show"}, "sort show"},
		{"season index", Item{Type: ItemTypeSeason, Name: "Season 2", SortName: "0002", IndexNumber: &two}, "0002"},
		{"episode keeps its title", Item{Type: ItemTypeEpisode, Name: "The Opening", SortName: "The Opening", ParentIndexNumber: &one, IndexNumber: &one}, "001 - 0001 - The Opening"},
		{"episode numbers pad", Item{Type: ItemTypeEpisode, Name: "Pilot", ParentIndexNumber: &two, IndexNumber: &ten}, "002 - 0010 - Pilot"},
		{"episode without numbers keeps its title", Item{Type: ItemTypeEpisode, Name: "The Special", SortName: "The Special"}, "The Special"},
		{"episode with a season only", Item{Type: ItemTypeEpisode, Name: "Short One", ParentIndexNumber: &zero}, "000 - Short One"},
		{"episode with an episode only", Item{Type: ItemTypeEpisode, Name: "Loose", IndexNumber: &ten}, "0010 - Loose"},
		{"episode with a sort title", Item{Type: ItemTypeEpisode, Name: "Two", SortName: "The Zed", ForcedSortName: " The Zed ", ParentIndexNumber: &one, IndexNumber: &two}, "zed"},
		{"season without a number", Item{Type: ItemTypeSeason, Name: "Season Unknown"}, "Season Unknown"},
		{"surrounding space is dropped", Item{Type: ItemTypeMovie, Name: "\u00a0 The Lead\t"}, "lead"},
	}
	for _, c := range cases {
		if got := c.it.SortKey(); got != c.want {
			t.Errorf("%s: SortKey() = %q, want %q", c.name, got, c.want)
		}
	}
}
