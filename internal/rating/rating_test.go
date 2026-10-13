package rating

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		val, sub int
	}{
		{"G", true, 0, 0},
		{"tv-g", true, 0, 0},
		{"Approved", true, 0, 0},
		{"TV-Y7", true, 7, 0},
		{"TV-Y7-FV", true, 7, 1},
		{"PG", true, 10, 0},
		{"PG-13", true, 13, 0},
		{"pg-13", true, 13, 0},
		{"TV-14", true, 14, 0},
		{"TV-14-D", true, 14, 1},
		{"R", true, 17, 0},
		{"NC-17", true, 17, 1},
		{"TV-MA", true, 17, 1},
		{"TV-X", true, 18, 0},
		{"21", true, 21, 0},
		{"XXX", true, 1000, 0},
		{"Banned", true, 1001, 0},
		// Not in the table: a prefix is stripped, a number is its own value.
		{"Rated R", true, 17, 0},
		{"US:PG", true, 10, 0},
		{"DE-12", true, 12, 0},
		{"BR-16", true, 16, 0},
		{"ZZ-99", true, 99, 0},
		{"12", true, 12, 0},
		{"16+", true, 16, 0},
		{"  PG  ", true, 10, 0},
		// No value: behaves as unrated.
		{"", false, 0, 0},
		{"Unrated", false, 0, 0},
		{"Not Rated", false, 0, 0},
		{"ABC", false, 0, 0},
		{"L", false, 0, 0},
		{"NR", false, 0, 0},
		{"Adults Only", false, 0, 0},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || (ok && (got.Value != c.val || got.Sub != c.sub)) {
			t.Errorf("Parse(%q) = %+v,%v want %d/%d,%v", c.in, got, ok, c.val, c.sub, c.ok)
		}
	}
}

func TestScoreOrdering(t *testing.T) {
	r, _ := Parse("R")
	nc, _ := Parse("NC-17")
	pg, _ := Parse("PG")
	if !nc.Exceeds(r) || r.Exceeds(nc) || r.Exceeds(r) || !r.Exceeds(pg) {
		t.Errorf("ordering broken: R=%+v NC-17=%+v PG=%+v", r, nc, pg)
	}
	if nc.ExceedsValue(r) {
		t.Error("ExceedsValue must ignore the sub rating")
	}
}
