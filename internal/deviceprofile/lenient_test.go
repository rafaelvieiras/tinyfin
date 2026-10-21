package deviceprofile

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestLenientInt(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
		nil_ bool
		bad  bool
	}{
		{in: `3`, want: 3}, {in: `"3"`, want: 3}, {in: `" 4 "`, want: 4}, {in: `null`, nil_: true}, {in: `""`, nil_: true},
		{in: `"abc"`, bad: true}, {in: `1.5`, bad: true}, {in: `true`, bad: true},
	} {
		got, err := LenientInt(json.RawMessage(c.in), true)
		switch {
		case c.bad:
			if !errors.Is(err, ErrBadNumber) {
				t.Errorf("%s: err = %v, want ErrBadNumber", c.in, err)
			}
		case err != nil:
			t.Errorf("%s: %v", c.in, err)
		case c.nil_:
			if got != nil {
				t.Errorf("%s: got %d, want unset", c.in, *got)
			}
		case got == nil || *got != c.want:
			t.Errorf("%s: got %v, want %d", c.in, got, c.want)
		}
	}
}

func TestTranscodingProfileDigitStrings(t *testing.T) {
	var tp TranscodingProfile
	if err := json.Unmarshal([]byte(`{"Container":"ts","MinSegments":"2","SegmentLength":3}`), &tp); err != nil {
		t.Fatal(err)
	}
	if tp.Container != "ts" || tp.MinSegments != 2 || tp.SegmentLength != 3 {
		t.Errorf("got %+v", tp)
	}
	for _, bad := range []string{`{"MinSegments":"abc"}`, `{"MinSegments":null}`, `{"SegmentLength":""}`, `{"MinSegments":1.5}`} {
		if err := json.Unmarshal([]byte(bad), &tp); !errors.Is(err, ErrBadNumber) {
			t.Errorf("%s: err = %v, want ErrBadNumber", bad, err)
		}
	}
}
