package conformance

import "testing"

func TestCheckText(t *testing.T) {
	c := &Client{Vars: map[string]string{"k": "v"}}
	r := &Response{Body: []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello v\n")}
	ok := []TextAssert{
		{Op: "prefix", Value: "WEBVTT\n"},
		{Op: "contains", Value: "Hello {{k}}"},
		{Op: "notcontains", Value: "Goodbye"},
		{Op: "matches", Value: `(?s)^WEBVTT.*Hello`},
		{Op: "lines", Value: float64(3)},
		{Op: "minlen", Value: float64(10)},
		{Op: "maxlen", Value: float64(100)},
		{Op: "eq", Value: "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello v\n"},
	}
	for _, a := range ok {
		if err := c.checkText(r, a, nil); err != nil {
			t.Errorf("%s %v: %v", a.Op, a.Value, err)
		}
	}
	bad := []TextAssert{
		{Op: "prefix", Value: "X"}, {Op: "contains", Value: "nope"}, {Op: "notcontains", Value: "Hello"},
		{Op: "matches", Value: `^Hello`}, {Op: "lines", Value: float64(2)}, {Op: "minlen", Value: float64(1000)}, {Op: "maxlen", Value: float64(5)}, {Op: "eq", Value: "x"}, {Op: "nope", Value: "x"},
	}
	for _, a := range bad {
		if err := c.checkText(r, a, nil); err == nil {
			t.Errorf("%s %v: no error", a.Op, a.Value)
		}
	}
}
