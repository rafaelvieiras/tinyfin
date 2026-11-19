package conformance

import "testing"

// TestSequenceAssertsOrderNotMembership covers the sequence op: the values
// named must be there and in that order, and values nobody named must not
// spoil it. Asserting the exact list instead would bind an order rule to a
// server nothing else had touched.
func TestSequenceAssertsOrderNotMembership(t *testing.T) {
	cases := []struct {
		name string
		got  []any
		want []any
		ok   bool
	}{
		{"in order, with strangers in between", []any{"x", "a", "stranger", "b"}, []any{"a", "b"}, true},
		{"out of order", []any{"b", "a"}, []any{"a", "b"}, false},
		{"one missing", []any{"a"}, []any{"a", "b"}, false},
		{"empty result", []any{}, []any{"a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{}
			err := c.checkAssert(&Response{JSON: map[string]any{"Items": tc.got}},
				Assert{Path: "$.Items", Op: "sequence", Value: tc.want}, map[string]string{})
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("no error, want a failure")
			}
		})
	}
}
