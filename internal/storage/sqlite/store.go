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
	"sync"
	"sync/atomic"

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

	// writeMu serializes every top-level write transaction (withTx, Tx)
	// across goroutines. SQLite only ever allows one writer at a time
	// regardless — WAL mode's real concurrency benefit is concurrent
	// *readers* (why New doesn't cap the connection pool to 1, unlike the
	// usual blanket advice for Go+SQLite), never concurrent writers — but
	// relying on the DSN's busy_timeout pragma to arbitrate that across
	// multiple pooled connections turned out not to be reliable in
	// practice: internal/scan's bounded concurrent scanner (its first
	// caller to issue writes from more than one goroutine at once)
	// reproduced "database is locked (SQLITE_BUSY)" on a plain
	// single-statement write racing another goroutine's still-open
	// transaction, well within the 5s busy_timeout window, under
	// modernc.org/sqlite. An explicit Go-level mutex removes the
	// ambiguity entirely: a second writer's BeginTx simply blocks until
	// the first commits, so SQLite itself never sees two connections
	// contend for the write lock.
	writeMu sync.Mutex

	// namedFilled is set once named_items has been rebuilt from the items
	// (FindNamed); shared by the transactional views of one store.
	namedFilled *atomic.Bool
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

	return &Store{db: db, ex: db, namedFilled: new(atomic.Bool)}, nil
}

// withTx runs fn against a single transaction, reusing s.ex's existing
// *sql.Tx if the Store is already bound to one (nested inside a caller's
// Tx call) rather than nesting a second transaction, or opening a fresh
// one against s.db otherwise. A caller (UpsertItem) that touches more than
// one table across more than one ExecContext call needs this: two
// unrelated statements issued straight against s.db (the top-level case,
// a pooled *sql.DB) can each land on a different pooled connection and
// interleave with a concurrent caller's own statements — exactly what let
// two goroutines' concurrent UpsertItem calls for a parent item and one of
// its children each half-complete recomputeAncestors for the same child
// row, tripping item_ancestors' UNIQUE constraint (found via
// internal/scan's bounded concurrent scanner, the first caller to upsert
// unrelated items from more than one goroutine at once). Wrapping the
// whole multi-statement sequence in one transaction, plus writeMu
// (Store's doc comment) serializing that transaction against every other
// top-level writer, fixes it structurally: two concurrent callers now
// fully serialize instead of interleaving or racing SQLite's own
// busy-timeout retry loop.
func (s *Store) withTx(ctx context.Context, fn func(dbtx) error) error {
	if tx, ok := s.ex.(*sql.Tx); ok {
		return fn(tx)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: beginning transaction: %w", err)
	}
	if err := fn(tx); err != nil {
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

// exec runs a single write statement, serialized via writeMu against every
// other top-level writer (same reasoning as withTx) — the single-statement
// counterpart to withTx for every write method that doesn't need a
// multi-statement transaction. Every entity file's Upsert*/Delete*/Create*/
// Update*/Issue*/Revoke*/Set* method routes its write through this instead
// of calling s.ex.ExecContext directly.
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx, ok := s.ex.(*sql.Tx); ok {
		return tx.ExecContext(ctx, query, args...)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.ex.ExecContext(ctx, query, args...)
}

// Tx runs fn against a Store bound to a single transaction (storage.Store).
// Locks writeMu for the same reason withTx does — Tx is a second,
// independent top-level entry point into a write transaction, not routed
// through withTx itself (fn needs a storage.Store, not a bare dbtx).
func (s *Store) Tx(ctx context.Context, fn func(storage.Store) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: beginning transaction: %w", err)
	}

	txStore := &Store{db: s.db, ex: tx, namedFilled: s.namedFilled}
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
