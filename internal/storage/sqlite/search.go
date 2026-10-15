package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// SearchHints ranks media items and names against a term. The result is the
// union of the media items of q.Items that match and, for each requested
// kind, the distinct names carried by the items of q.Items (item types
// cleared) that match, ordered by relevance (searchRankSQL), then by sort
// name (binary comparison).
func (s *Store) SearchHints(ctx context.Context, q storage.SearchQuery) (storage.SearchPage, error) {
	term := storage.SearchKey(q.Term)
	if term == "" {
		return storage.SearchPage{}, nil
	}
	sortTerm := storage.SortSearchKey(q.Term)

	var selects []string
	var args []any

	if !q.NoMedia {
		parts, empty := buildItemParts(q.Items)
		if !empty {
			rank, rankArgs := searchRankSQL("search_key(items.name)", "items.type", term)
			where := parts.whereSQL
			cond := "(instr(search_key(items.name), ?) > 0 OR instr(items.sort_name, ?) > 0)"
			if where == "" {
				where = "WHERE " + cond
			} else {
				where += " AND " + cond
			}
			selects = append(selects, "SELECT 'i' AS src, items.id AS ref, "+rank+" AS rk, items.sort_name AS sk "+
				"FROM items "+parts.joinSQL+" "+where)
			args = append(args, rankArgs...)
			args = append(args, parts.args...)
			args = append(args, term, sortTerm)
		}
	}

	scope := q.Items
	scope.ItemTypes = nil
	scope.NameContains = ""
	for _, kind := range q.Kinds {
		c, ok := carrierFor(kind)
		if !ok || kind == storage.NamedYear {
			continue
		}
		from, fargs, empty := namedFromClause(c, scope, nil, nil)
		if empty {
			continue
		}
		sk := "lower(nm)"
		if kind == storage.NamedPerson && q.KeepPersonCase {
			sk = "nm"
		}
		rank, rankArgs := searchRankSQL("search_key(nm)", "'"+string(kind)+"'", term)
		selects = append(selects, "SELECT '"+string(kind)+"' AS src, nm AS ref, "+rank+" AS rk, "+sk+" AS sk "+
			"FROM (SELECT MIN("+c.name+") AS nm "+from+" GROUP BY lower("+c.name+")) WHERE instr(search_key(nm), ?) > 0")
		args = append(args, rankArgs...)
		args = append(args, fargs...)
		args = append(args, term)
	}
	if len(selects) == 0 {
		return storage.SearchPage{}, nil
	}
	union := strings.Join(selects, " UNION ALL ")

	var total int
	if err := s.ex.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+union+")", args...).Scan(&total); err != nil {
		return storage.SearchPage{}, fmt.Errorf("sqlite: counting search hits: %w", err)
	}
	page := storage.SearchPage{Total: total}
	if q.Limit < 0 || total == 0 {
		return page, nil
	}
	query := "SELECT src, ref FROM (" + union + ") ORDER BY rk, sk, ref"
	qargs := append([]any{}, args...)
	if q.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		qargs = append(qargs, q.Limit, q.Offset)
	} else if q.Offset > 0 {
		query += " LIMIT -1 OFFSET ?"
		qargs = append(qargs, q.Offset)
	}
	rows, err := s.ex.QueryContext(ctx, query, qargs...)
	if err != nil {
		return storage.SearchPage{}, fmt.Errorf("sqlite: querying search hits: %w", err)
	}
	type hit struct{ src, ref string }
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.src, &h.ref); err != nil {
			rows.Close()
			return storage.SearchPage{}, err
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storage.SearchPage{}, err
	}
	rows.Close()
	for _, h := range hits {
		if h.src == "i" {
			it, err := s.GetItem(ctx, storage.ItemID(h.ref))
			if err != nil {
				continue
			}
			page.Hits = append(page.Hits, storage.SearchHit{Item: &it})
			continue
		}
		kind := storage.NamedKind(h.src)
		page.Hits = append(page.Hits, storage.SearchHit{Named: &storage.NamedRef{Kind: kind, Name: h.ref, ID: storage.NamedID(kind, h.ref)}})
	}
	return page, nil
}
