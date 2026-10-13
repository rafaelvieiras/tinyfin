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
	}
	for _, c := range cases {
		if got := NormalizeSortName(c.in); got != c.want {
			t.Errorf("NormalizeSortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestItemSortKey(t *testing.T) {
	one, two := 1, 2
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
		{"episode without numbers", Item{Type: ItemTypeEpisode, Name: "The Special", SortName: "The Special"}, "special"},
	}
	for _, c := range cases {
		if got := c.it.SortKey(); got != c.want {
			t.Errorf("%s: SortKey() = %q, want %q", c.name, got, c.want)
		}
	}
}
