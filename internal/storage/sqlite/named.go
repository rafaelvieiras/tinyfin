package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Named items (genres, studios, people, years) are derived from the items
// that carry the names. named_items maps the identifier of a name back to
// the name; the carriers are read straight from the items' JSON columns.

// namedCarrier describes how a kind's names are read off an item row: the
// join that unnests them, the SQL expression of the name, and an extra
// predicate every carrier row must satisfy.
type namedCarrier struct {
	join  string
	name  string
	where string
}

func carrierFor(kind storage.NamedKind) (namedCarrier, bool) {
	switch kind {
	case storage.NamedGenre:
		return namedCarrier{join: "JOIN json_each(items.genres) je", name: "je.value", where: "je.value <> ''"}, true
	case storage.NamedStudio:
		return namedCarrier{join: "JOIN json_each(items.studios) je", name: "je.value", where: "je.value <> ''"}, true
	case storage.NamedPerson:
		return namedCarrier{join: "JOIN json_each(items.people) je", name: "json_extract(je.value,'$.Name')",
			where: "COALESCE(json_extract(je.value,'$.Name'), '') <> ''"}, true
	case storage.NamedYear:
		return namedCarrier{name: "CAST(items.production_year AS TEXT)", where: "items.production_year IS NOT NULL"}, true
	}
	return namedCarrier{}, false
}

// namedFromClause renders "FROM items <carrier join> <scope joins>
// <scope where> [AND extra...]" and its bind values. empty reports a scope
// nothing satisfies.
func namedFromClause(c namedCarrier, scope storage.ItemFilter, extra []string, extraArgs []any) (string, []any, bool) {
	parts, empty := buildItemParts(scope)
	if empty {
		return "", nil, true
	}
	where := parts.whereSQL
	conds := append([]string{}, extra...)
	if c.where != "" {
		conds = append([]string{c.where}, conds...)
	}
	if len(conds) > 0 {
		if where == "" {
			where = "WHERE " + strings.Join(conds, " AND ")
		} else {
			where += " AND " + strings.Join(conds, " AND ")
		}
	}
	args := append(append([]any{}, parts.args...), extraArgs...)
	return "FROM items " + c.join + " " + parts.joinSQL + " " + where, args, false
}

func (s *Store) QueryNamed(ctx context.Context, q storage.NamedQuery) (storage.NamedPage, error) {
	c, ok := carrierFor(q.Kind)
	if !ok {
		return storage.NamedPage{}, fmt.Errorf("sqlite: QueryNamed: unsupported kind %q", q.Kind)
	}
	var extra []string
	var extraArgs []any
	if q.Kind == storage.NamedPerson {
		if len(q.PersonTypes) > 0 {
			extra = append(extra, "lower(json_extract(je.value,'$.Type')) IN ("+sqlPlaceholders(len(q.PersonTypes))+")")
			for _, t := range q.PersonTypes {
				extraArgs = append(extraArgs, strings.ToLower(t))
			}
		}
		if len(q.ExcludePersonTypes) > 0 {
			extra = append(extra, "lower(COALESCE(json_extract(je.value,'$.Type'),'')) NOT IN ("+sqlPlaceholders(len(q.ExcludePersonTypes))+")")
			for _, t := range q.ExcludePersonTypes {
				extraArgs = append(extraArgs, strings.ToLower(t))
			}
		}
		if q.AppearsIn != nil {
			extra = append(extra, "items.id = ?")
			extraArgs = append(extraArgs, string(*q.AppearsIn))
		}
	}
	from, args, empty := namedFromClause(c, q.Scope, extra, extraArgs)
	if empty {
		return storage.NamedPage{}, nil
	}

	// Inner: one row per (name, carrying item). A genre or studio of a
	// series is also carried by its episodes: they are counted under it but
	// do not make the name appear (own = 0).
	branch := "SELECT lower(" + c.name + ") AS k, " + c.name + " AS nm, items.id AS iid, items.type AS typ, items.rowid AS rid, items.date_created AS dc, 1 AS own " + from
	innerArgs := args
	if q.Kind == storage.NamedGenre || q.Kind == storage.NamedStudio {
		col := "genres"
		if q.Kind == storage.NamedStudio {
			col = "studios"
		}
		c2 := namedCarrier{join: "JOIN items ser ON ser.id = items.series_id JOIN json_each(ser." + col + ") je", name: "je.value", where: "je.value <> '' AND items.type = 'Episode'"}
		from2, args2, empty2 := namedFromClause(c2, q.Scope, extra, extraArgs)
		if !empty2 {
			branch += " UNION ALL SELECT lower(je.value), je.value, items.id, items.type, items.rowid, items.date_created, 0 " + from2
			innerArgs = append(append([]any{}, args...), args2...)
		}
	}
	inner := "SELECT k, nm, iid, typ, rid, dc, MAX(own) AS own FROM (" + branch + ") GROUP BY k, iid"
	grouped := "SELECT MIN(nm) AS nm, MIN(rid) AS first, " +
		"SUM(typ = 'Movie') AS movies, SUM(typ = 'Series') AS series, SUM(typ = 'Episode') AS episodes, SUM(typ = 'Playlist') AS playlists, COUNT(*) AS total, MIN(dc) AS created " +
		"FROM (" + inner + ") GROUP BY k HAVING MAX(own) = 1"

	var conds []string
	var condArgs []any
	if t := storage.SearchKey(q.SearchTerm); strings.TrimSpace(q.SearchTerm) != "" {
		if t == "" {
			conds = append(conds, "0")
		} else {
			conds = append(conds, "instr(search_key(nm), ?) > 0")
			condArgs = append(condArgs, t)
		}
	}
	if q.NameStartsWith != "" {
		conds = append(conds, "lower(nm) LIKE ? ESCAPE '\\'")
		condArgs = append(condArgs, escapeLike(strings.ToLower(q.NameStartsWith))+"%")
	}
	if q.NameStartsWithOrGreater != "" {
		conds = append(conds, "lower(nm) >= ?")
		condArgs = append(condArgs, strings.ToLower(q.NameStartsWithOrGreater))
	}
	if q.NameLessThan != "" {
		conds = append(conds, "lower(nm) < ?")
		condArgs = append(condArgs, strings.ToLower(q.NameLessThan))
	}
	outer := "SELECT nm, first, movies, series, episodes, playlists, total, created FROM (" + grouped + ")"
	if len(conds) > 0 {
		outer += " WHERE " + strings.Join(conds, " AND ")
	}
	allArgs := append(append([]any{}, innerArgs...), condArgs...)

	var total int
	if err := s.ex.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+outer+")", allArgs...).Scan(&total); err != nil {
		return storage.NamedPage{}, fmt.Errorf("sqlite: counting named items: %w", err)
	}

	dir := "ASC"
	if q.Descending {
		dir = "DESC"
	}
	var order string
	switch {
	case q.ByName && q.Kind == storage.NamedYear:
		order = "CAST(nm AS INTEGER) " + dir
	case q.ByName:
		order = "lower(nm) " + dir
	default:
		order = "first " + dir
		if q.Kind != storage.NamedYear {
			// first appearance is the natural order only for years; the
			// other kinds list by name unless asked otherwise.
			order = "lower(nm) " + dir
		}
	}
	page := outer + " ORDER BY " + order + ", lower(nm) ASC"
	pageArgs := allArgs
	if q.Limit > 0 {
		page += " LIMIT ? OFFSET ?"
		pageArgs = append(append([]any{}, allArgs...), q.Limit, q.Offset)
	} else if q.Offset > 0 {
		page += " LIMIT -1 OFFSET ?"
		pageArgs = append(append([]any{}, allArgs...), q.Offset)
	}
	rows, err := s.ex.QueryContext(ctx, page, pageArgs...)
	if err != nil {
		return storage.NamedPage{}, fmt.Errorf("sqlite: querying named items: %w", err)
	}
	defer rows.Close()
	out := storage.NamedPage{Total: total}
	for rows.Next() {
		var e storage.NamedEntry
		var first int64
		var created sql.NullString
		if err := rows.Scan(&e.Name, &first, &e.Movies, &e.Series, &e.Episodes, &e.Playlists, &e.Total, &created); err != nil {
			return storage.NamedPage{}, fmt.Errorf("sqlite: scanning named item: %w", err)
		}
		e.Kind = q.Kind
		e.ID = storage.NamedID(q.Kind, e.Name)
		if created.Valid {
			if t, err := parseAggregateTime(created.String); err == nil {
				e.Created = t
			}
		}
		e.Others = e.Total - e.Movies - e.Series - e.Episodes - e.Playlists
		out.Entries = append(out.Entries, e)
	}
	return out, rows.Err()
}

func (s *Store) CountNamed(ctx context.Context, ref storage.NamedRef, scope storage.ItemFilter) (storage.NamedEntry, error) {
	c, ok := carrierFor(ref.Kind)
	if !ok {
		return storage.NamedEntry{}, fmt.Errorf("sqlite: CountNamed: unsupported kind %q", ref.Kind)
	}
	out := storage.NamedEntry{NamedRef: ref}
	cond := "lower(" + c.name + ") = ?"
	args := []any{strings.ToLower(ref.Name)}
	from, fargs, empty := namedFromClause(c, scope, []string{cond}, args)
	if empty {
		return out, nil
	}
	inner := "SELECT DISTINCT items.id AS iid, items.type AS typ, items.date_created AS dc " + from
	innerArgs := fargs
	if ref.Kind == storage.NamedGenre || ref.Kind == storage.NamedStudio {
		col := "genres"
		if ref.Kind == storage.NamedStudio {
			col = "studios"
		}
		c2 := namedCarrier{join: "JOIN items ser ON ser.id = items.series_id JOIN json_each(ser." + col + ") je", name: "je.value", where: "je.value <> '' AND items.type = 'Episode'"}
		from2, args2, empty2 := namedFromClause(c2, scope, []string{cond}, args)
		if !empty2 {
			inner += " UNION SELECT DISTINCT items.id, items.type, items.date_created " + from2
			innerArgs = append(append([]any{}, fargs...), args2...)
		}
	}
	q := "SELECT COALESCE(SUM(typ = 'Movie'),0), COALESCE(SUM(typ = 'Series'),0), COALESCE(SUM(typ = 'Episode'),0), COALESCE(SUM(typ = 'Playlist'),0), COUNT(*), MIN(dc) FROM (" + inner + ")"
	var created sql.NullString
	if err := s.ex.QueryRowContext(ctx, q, innerArgs...).Scan(&out.Movies, &out.Series, &out.Episodes, &out.Playlists, &out.Total, &created); err != nil {
		return out, fmt.Errorf("sqlite: counting a named item: %w", err)
	}
	if created.Valid {
		if t, err := parseAggregateTime(created.String); err == nil {
			out.Created = t
		}
	}
	out.Others = out.Total - out.Movies - out.Series - out.Episodes - out.Playlists
	return out, nil
}

// registerNamed records the names item carries in named_items.
func registerNamed(ctx context.Context, ex dbtx, item storage.Item) error {
	type row struct{ id, kind, name string }
	var rows []row
	seen := map[string]bool{}
	add := func(kind storage.NamedKind, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		id := string(storage.NamedID(kind, name))
		if seen[id] {
			return
		}
		seen[id] = true
		rows = append(rows, row{id, string(kind), name})
	}
	for _, g := range item.Genres {
		add(storage.NamedGenre, g)
	}
	for _, st := range item.Studios {
		add(storage.NamedStudio, st)
	}
	for _, p := range item.People {
		add(storage.NamedPerson, p.Name)
	}
	if item.ProductionYear != nil {
		add(storage.NamedYear, strconv.Itoa(*item.ProductionYear))
	}
	for len(rows) > 0 {
		n := len(rows)
		if n > 100 {
			n = 100
		}
		args := make([]any, 0, n*3)
		ph := make([]string, n)
		for i, r := range rows[:n] {
			ph[i] = "(?,?,?)"
			args = append(args, r.id, r.kind, r.name)
		}
		if _, err := ex.ExecContext(ctx, "INSERT OR IGNORE INTO named_items (id, kind, name) VALUES "+strings.Join(ph, ","), args...); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return nil
}

func (s *Store) FindNamed(ctx context.Context, id storage.ItemID) (storage.NamedRef, bool, error) {
	look := func() (storage.NamedRef, bool, error) {
		var kind, name string
		err := s.ex.QueryRowContext(ctx, `SELECT kind, name FROM named_items WHERE id = ?`, string(id)).Scan(&kind, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return storage.NamedRef{}, false, nil
		}
		if err != nil {
			return storage.NamedRef{}, false, fmt.Errorf("sqlite: finding a named item: %w", err)
		}
		return storage.NamedRef{Kind: storage.NamedKind(kind), Name: name, ID: id}, true, nil
	}
	ref, ok, err := look()
	if err != nil || ok {
		return ref, ok, err
	}
	// A miss on a database filled before named_items existed: index every
	// name once, then ask again.
	if s.namedFilled != nil && s.namedFilled.CompareAndSwap(false, true) {
		if err := s.backfillNamed(ctx); err != nil {
			s.namedFilled.Store(false)
			return storage.NamedRef{}, false, err
		}
		return look()
	}
	return storage.NamedRef{}, false, nil
}

func (s *Store) backfillNamed(ctx context.Context) error {
	return s.withTx(ctx, func(ex dbtx) error {
		for _, q := range []struct {
			kind storage.NamedKind
			sql  string
		}{
			{storage.NamedGenre, `SELECT DISTINCT je.value FROM items, json_each(items.genres) je WHERE je.value <> ''`},
			{storage.NamedStudio, `SELECT DISTINCT je.value FROM items, json_each(items.studios) je WHERE je.value <> ''`},
			{storage.NamedPerson, `SELECT DISTINCT json_extract(je.value,'$.Name') FROM items, json_each(items.people) je WHERE COALESCE(json_extract(je.value,'$.Name'),'') <> ''`},
			{storage.NamedYear, `SELECT DISTINCT CAST(production_year AS TEXT) FROM items WHERE production_year IS NOT NULL`},
		} {
			rows, err := ex.QueryContext(ctx, q.sql)
			if err != nil {
				return err
			}
			var names []string
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					rows.Close()
					return err
				}
				names = append(names, n)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			for _, n := range names {
				if _, err := ex.ExecContext(ctx, `INSERT OR IGNORE INTO named_items (id, kind, name) VALUES (?,?,?)`,
					string(storage.NamedID(q.kind, n)), string(q.kind), n); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
