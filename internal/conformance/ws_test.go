package conformance

import "testing"

// TestFindInHistorySeesWhatAWaitSteppedOver covers WsStep.Lookback: take (and
// so waitFor) looks forward from the last message it matched, so a push that
// arrived between two waits is stepped over for good. A scenario that asserts
// "this change was pushed" needs findInHistory instead, which reads the whole
// buffer without consuming anything.
func TestFindInHistorySeesWhatAWaitSteppedOver(t *testing.T) {
	removal := map[string]any{"MessageType": "LibraryChanged", "Data": map[string]any{"ItemsRemoved": []any{"gone"}}}
	additions := map[string]any{"MessageType": "LibraryChanged", "Data": map[string]any{"ItemsAdded": []any{"new"}}}
	w := &wsConn{msgs: []map[string]any{removal, additions}}

	// The additions are matched first in buffer order, which steps over the
	// removal that came before them.
	wantAdded := func(m map[string]any) bool {
		d, _ := m["Data"].(map[string]any)
		return d["ItemsAdded"] != nil
	}
	if _, ok := w.take("LibraryChanged", wantAdded); !ok {
		t.Fatal("take did not find the additions")
	}
	if _, ok := w.take("LibraryChanged", func(m map[string]any) bool {
		d, _ := m["Data"].(map[string]any)
		return d["ItemsRemoved"] != nil
	}); ok {
		t.Fatal("take found the removal, but the cursor had already stepped over it")
	}
	if _, ok := w.findInHistory("LibraryChanged", func(m map[string]any) bool {
		d, _ := m["Data"].(map[string]any)
		return d["ItemsRemoved"] != nil
	}); !ok {
		t.Fatal("findInHistory did not find the removal the wait stepped over")
	}
}
