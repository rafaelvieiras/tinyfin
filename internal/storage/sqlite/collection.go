package sqlite

import (
	"context"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// Collection membership lives in item_relations (kind "collection", parent =
// the BoxSet, child = the member). On top of that, item_ancestors carries one
// (descendant, collection) row for every member and everything below it, so
// the generic recursive listing, the folder leaf counts and the played marks
// work on a collection like on any folder. The rows are derived data:
// rebuildCollection recomputes them from the relations, and recomputeAncestors
// re-adds them whenever an item row is rewritten.

// collectionsHolding returns every collection that holds one of ids as a
// direct member, plus the collections that hold those collections, and so on.
func collectionsHolding(ctx context.Context, ex dbtx, ids []storage.ItemID) ([]storage.ItemID, error) {
	var out []storage.ItemID
	seen := map[storage.ItemID]bool{}
	frontier := ids
	for len(frontier) > 0 {
		args := make([]any, len(frontier))
		for i, id := range frontier {
			args[i] = string(id)
		}
		rows, err := ex.QueryContext(ctx,
			`SELECT DISTINCT parent_id FROM item_relations WHERE kind = 'collection' AND child_id IN (`+sqlPlaceholders(len(args))+`)`, args...)
		if err != nil {
			return nil, err
		}
		var next []storage.ItemID
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return nil, err
			}
			id := storage.ItemID(p)
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
				next = append(next, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		frontier = next
	}
	return out, nil
}

// collectionClosure is the set of items below a collection: its direct
// members, everything below each of them by parent, and (for a member that is
// itself a collection) the closure of that collection. Cycles are cut by the
// visited set.
func collectionClosure(ctx context.Context, ex dbtx, id storage.ItemID, visited map[storage.ItemID]bool, out map[storage.ItemID]bool) error {
	if visited[id] {
		return nil
	}
	visited[id] = true
	rows, err := ex.QueryContext(ctx,
		`SELECT r.child_id, i.type FROM item_relations r JOIN items i ON i.id = r.child_id
		 WHERE r.parent_id = ? AND r.kind = 'collection'`, string(id))
	if err != nil {
		return err
	}
	type member struct {
		id  storage.ItemID
		typ string
	}
	var members []member
	for rows.Next() {
		var m member
		var cid string
		if err := rows.Scan(&cid, &m.typ); err != nil {
			rows.Close()
			return err
		}
		m.id = storage.ItemID(cid)
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, m := range members {
		out[m.id] = true
		if storage.ItemType(m.typ) == storage.ItemTypeBoxSet {
			if err := collectionClosure(ctx, ex, m.id, visited, out); err != nil {
				return err
			}
			continue
		}
		drows, err := ex.QueryContext(ctx, `SELECT item_id FROM item_ancestors WHERE ancestor_id = ?`, string(m.id))
		if err != nil {
			return err
		}
		for drows.Next() {
			var d string
			if err := drows.Scan(&d); err != nil {
				drows.Close()
				return err
			}
			out[storage.ItemID(d)] = true
		}
		if err := drows.Err(); err != nil {
			drows.Close()
			return err
		}
		drows.Close()
	}
	return nil
}

// rebuildCollection recomputes the ancestor rows of one collection and of
// every collection that (transitively) holds it.
func rebuildCollection(ctx context.Context, ex dbtx, id storage.ItemID) error {
	targets := append([]storage.ItemID{id}, nil...)
	outer, err := collectionsHolding(ctx, ex, []storage.ItemID{id})
	if err != nil {
		return err
	}
	// Inner collections first: an outer closure reads the inner one's rows
	// only through relations, so the order is not required for
	// correctness, but it keeps the intermediate state consistent.
	targets = append(targets, outer...)
	for _, t := range targets {
		if _, err := ex.ExecContext(ctx, `DELETE FROM item_ancestors WHERE ancestor_id = ?`, string(t)); err != nil {
			return err
		}
		closure := map[storage.ItemID]bool{}
		if err := collectionClosure(ctx, ex, t, map[storage.ItemID]bool{}, closure); err != nil {
			return err
		}
		for m := range closure {
			if m == t {
				continue
			}
			if _, err := ex.ExecContext(ctx, `INSERT OR IGNORE INTO item_ancestors (item_id, ancestor_id) VALUES (?, ?)`, string(m), string(t)); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectionMembers(ctx context.Context, ex dbtx, id storage.ItemID) ([]storage.ItemID, error) {
	rows, err := ex.QueryContext(ctx,
		`SELECT child_id FROM item_relations WHERE parent_id = ? AND kind = 'collection' ORDER BY sort_index ASC, rowid ASC`, string(id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.ItemID
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, storage.ItemID(c))
	}
	return out, rows.Err()
}

func relationID(parent, child storage.ItemID) string {
	return "collection:" + string(parent) + ":" + string(child)
}

func (s *Store) CollectionMembers(ctx context.Context, collectionID storage.ItemID) ([]storage.ItemID, error) {
	return collectionMembers(ctx, s.ex, collectionID)
}

func (s *Store) AddToCollection(ctx context.Context, collectionID storage.ItemID, memberIDs []storage.ItemID) ([]storage.ItemID, error) {
	var added []storage.ItemID
	err := s.withTx(ctx, func(ex dbtx) error {
		var next int
		if err := ex.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(sort_index), -1) + 1 FROM item_relations WHERE parent_id = ? AND kind = 'collection'`,
			string(collectionID)).Scan(&next); err != nil {
			return err
		}
		for _, m := range memberIDs {
			if m == collectionID {
				continue
			}
			res, err := ex.ExecContext(ctx,
				`INSERT OR IGNORE INTO item_relations (id, parent_id, child_id, kind, sort_index) VALUES (?, ?, ?, 'collection', ?)`,
				relationID(collectionID, m), string(collectionID), string(m), next)
			if err != nil {
				return fmt.Errorf("sqlite: adding %q to collection %q: %w", m, collectionID, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				added = append(added, m)
				next++
			}
		}
		if len(added) == 0 {
			return nil
		}
		return rebuildCollection(ctx, ex, collectionID)
	})
	return added, err
}

func (s *Store) RemoveFromCollection(ctx context.Context, collectionID storage.ItemID, memberIDs []storage.ItemID) error {
	return s.withTx(ctx, func(ex dbtx) error {
		removed := false
		for _, m := range memberIDs {
			res, err := ex.ExecContext(ctx,
				`DELETE FROM item_relations WHERE parent_id = ? AND child_id = ? AND kind = 'collection'`, string(collectionID), string(m))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				removed = true
			}
		}
		if !removed {
			return nil
		}
		return rebuildCollection(ctx, ex, collectionID)
	})
}

func (s *Store) SetCollectionMembers(ctx context.Context, collectionID storage.ItemID, memberIDs []storage.ItemID) error {
	return s.withTx(ctx, func(ex dbtx) error {
		if _, err := ex.ExecContext(ctx, `DELETE FROM item_relations WHERE parent_id = ? AND kind = 'collection'`, string(collectionID)); err != nil {
			return err
		}
		next := 0
		for _, m := range memberIDs {
			if m == collectionID {
				continue
			}
			res, err := ex.ExecContext(ctx,
				`INSERT OR IGNORE INTO item_relations (id, parent_id, child_id, kind, sort_index) VALUES (?, ?, ?, 'collection', ?)`,
				relationID(collectionID, m), string(collectionID), string(m), next)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				next++
			}
		}
		return rebuildCollection(ctx, ex, collectionID)
	})
}

func (s *Store) MoviesWithSet(ctx context.Context, libraryIDs []storage.LibraryID) ([]storage.SetGroup, error) {
	if len(libraryIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(libraryIDs))
	for i, id := range libraryIDs {
		args[i] = string(id)
	}
	rows, err := s.ex.QueryContext(ctx,
		`SELECT set_name, id FROM items WHERE type = 'Movie' AND set_name <> '' AND library_id IN (`+sqlPlaceholders(len(args))+`)
		 ORDER BY production_year ASC, sort_name ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying set names: %w", err)
	}
	defer rows.Close()
	var out []storage.SetGroup
	index := map[string]int{}
	for rows.Next() {
		var name, id string
		if err := rows.Scan(&name, &id); err != nil {
			return nil, err
		}
		i, ok := index[name]
		if !ok {
			i = len(out)
			index[name] = i
			out = append(out, storage.SetGroup{Name: name})
		}
		out[i].Members = append(out[i].Members, storage.ItemID(id))
	}
	return out, rows.Err()
}
