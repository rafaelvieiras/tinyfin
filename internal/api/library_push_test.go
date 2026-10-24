package api

import "testing"

func TestDiffLibrary(t *testing.T) {
	before := map[string]libSnap{
		"keep": {fp: "a", parent: "root"}, "gone": {fp: "b", parent: "root"}, "edit": {fp: "c", parent: "root"},
	}
	after := map[string]libSnap{
		"keep": {fp: "a", parent: "root"}, "edit": {fp: "c2", parent: "root"}, "new": {fp: "d", parent: "root"},
	}
	d := diffLibrary("lib", before, after)
	if _, ok := d.added["new"]; !ok || len(d.added) != 1 {
		t.Errorf("added = %v, want only new", d.added)
	}
	if _, ok := d.removed["gone"]; !ok || len(d.removed) != 1 {
		t.Errorf("removed = %v, want only gone", d.removed)
	}
	if _, ok := d.updated["edit"]; !ok || len(d.updated) != 1 {
		t.Errorf("updated = %v, want only edit", d.updated)
	}
	if _, ok := d.addedTo["root"]; !ok {
		t.Errorf("addedTo = %v, want root", d.addedTo)
	}
	if _, ok := d.removedFrom["root"]; !ok {
		t.Errorf("removedFrom = %v, want root", d.removedFrom)
	}
	if !diffLibrary("lib", before, before).empty() {
		t.Error("identical snapshots produced a delta")
	}
}
