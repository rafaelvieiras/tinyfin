package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// AppendActivityEntry lets SQLite mint the row's id via AUTOINCREMENT
// (migrations/0005_activity_log.sql) rather than this package's usual newID
// — storage.ActivityEntry.ID's doc comment explains why a plain integer,
// not this project's opaque UUID-shaped ID, is the right shape here.
func (s *Store) AppendActivityEntry(ctx context.Context, entry storage.ActivityEntry) (int64, error) {
	var userID, itemID any
	if entry.UserID != nil {
		userID = string(*entry.UserID)
	}
	if entry.ItemID != nil {
		itemID = string(*entry.ItemID)
	}

	const query = `INSERT INTO activity_log (name, short_overview, type, date, user_id, severity, item_id) VALUES (?, ?, ?, ?, ?, ?, ?)`
	res, err := s.exec(ctx, query, entry.Name, entry.ShortOverview, string(entry.Type), entry.Date, userID, string(entry.Severity), itemID)
	if err != nil {
		return 0, fmt.Errorf("sqlite: appending activity log entry: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: reading inserted activity log entry id: %w", err)
	}
	return id, nil
}

// QueryActivityEntries answers GET /System/ActivityLog/Entries's paging,
// newest-first (see ActivityLogStore's doc comment for why). MinDate/
// HasUserID become an optional SQL WHERE clause; the two are independent
// and can both apply at once (jellyfin-web's own dashboard requests never
// combine them today, but the interface doesn't assume that).
func (s *Store) QueryActivityEntries(ctx context.Context, filter storage.ActivityEntryFilter) ([]storage.ActivityEntry, int, error) {
	if filter.Limit <= 0 {
		return nil, 0, fmt.Errorf("sqlite: QueryActivityEntries: Limit must be > 0, got %d", filter.Limit)
	}

	var conds []string
	var args []any
	if filter.MinDate != nil {
		conds = append(conds, "date >= ?")
		args = append(args, *filter.MinDate)
	}
	if filter.HasUserID != nil {
		if *filter.HasUserID {
			conds = append(conds, "user_id IS NOT NULL")
		} else {
			conds = append(conds, "user_id IS NULL")
		}
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	countRow := s.ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_log `+where, args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sqlite: counting activity log entries: %w", err)
	}

	query := `SELECT id, name, short_overview, type, date, user_id, severity, item_id FROM activity_log ` + where +
		` ORDER BY date DESC, id DESC LIMIT ? OFFSET ?`
	pageArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)

	rows, err := s.ex.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: querying activity log entries: %w", err)
	}
	defer rows.Close()

	var entries []storage.ActivityEntry
	for rows.Next() {
		var (
			e             storage.ActivityEntry
			typ, severity string
			userID        sql.NullString
			itemID        sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.Name, &e.ShortOverview, &typ, &e.Date, &userID, &severity, &itemID); err != nil {
			return nil, 0, fmt.Errorf("sqlite: scanning activity log row: %w", err)
		}
		e.Type = storage.ActivityType(typ)
		e.Severity = storage.ActivitySeverity(severity)
		if userID.Valid {
			uid := storage.UserID(userID.String)
			e.UserID = &uid
		}
		if itemID.Valid {
			iid := storage.ItemID(itemID.String)
			e.ItemID = &iid
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("sqlite: iterating activity log rows: %w", err)
	}
	return entries, total, nil
}

// DeleteActivityEntriesBefore implements the retention sweep of the
// "Clean Activity Log" scheduled task.
func (s *Store) DeleteActivityEntriesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.exec(ctx, `DELETE FROM activity_log WHERE date < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sqlite: deleting old activity log entries: %w", err)
	}
	return res.RowsAffected()
}

// SnapshotTo writes a consistent copy of the database to path (which must
// not exist) with VACUUM INTO; used by the /Backup/Create route.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	if _, err := s.exec(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("sqlite: snapshot to %s: %w", path, err)
	}
	return nil
}
