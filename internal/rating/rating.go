// Package rating maps an item's official (parental) rating text to a
// comparable score. The table and the fallbacks below are the observable
// behavior of the reference server (GET /Localization/ParentalRatings plus
// black-box probes of listings), documented in docs/contract/specs/items.md
// (items.rating.*).
package rating

import (
	"strconv"
	"strings"
)

// Score is a rating's position: Value is the age-like number, Sub breaks
// ties between ratings with the same value (for example R is 17/0 while
// NC-17 and TV-MA are 17/1).
type Score struct {
	Value int
	Sub   int
}

// Exceeds reports whether s is stricter than limit, comparing Value then Sub.
func (s Score) Exceeds(limit Score) bool {
	if s.Value != limit.Value {
		return s.Value > limit.Value
	}
	return s.Sub > limit.Sub
}

// ExceedsValue is Exceeds ignoring Sub (the single-item read gate).
func (s Score) ExceedsValue(limit Score) bool { return s.Value > limit.Value }

const noValue = -1

type entry struct {
	name       string
	value, sub int
}

var table = []entry{
	{"Unrated", noValue, 0},
	{"Approved", 0, 0},
	{"G", 0, 0},
	{"TV-G", 0, 0},
	{"TV-Y", 0, 0},
	{"TV-Y7", 7, 0},
	{"TV-Y7-FV", 7, 1},
	{"PG", 10, 0},
	{"TV-PG", 10, 0},
	{"TV-PG-D", 10, 1},
	{"TV-PG-L", 10, 1},
	{"TV-PG-S", 10, 1},
	{"TV-PG-V", 10, 1},
	{"TV-PG-DL", 10, 1},
	{"TV-PG-DS", 10, 1},
	{"TV-PG-DV", 10, 1},
	{"TV-PG-LS", 10, 1},
	{"TV-PG-LV", 10, 1},
	{"TV-PG-SV", 10, 1},
	{"TV-PG-DLS", 10, 1},
	{"TV-PG-DLV", 10, 1},
	{"TV-PG-DSV", 10, 1},
	{"TV-PG-LSV", 10, 1},
	{"TV-PG-DLSV", 10, 1},
	{"PG-13", 13, 0},
	{"TV-14", 14, 0},
	{"TV-14-D", 14, 1},
	{"TV-14-L", 14, 1},
	{"TV-14-S", 14, 1},
	{"TV-14-V", 14, 1},
	{"TV-14-DL", 14, 1},
	{"TV-14-DS", 14, 1},
	{"TV-14-DV", 14, 1},
	{"TV-14-LS", 14, 1},
	{"TV-14-LV", 14, 1},
	{"TV-14-SV", 14, 1},
	{"TV-14-DLS", 14, 1},
	{"TV-14-DLV", 14, 1},
	{"TV-14-DSV", 14, 1},
	{"TV-14-LSV", 14, 1},
	{"TV-14-DLSV", 14, 1},
	{"R", 17, 0},
	{"NC-17", 17, 1},
	{"TV-MA", 17, 1},
	{"TV-MA-L", 17, 1},
	{"TV-MA-S", 17, 1},
	{"TV-MA-V", 17, 1},
	{"TV-MA-LS", 17, 1},
	{"TV-MA-LV", 17, 1},
	{"TV-MA-SV", 17, 1},
	{"TV-MA-LSV", 17, 1},
	{"TV-X", 18, 0},
	{"TV-AO", 18, 0},
	{"21", 21, 0},
	{"XXX", 1000, 0},
	{"Banned", 1001, 0},
}

var byName = func() map[string]entry {
	m := make(map[string]entry, len(table))
	for _, e := range table {
		m[strings.ToLower(e.name)] = e
	}
	return m
}()

func lookup(s string) (Score, bool, bool) {
	e, ok := byName[strings.ToLower(s)]
	if !ok {
		return Score{}, false, false
	}
	if e.value == noValue {
		return Score{}, false, true
	}
	return Score{Value: e.value, Sub: e.sub}, true, true
}

func number(s string) (Score, bool) {
	s = strings.TrimSuffix(s, "+")
	if s == "" {
		return Score{}, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return Score{}, false
	}
	return Score{Value: n}, true
}

// Parse resolves rating text. The second result is false when the text has
// no value (empty, "Unrated", or unrecognized): such an item is "unrated".
//
// Order: the table (case-insensitive); the text without a leading "Rated ";
// the part after a country prefix ("US:PG", "DE-12"), by table or as a
// number; the whole text as a number ("12", "16+").
func Parse(text string) (Score, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return Score{}, false
	}
	if sc, ok, known := lookup(t); known {
		return sc, ok
	}
	if len(t) > 6 && strings.EqualFold(t[:6], "rated ") {
		t = strings.TrimSpace(t[6:])
		if sc, ok, known := lookup(t); known {
			return sc, ok
		}
	}
	if i := strings.IndexAny(t, ":-"); i > 0 && i < len(t)-1 {
		rest := strings.TrimSpace(t[i+1:])
		if sc, ok, known := lookup(rest); known {
			return sc, ok
		}
		if sc, ok := number(rest); ok {
			return sc, true
		}
	}
	if sc, ok := number(t); ok {
		return sc, true
	}
	return Score{}, false
}
