package scan

import "testing"

func TestFrameSeconds(t *testing.T) {
	ticks := func(s float64) *int64 { v := int64(s * 1e7); return &v }
	if got := frameSeconds(nil); got != 0 {
		t.Fatalf("unknown length: got %v, want 0", got)
	}
	for _, rt := range []float64{0.1, 1, 6, 20, 3600} {
		got := frameSeconds(ticks(rt))
		if got <= 0 || got >= rt {
			t.Errorf("runtime %vs: frame at %vs, want inside the video", rt, got)
		}
	}
	if a, b := frameSeconds(ticks(6)), frameSeconds(ticks(60)); a >= b {
		t.Errorf("a longer video should be sampled later: %v >= %v", a, b)
	}
}
