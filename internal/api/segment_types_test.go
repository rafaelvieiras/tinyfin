package api

import "testing"

func TestSegmentTypesProblem(t *testing.T) {
	for v, want := range map[string]string{
		"Intro": "", "outro,5": "", "Intro, Outro": "", "0": "",
		"": "invalid", "9": "invalid", "-1": "invalid", "Intro,9": "invalid",
		"Bogus": "not valid", "Intro,Bogus": "not valid", "Intro,": "not valid",
	} {
		if got := segmentTypesProblem(v); got != want {
			t.Errorf("segmentTypesProblem(%q) = %q, want %q", v, got, want)
		}
	}
}
