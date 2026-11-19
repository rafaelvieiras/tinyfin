package trickplay

import "testing"

// TestTaskProgressIsTheShareOfExaminedItems pins the reporting arithmetic:
// the percentage is examined/expected, it is capped, and it is only reported
// while a batch with a known size runs.
func TestTaskProgressIsTheShareOfExaminedItems(t *testing.T) {
	g := &Generator{taskJobs: 1, taskDone: 3, taskTotal: 12}
	if pct, ok := g.TaskProgress(); !ok || pct != 25 {
		t.Errorf("3 of 12 = %v, %v; want 25, true", pct, ok)
	}
	g.taskDone = 13
	if pct, _ := g.TaskProgress(); pct != 100 {
		t.Errorf("past the end = %v, want 100", pct)
	}
	g.taskDone, g.taskTotal = 0, 0
	if pct, ok := g.TaskProgress(); ok || pct != 0 {
		t.Errorf("empty selection = %v, %v; want 0, false", pct, ok)
	}
	g.taskTotal, g.taskJobs = 4, 0
	if _, ok := g.TaskProgress(); ok {
		t.Error("no batch running, but progress is reported")
	}
}
