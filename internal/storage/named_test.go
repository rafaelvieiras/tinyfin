package storage

import "testing"

func TestSearchKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Alpha Movie (2015)", "alpha movie 2015"},
		{"  ALPHA   movie ", "alpha movie"},
		{"Mr. O'Neil-Smith_Jr: Part 2! & 3", "mr oneil smith jr part 2 3"},
		{"Café Über Straße", "cafe uber strasse"},
		{"Dot.Dash", "dot dash"},
		{"“quoted”", "quoted"},
		{"...", ""},
		{"", ""},
		{"2001: A Space Odyssey", "2001 a space odyssey"},
	} {
		if got := SearchKey(tc.in); got != tc.want {
			t.Errorf("SearchKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNamedIDIgnoresCase(t *testing.T) {
	if NamedID(NamedGenre, "Action") != NamedID(NamedGenre, " action ") {
		t.Error("ids differ by case or padding")
	}
	if NamedID(NamedGenre, "Action") == NamedID(NamedStudio, "Action") {
		t.Error("kinds share ids")
	}
	if len(NamedID(NamedPerson, "x")) != 32 {
		t.Error("not a 32-hex id")
	}
}
