package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigration0017BackfillsWizardCompleted: a database that already holds
// accounts is recorded as set up, an empty one is left open.
func TestMigration0017BackfillsWizardCompleted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		users int
		want  bool
	}{{"with accounts", 1, true}, {"empty", 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "old.db")+"?_pragma=foreign_keys(ON)")
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
				if m.version >= 17 {
					continue
				}
				if err := applyMigration(ctx, db, m); err != nil {
					t.Fatalf("apply %s: %v", m.name, err)
				}
			}
			for i := 0; i < tc.users; i++ {
				if _, err := db.ExecContext(ctx, `INSERT INTO users (id, name) VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'old')`); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrate(ctx, db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			var v string
			err = db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = 'StartupWizardCompleted'`).Scan(&v)
			if tc.want && (err != nil || v != "true") {
				t.Fatalf("flag = %q, %v; want true", v, err)
			}
			if !tc.want && err != sql.ErrNoRows {
				t.Fatalf("flag = %q, %v; want unset", v, err)
			}
		})
	}
}
