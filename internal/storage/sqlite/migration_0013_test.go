package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigration0013CanonicalIDs seeds a database at schema version 12 with
// dashed ids everywhere, applies the pending migrations and checks every id
// column (and the id lists inside the stored documents) is now 32 lowercase
// hex, with references intact.
func TestMigration0013CanonicalIDs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.version >= 13 {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}

	const (
		libD  = "aaaaaaaa-1111-4222-8333-444444444444"
		itemD = "bbbbbbbb-1111-4222-8333-444444444444"
		kidD  = "cccccccc-1111-4222-8333-444444444444"
		userD = "dddddddd-1111-4222-8333-444444444444"
	)
	hex := func(s string) string {
		out := make([]byte, 0, 32)
		for i := 0; i < len(s); i++ {
			if s[i] != '-' {
				out = append(out, s[i])
			}
		}
		return string(out)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name) VALUES (?, 'L')`, libD)
	exec(`INSERT INTO items (id, type, library_id, name, date_created) VALUES (?, 'Series', ?, 'S', '2026-01-01')`, itemD, libD)
	exec(`INSERT INTO items (id, type, library_id, parent_id, series_id, name, date_created) VALUES (?, 'Episode', ?, ?, ?, 'E', '2026-01-01')`, kidD, libD, itemD, itemD)
	exec(`INSERT INTO item_ancestors (item_id, ancestor_id) VALUES (?, ?)`, kidD, itemD)
	exec(`INSERT INTO users (id, name, policy_json) VALUES (?, 'u', ?)`, userD,
		`{"EnabledFolders":["`+libD+`"],"EnabledDevices":["`+libD+`"],"IsHidden":true}`)
	exec(`INSERT INTO auth_tokens (token, user_id, device_id, issued_at) VALUES ('t', ?, 'dev-1', '2026-01-01')`, userD)
	exec(`INSERT INTO playstates (user_id, item_id, played) VALUES (?, ?, 1)`, userD, kidD)
	exec(`INSERT INTO image_refs (id, item_id, kind, tag) VALUES (?, ?, 'Primary', ?)`, "eeeeeeee-1111-4222-8333-444444444444", itemD, "ffffffff-1111-4222-8333-444444444444")
	exec(`INSERT INTO user_configuration (user_id, json) VALUES (?, ?)`, userD, `{"OrderedViews":["`+libD+`","`+itemD+`"],"SubtitleMode":"Default"}`)

	if err := migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	str := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := db.QueryRowContext(ctx, q, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return s
	}
	if got := str(`SELECT id FROM libraries`); got != hex(libD) {
		t.Errorf("library id = %q", got)
	}
	if got := str(`SELECT parent_id || series_id FROM items WHERE type = 'Episode'`); got != hex(itemD)+hex(itemD) {
		t.Errorf("episode refs = %q", got)
	}
	if got := str(`SELECT library_id FROM items WHERE id = ?`, hex(kidD)); got != hex(libD) {
		t.Errorf("item library = %q", got)
	}
	if got := str(`SELECT item_id || ancestor_id FROM item_ancestors`); got != hex(kidD)+hex(itemD) {
		t.Errorf("ancestors = %q", got)
	}
	if got := str(`SELECT user_id || '/' || device_id FROM auth_tokens`); got != hex(userD)+"/dev-1" {
		t.Errorf("token = %q", got)
	}
	if got := str(`SELECT user_id || item_id FROM playstates`); got != hex(userD)+hex(kidD) {
		t.Errorf("playstate = %q", got)
	}
	if got := str(`SELECT tag FROM image_refs`); got != hex("ffffffff-1111-4222-8333-444444444444") {
		t.Errorf("image tag = %q", got)
	}
	if got := str(`SELECT json_extract(policy_json, '$.EnabledFolders[0]') || '/' || json_extract(policy_json, '$.EnabledDevices[0]') FROM users`); got != hex(libD)+"/"+libD {
		t.Errorf("policy lists = %q", got)
	}
	if got := str(`SELECT json FROM user_configuration`); got != `{"OrderedViews":["`+hex(libD)+`","`+hex(itemD)+`"],"SubtitleMode":"Default"}` {
		t.Errorf("configuration = %q", got)
	}
	if got := str(`SELECT count(*) FROM pragma_foreign_key_check`); got != "0" {
		t.Errorf("foreign_key_check violations = %s", got)
	}
}
