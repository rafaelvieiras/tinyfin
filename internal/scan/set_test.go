package scan

import (
	"encoding/xml"
	"testing"
)

func TestMovieNFOSetName(t *testing.T) {
	cases := []struct {
		name, xml, want string
	}{
		{"named", `<movie><title>A</title><set><name>Alpha Saga</name><overview>x</overview></set></movie>`, "Alpha Saga"},
		{"text form", `<movie><set>Alpha Saga</set></movie>`, "Alpha Saga"},
		{"kept as written", `<movie><set><name>  Padded  </name></set></movie>`, "  Padded  "},
		{"blank", `<movie><set><name>   </name></set></movie>`, ""},
		{"empty", `<movie><set/></movie>`, ""},
		{"none", `<movie><title>A</title></movie>`, ""},
		{"collection id alone names nothing", `<movie><tmdbcollectionid>9</tmdbcollectionid></movie>`, ""},
	}
	for _, c := range cases {
		var n movieNFO
		if err := xml.Unmarshal([]byte(c.xml), &n); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := n.Set.value(); got != c.want {
			t.Errorf("%s: set = %q, want %q", c.name, got, c.want)
		}
	}
}
