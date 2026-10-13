package api

import (
	"encoding/json"
	"fmt"
	"time"
)

// wireTime is the type of every date-time member in a JSON response. It
// writes the one format the reference server uses (wire.date.R1-R3): UTC,
// a trailing Z, a fraction of at most 7 digits (100 ns ticks, truncated),
// trailing zeros dropped, and ".0000000" when the fraction is exactly
// zero. The zero time.Time is therefore "0001-01-01T00:00:00.0000000Z".
//
// Use it (or *wireTime with omitempty) instead of time.Time in any struct
// that is encoded for a client; Go's default encoding would write up to 9
// digits.
type wireTime struct{ time.Time }

// formatWireTime renders t in the wire date format.
func formatWireTime(t time.Time) string {
	t = t.UTC()
	ticks := t.Nanosecond() / 100
	s := t.Format("2006-01-02T15:04:05")
	if ticks == 0 {
		return s + ".0000000Z"
	}
	frac := [7]byte{}
	for i := 6; i >= 0; i-- {
		frac[i] = byte('0' + ticks%10)
		ticks /= 10
	}
	n := 7
	for frac[n-1] == '0' {
		n--
	}
	return s + "." + string(frac[:n]) + "Z"
}

// MarshalJSON implements json.Marshaler.
func (w wireTime) MarshalJSON() ([]byte, error) {
	return []byte(`"` + formatWireTime(w.Time) + `"`), nil
}

// wireTimePtr converts an optional time for a response member.
func wireTimePtr(t *time.Time) *wireTime {
	if t == nil {
		return nil
	}
	return &wireTime{*t}
}

// requestTime is the type of a date-time member read from a request body
// (wire.date.R4): a fraction of any length, an offset, or no zone at all
// (read as UTC) are all accepted.
type requestTime struct{ time.Time }

// UnmarshalJSON implements json.Unmarshaler.
func (r *requestTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t, ok := parseDatePlayed(s)
	if !ok {
		return fmt.Errorf("invalid date-time %q", s)
	}
	r.Time = t
	return nil
}
