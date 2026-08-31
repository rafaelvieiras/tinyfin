// Package sqlite implements storage.Store (internal/storage) as a single
// embedded SQLite database file, per docs/05-storage-drivers.md's "embedded
// mode" — no separate process, WAL journal mode, foreign keys enforced,
// FTS5-backed search, and a materialized ancestor-closure table so
// ItemFilter.Recursive is an indexed join rather than a recursive walk
// (docs/05's "Implementation note for Recursive queries").
//
// ID-minting convention: a method that returns a newly minted ID
// (UserStore.CreateUser, UserStore.IssueToken) always mints one, ignoring
// any ID the caller may have set on the input struct. A method that takes a
// struct whose own ID field is the upsert key (UpsertItem, UpsertRelation,
// UpsertImageRef, UpsertDevice, UpsertSubtitle, UpsertLyric, UpsertLibrary)
// mints an ID with newID (id.go) only when the caller left that field
// empty — otherwise it upserts by the given ID. This lets a caller that
// already knows its own stable key (e.g. a scanner deriving an ItemID from
// a file path) supply it, while a caller creating a brand new row without
// one still gets a working ID.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// dbtx is satisfied by both *sql.DB and *sql.Tx, letting every entity method
// below be written once and used both at the top level (against the real
// connection pool) and inside Tx (against a single transaction).
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store implements storage.Store against an embedded SQLite database. The
// zero value is not usable — construct with New.
type Store struct {
	db *sql.DB
	ex dbtx // db itself at the top level; a *sql.Tx when bound by Tx
}

var _ storage.Store = (*Store)(nil)

// New opens (creating if necessary) a SQLite database at path, applies any
// pending embedded migrations, and returns a Store ready to use.
func New(path string) (*Store, error) {
	// _pragma sets WAL journal mode, foreign key enforcement, and a busy
	// timeout on every new connection the pool opens — DSN-level pragmas
	// apply per-connection in modernc.org/sqlite, which matters because
	// database/sql may open more than one connection to the same file.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: opening %s: %w", path, err)
	}

	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, ex: db}, nil
}

// Tx runs fn against a Store bound to a single transaction (storage.Store).
func (s *Store) Tx(ctx context.Context, fn func(storage.Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: beginning transaction: %w", err)
	}

	txStore := &Store{db: s.db, ex: tx}
	if err := fn(txStore); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("sqlite: tx failed (%w) and rollback failed: %v", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: committing transaction: %w", err)
	}
	return nil
}

// Healthy reports whether the database can currently serve reads/writes.
// No read-replica lag concept applies to a single embedded file (docs/05:
// that's scale-mode only) — a PingContext is sufficient here.
func (s *Store) Healthy(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close releases the underlying connection. Safe to call once, at process
// shutdown.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- shared marshal/unmarshal helpers for JSON-encoded columns ---

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("sqlite: marshaling %T: %w", v, err)
	}
	return string(b), nil
}

func unmarshalJSONInto(s string, out any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), out); err != nil {
		return fmt.Errorf("sqlite: unmarshaling into %T: %w", out, err)
	}
	return nil
}
