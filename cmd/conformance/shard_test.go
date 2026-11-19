package main

import "testing"

func TestParseShard(t *testing.T) {
	cases := []struct {
		in      string
		index   int
		total   int
		wantErr bool
	}{
		{in: "1/4", index: 0, total: 4},
		{in: "4/4", index: 3, total: 4},
		{in: "1/1", index: 0, total: 1},
		// The message tells the operator what went wrong, so the flag fails
		// loudly instead of silently running the whole suite.
		{in: "", wantErr: true},
		{in: "0/4", wantErr: true},
		{in: "5/4", wantErr: true},
		{in: "1/0", wantErr: true},
		{in: "x/4", wantErr: true},
		{in: "1/4/7", wantErr: true},
	}
	for _, c := range cases {
		index, total, err := parseShard(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseShard(%q) = %d/%d, want an error", c.in, index, total)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseShard(%q): %v", c.in, err)
			continue
		}
		if index != c.index || total != c.total {
			t.Errorf("parseShard(%q) = %d/%d, want %d/%d", c.in, index, total, c.index, c.total)
		}
	}
}
