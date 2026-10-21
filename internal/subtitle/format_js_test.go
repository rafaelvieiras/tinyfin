package subtitle

import "testing"

func TestParseFormatJSAlias(t *testing.T) {
	for _, n := range []string{"js", "JS", "json"} {
		if f, alias, ok := ParseFormat(n); !ok || alias || f != FormatJSON {
			t.Errorf("%s: got %q alias=%v ok=%v", n, f, alias, ok)
		}
	}
	if _, _, ok := ParseFormat("jsonp"); ok {
		t.Error("jsonp must not be a format")
	}
}
