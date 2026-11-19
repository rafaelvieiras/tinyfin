package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ShardPlan is how a suite is dealt across machines, as a list of file lists.
// It is committed (docs/contract/shards.json) instead of being computed from
// the scenario count, because a scenario file's cost is not its length: a file
// of 23 scan scenarios took 163 s where a file of 156 item scenarios took
// 181 s, and dealing by count left the shards at 2.3, 6.1, 9.1 and 11.8
// minutes instead of the 7.3 a split by measured time gives.
//
// The plan is data, not code, so it is regenerated from a run's report
// (`go run ./cmd/conformance shards`) when the suite changes, and checked
// against the scenario directory: every file appears exactly once, or the run
// refuses to start.
type ShardPlan struct {
	// Note records how the plan was measured, for whoever regenerates it.
	Note string `json:"note,omitempty"`
	// Shards[i] lists the files shard i runs. A file is assigned whole: a
	// capture is only read by a later scenario of the same file, so cutting one
	// would break it.
	Shards [][]string `json:"shards"`
}

// LoadShardPlan reads a plan file. A missing file is not an error: the caller
// falls back to dealing by count, which is worse but needs no bookkeeping.
func LoadShardPlan(path string) (*ShardPlan, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p ShardPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// Check verifies the plan covers the scenario directory exactly once, so a new
// scenario file cannot silently fall out of CI.
func (p *ShardPlan) Check(files []string) error {
	seen := map[string]int{}
	for i, shard := range p.Shards {
		for _, f := range shard {
			seen[f] = seen[f] + 1
			_ = i
		}
	}
	want := map[string]bool{}
	for _, f := range files {
		want[f] = true
	}
	var missing, dup, unknown []string
	for f := range want {
		switch seen[f] {
		case 0:
			missing = append(missing, f)
		case 1:
		default:
			dup = append(dup, f)
		}
	}
	for f := range seen {
		if !want[f] {
			unknown = append(unknown, f)
		}
	}
	sort.Strings(missing)
	sort.Strings(dup)
	sort.Strings(unknown)
	// Everything wrong is reported at once: regenerating the plan is one
	// command, and seeing all of it saves a round of that.
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("misses %v", missing))
	}
	if len(dup) > 0 {
		problems = append(problems, fmt.Sprintf("lists %v more than once", dup))
	}
	if len(unknown) > 0 {
		problems = append(problems, fmt.Sprintf("names %v, which is not a scenario file", unknown))
	}
	if len(problems) > 0 {
		return fmt.Errorf("shard plan %s: regenerate with go run ./cmd/conformance shards -report <run.json> -write docs/contract/shards.json",
			strings.Join(problems, "; "))
	}
	return nil
}

// Select returns the scenarios shard index runs, in the order they were
// written. index is zero-based.
func (p *ShardPlan) Select(list []Scenario, index int) []Scenario {
	if index < 0 || index >= len(p.Shards) {
		return nil
	}
	mine := map[string]bool{}
	for _, f := range p.Shards[index] {
		mine[f] = true
	}
	out := make([]Scenario, 0, len(list)/len(p.Shards))
	for _, s := range list {
		if mine[s.File] {
			out = append(out, s)
		}
	}
	return out
}

// PlanFromWeights deals files to shards by a weight per file (measured
// milliseconds, or a scenario count when nothing was measured), largest first,
// each to the shard carrying the least so far. Ties break on file name, so the
// same weights always produce the same plan.
func PlanFromWeights(weights map[string]int64, shards int) ShardPlan {
	if shards < 1 {
		shards = 1
	}
	order := make([]string, 0, len(weights))
	for f := range weights {
		order = append(order, f)
	}
	sort.Slice(order, func(i, j int) bool {
		if weights[order[i]] != weights[order[j]] {
			return weights[order[i]] > weights[order[j]]
		}
		return order[i] < order[j]
	})
	load := make([]int64, shards)
	plan := ShardPlan{Shards: make([][]string, shards)}
	for _, f := range order {
		lightest := 0
		for i := 1; i < shards; i++ {
			if load[i] < load[lightest] {
				lightest = i
			}
		}
		load[lightest] += weights[f]
		plan.Shards[lightest] = append(plan.Shards[lightest], f)
	}
	for i := range plan.Shards {
		sort.Strings(plan.Shards[i])
	}
	return plan
}
