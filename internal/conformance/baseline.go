package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Baseline is the committed per-scenario status of a Tinyfin run (the ratchet
// of docs/contract/README.md). A scenario that passes in the baseline must not
// stop passing; improvements are reported so the baseline can be refreshed.
type Baseline struct {
	Version int `json:"version"`
	// Reference records the reference-server run the scenarios were validated
	// against (counts only; that run is manual).
	Reference *BaselineReference `json:"reference,omitempty"`
	Counts    map[string]int     `json:"counts"`
	Scenarios map[string]string  `json:"scenarios"`
}

// BaselineReference describes the manual reference validation.
type BaselineReference struct {
	Image  string         `json:"image"`
	Counts map[string]int `json:"counts"`
}

// Regression is a baseline-passing scenario that no longer passes.
type Regression struct {
	ID      string
	Now     string
	Message string
}

// Comparison is the result of checking a run against a baseline.
type Comparison struct {
	Regressions  []Regression
	Improvements []string // not passing in the baseline, passing now
	New          []string // not in the baseline
	Missing      []string // in the baseline, not part of this run
}

// NewBaseline builds a baseline from a run.
func NewBaseline(res []Result) Baseline {
	b := Baseline{Version: 1, Counts: map[string]int{}, Scenarios: map[string]string{}}
	for _, r := range res {
		b.Scenarios[r.ID] = r.Status
		b.Counts[r.Status]++
	}
	return b
}

// Marshal renders the baseline deterministically (sorted keys, trailing
// newline) so refreshes produce minimal diffs.
func (b Baseline) Marshal() ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ParseBaseline decodes a baseline file.
func ParseBaseline(data []byte) (Baseline, error) {
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	if b.Version != 1 || b.Scenarios == nil {
		return b, fmt.Errorf("not a version 1 baseline")
	}
	return b, nil
}

// BaselineCoverage is the bookkeeping a committed baseline owes the suite.
//
// Compare is deliberately lenient with a scenario it has never seen: a
// scenario missing from the baseline is reported as NEW, never as a
// regression, so it can fail on every run and leave the ratchet green. Being
// in the baseline is what makes a scenario gate at all, and this check is what
// makes that true instead of assumed. The reverse direction matters as well:
// an entry the suite no longer carries would keep the counts high, so a
// renamed or deleted scenario would still look like a passing one.
//
// Scenarios tagged fresh-server are exempt: they run only in -fresh-server
// mode, against a server that has never completed setup, and each run consumes
// it. No baselined run selects them, so they cannot have an entry and their
// absence is not a blind spot (docs/contract/README.md, "Fresh-server
// scenarios").
type BaselineCoverage struct {
	// Missing are scenarios the suite carries and the baseline does not.
	Missing []string
	// Stale are baseline entries the suite no longer carries.
	Stale []string
}

// Empty reports whether the baseline matches the suite.
func (c BaselineCoverage) Empty() bool { return len(c.Missing) == 0 && len(c.Stale) == 0 }

// freshServerTag marks the scenarios that need a server which has never
// completed setup. They are selected only by -fresh-server, which no baselined
// run uses, so they are outside the ratchet by design.
const freshServerTag = "fresh-server"

// CheckBaselineCoversTheSuite compares the scenarios on disk with the entries
// of a baseline file, in both directions.
func CheckBaselineCoversTheSuite(scenarioDir, baselinePath string) (BaselineCoverage, error) {
	list, err := LoadDir(scenarioDir)
	if err != nil {
		return BaselineCoverage{}, err
	}
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		return BaselineCoverage{}, err
	}
	b, err := ParseBaseline(data)
	if err != nil {
		return BaselineCoverage{}, fmt.Errorf("%s: %w", baselinePath, err)
	}
	var cov BaselineCoverage
	inSuite := make(map[string]bool, len(list))
	for _, s := range list {
		inSuite[s.ID] = true
		if hasTag(s.Tags, freshServerTag) {
			continue
		}
		if _, ok := b.Scenarios[s.ID]; !ok {
			cov.Missing = append(cov.Missing, s.ID)
		}
	}
	for id := range b.Scenarios {
		if !inSuite[id] {
			cov.Stale = append(cov.Stale, id)
		}
	}
	sort.Strings(cov.Missing)
	sort.Strings(cov.Stale)
	return cov, nil
}

// Compare checks a run against the baseline.
func (b Baseline) Compare(res []Result) Comparison {
	var c Comparison
	seen := map[string]bool{}
	for _, r := range res {
		seen[r.ID] = true
		was, ok := b.Scenarios[r.ID]
		switch {
		case !ok:
			c.New = append(c.New, r.ID)
		case was == StatusPass && r.Status != StatusPass:
			c.Regressions = append(c.Regressions, Regression{ID: r.ID, Now: r.Status, Message: r.Message})
		case was != StatusPass && r.Status == StatusPass:
			c.Improvements = append(c.Improvements, r.ID)
		}
	}
	for id := range b.Scenarios {
		if !seen[id] {
			c.Missing = append(c.Missing, id)
		}
	}
	sort.Strings(c.Missing)
	sort.Strings(c.New)
	sort.Strings(c.Improvements)
	sort.Slice(c.Regressions, func(i, j int) bool { return c.Regressions[i].ID < c.Regressions[j].ID })
	return c
}

// ProgressTable renders a markdown table of statuses per area (the first
// dot-separated segment of the scenario id).
func (b Baseline) ProgressTable() string {
	type row struct{ pass, fail, blocked, skipped, total int }
	areas := map[string]*row{}
	var total row
	for id, st := range b.Scenarios {
		area, _, _ := strings.Cut(id, ".")
		r := areas[area]
		if r == nil {
			r = &row{}
			areas[area] = r
		}
		for _, x := range []*row{r, &total} {
			x.total++
			switch st {
			case StatusPass:
				x.pass++
			case StatusFail:
				x.fail++
			case StatusBlocked:
				x.blocked++
			default:
				x.skipped++
			}
		}
	}
	names := make([]string, 0, len(areas))
	for a := range areas {
		names = append(names, a)
	}
	sort.Strings(names)
	var sb strings.Builder
	sb.WriteString("| Area | Scenarios | Pass | Fail | Blocked | Skipped |\n|---|---|---|---|---|---|\n")
	for _, a := range names {
		r := areas[a]
		fmt.Fprintf(&sb, "| %s | %d | %d | %d | %d | %d |\n", a, r.total, r.pass, r.fail, r.blocked, r.skipped)
	}
	fmt.Fprintf(&sb, "| **Total** | %d | %d | %d | %d | %d |\n", total.total, total.pass, total.fail, total.blocked, total.skipped)
	return sb.String()
}
