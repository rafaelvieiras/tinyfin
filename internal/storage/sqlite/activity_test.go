package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func TestAppendAndQueryActivityEntries(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	uid := storage.UserID("user-1")

	// Three entries, oldest to newest: a failed login (no UserID), a
	// successful login (UserID set), then another failed login — spread a
	// day apart so MinDate can select a strict subset.
	ids := make([]int64, 0, 3)
	for i, e := range []storage.ActivityEntry{
		{
			Name:     "Failed login attempt by alice",
			Type:     storage.ActivityTypeAuthenticationFailed,
			Date:     base,
			Severity: storage.ActivitySeverityError,
		},
		{
			Name:          "alice is online from TestClient",
			ShortOverview: "Remote IP: 127.0.0.1",
			Type:          storage.ActivityTypeSessionStarted,
			Date:          base.Add(24 * time.Hour),
			UserID:        &uid,
			Severity:      storage.ActivitySeverityInfo,
		},
		{
			Name:     "Failed login attempt by mallory",
			Type:     storage.ActivityTypeAuthenticationFailed,
			Date:     base.Add(48 * time.Hour),
			Severity: storage.ActivitySeverityError,
		},
	} {
		id, err := st.AppendActivityEntry(ctx, e)
		if err != nil {
			t.Fatalf("AppendActivityEntry(%d): %v", i, err)
		}
		if id <= 0 {
			t.Fatalf("AppendActivityEntry(%d): id = %d, want > 0", i, id)
		}
		ids = append(ids, id)
	}
	if !(ids[0] < ids[1] && ids[1] < ids[2]) {
		t.Fatalf("expected monotonically increasing ids, got %v", ids)
	}

	// No filter: all three, newest first.
	all, total, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("QueryActivityEntries (all): %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}
	if all[0].Name != "Failed login attempt by mallory" || all[2].Name != "Failed login attempt by alice" {
		t.Fatalf("expected newest-first ordering, got %v, %v, %v", all[0].Name, all[1].Name, all[2].Name)
	}

	// hasUserId=true: only the successful login.
	hasUser := true
	withUser, total, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 10, HasUserID: &hasUser})
	if err != nil {
		t.Fatalf("QueryActivityEntries (hasUserId=true): %v", err)
	}
	if total != 1 || len(withUser) != 1 {
		t.Fatalf("hasUserId=true: total=%d len=%d, want 1/1", total, len(withUser))
	}
	if withUser[0].UserID == nil || *withUser[0].UserID != uid {
		t.Fatalf("hasUserId=true entry UserID = %v, want %q", withUser[0].UserID, uid)
	}
	if withUser[0].ShortOverview != "Remote IP: 127.0.0.1" {
		t.Errorf("ShortOverview = %q, want %q", withUser[0].ShortOverview, "Remote IP: 127.0.0.1")
	}

	// hasUserId=false: the two failed logins, still newest first.
	noUser := false
	withoutUser, total, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 10, HasUserID: &noUser})
	if err != nil {
		t.Fatalf("QueryActivityEntries (hasUserId=false): %v", err)
	}
	if total != 2 || len(withoutUser) != 2 {
		t.Fatalf("hasUserId=false: total=%d len=%d, want 2/2", total, len(withoutUser))
	}
	for _, e := range withoutUser {
		if e.UserID != nil {
			t.Errorf("hasUserId=false entry has non-nil UserID: %v", *e.UserID)
		}
	}
	if withoutUser[0].Name != "Failed login attempt by mallory" {
		t.Errorf("withoutUser[0].Name = %q, want the newer failed attempt", withoutUser[0].Name)
	}

	// MinDate excludes the oldest entry.
	minDate := base.Add(1 * time.Hour)
	afterMinDate, total, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 10, MinDate: &minDate})
	if err != nil {
		t.Fatalf("QueryActivityEntries (MinDate): %v", err)
	}
	if total != 2 || len(afterMinDate) != 2 {
		t.Fatalf("MinDate filter: total=%d len=%d, want 2/2", total, len(afterMinDate))
	}
	for _, e := range afterMinDate {
		if e.Name == "Failed login attempt by alice" {
			t.Errorf("MinDate filter should have excluded the oldest entry, got %v", e.Name)
		}
	}

	// startIndex/limit paging: total stays 3 (pre-pagination count) even
	// though Items only carries one entry per page.
	page1, total, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("QueryActivityEntries (page1): %v", err)
	}
	if total != 3 || len(page1) != 1 || page1[0].Name != "Failed login attempt by mallory" {
		t.Fatalf("page1 = %+v total=%d, want the newest entry alone with total 3", page1, total)
	}
	page2, total, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("QueryActivityEntries (page2): %v", err)
	}
	if total != 3 || len(page2) != 1 || page2[0].Name != "alice is online from TestClient" {
		t.Fatalf("page2 = %+v total=%d, want the middle entry alone with total 3", page2, total)
	}
}

func TestQueryActivityEntriesRequiresPositiveLimit(t *testing.T) {
	st := openTestStore(t)
	if _, _, err := st.QueryActivityEntries(context.Background(), storage.ActivityEntryFilter{Limit: 0}); err == nil {
		t.Fatal("QueryActivityEntries with Limit=0: want an error, got nil")
	}
}

// TestActivityEntryItemIDRoundTrip: a playback entry names the item it is
// about; entries that are not about an item have none.
func TestActivityEntryItemIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	uid := storage.UserID("0123456789abcdef0123456789abcdef")
	itemID := storage.ItemID("fedcba9876543210fedcba9876543210")
	now := time.Now().UTC()
	for _, e := range []storage.ActivityEntry{
		{Name: "login", Type: storage.ActivityTypeSessionStarted, Date: now, UserID: &uid, Severity: storage.ActivitySeverityInfo},
		{Name: "playing", Type: storage.ActivityTypeVideoPlayback, Date: now.Add(time.Second), UserID: &uid, ItemID: &itemID, Severity: storage.ActivitySeverityInfo},
	} {
		if _, err := st.AppendActivityEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := st.QueryActivityEntries(ctx, storage.ActivityEntryFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ItemID == nil || *got[0].ItemID != itemID || got[1].ItemID != nil {
		t.Fatalf("entries = %+v", got)
	}
}
