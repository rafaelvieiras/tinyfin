package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// This file answers, in one statement each, the questions baseItemDto asks once
// per item: a picture's refs, the caller's playstate, the played state of a
// folder's leaves, the direct and recursive counts, and the item itself.
//
// It exists for the same reason SeriesRecentItems and ChildItemCounts do: a
// grouped "recently added" row was measured at 640 ms for 16 cards on a
// 6,187-item library, and after the grouping path was batched the remaining
// cost was the DTO — seven statements per card (two picture lookups, a
// playstate, a folder's leaf counts, two counts, and one lookup per ancestor).
// Each function here is the batched form of an existing per-item query and must
// keep that query's semantics exactly: the filters below repeat the ones
// QueryItems applies, so a batched answer and a per-item answer cannot disagree.

// ImageRefsFor answers GetImageRefs for many items and kinds at once. The
// result is keyed by item, then by kind, with each kind's refs in index order
// like the single-item call.
func (s *Store) ImageRefsFor(ctx context.Context, itemIDs []storage.ItemID, kinds []storage.ImageKind) (map[storage.ItemID]map[storage.ImageKind][]storage.ImageRef, error) {
	out := make(map[storage.ItemID]map[storage.ImageKind][]storage.ImageRef, len(itemIDs))
	if len(itemIDs) == 0 || len(kinds) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(itemIDs)+len(kinds))
	for _, id := range itemIDs {
		args = append(args, string(id))
	}
	for _, k := range kinds {
		args = append(args, string(k))
	}
	query := `SELECT id, item_id, kind, idx, path, tag, blur_hash, width, height, size FROM image_refs
		WHERE item_id IN (` + sqlPlaceholders(len(itemIDs)) + `) AND kind IN (` + sqlPlaceholders(len(kinds)) + `)
		ORDER BY item_id ASC, kind ASC, idx ASC`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying image refs for %d items: %w", len(itemIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref storage.ImageRef
		var id, item, kind string
		if err := rows.Scan(&id, &item, &kind, &ref.Index, &ref.Path, &ref.Tag, &ref.BlurHash, &ref.Width, &ref.Height, &ref.Size); err != nil {
			return nil, fmt.Errorf("sqlite: scanning image ref row: %w", err)
		}
		ref.ID, ref.ItemID, ref.Kind = storage.ImageRefID(id), storage.ItemID(item), storage.ImageKind(kind)
		if out[ref.ItemID] == nil {
			out[ref.ItemID] = map[storage.ImageKind][]storage.ImageRef{}
		}
		out[ref.ItemID][ref.Kind] = append(out[ref.ItemID][ref.Kind], ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating image refs: %w", err)
	}
	return out, nil
}

// PlaystatesFor answers GetPlaystate for many items at once, for one user. An
// item with no row is absent from the map, the same "never played, not
// favorited" zero state the single-item call returns.
func (s *Store) PlaystatesFor(ctx context.Context, userID storage.UserID, itemIDs []storage.ItemID) (map[storage.ItemID]storage.Playstate, error) {
	out := make(map[storage.ItemID]storage.Playstate, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, string(userID))
	for _, id := range itemIDs {
		args = append(args, string(id))
	}
	query := `SELECT item_id, played, playback_position_ticks, play_count, is_favorite, last_played_date, rating, likes, folder_marked_at
		FROM playstates WHERE user_id = ? AND item_id IN (` + sqlPlaceholders(len(itemIDs)) + `)`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying playstates for %d items: %w", len(itemIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id             string
			ps             storage.Playstate
			lastPlayedDate sql.NullTime
			rating         sql.NullFloat64
			likes          sql.NullBool
			markedAt       sql.NullInt64
		)
		if err := rows.Scan(&id, &ps.Played, &ps.PlaybackPositionTicks, &ps.PlayCount, &ps.IsFavorite, &lastPlayedDate, &rating, &likes, &markedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scanning playstate row: %w", err)
		}
		if lastPlayedDate.Valid {
			v := lastPlayedDate.Time
			ps.LastPlayedDate = &v
		}
		if rating.Valid {
			v := rating.Float64
			ps.Rating = &v
		}
		if likes.Valid {
			v := likes.Bool
			ps.Likes = &v
		}
		if markedAt.Valid {
			ps.FolderMarkedAt = markedAt.Int64
		}
		out[storage.ItemID(id)] = ps
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating playstates: %w", err)
	}
	return out, nil
}

// LeafCountsFor answers FolderLeafCounts for many folders at once: how many
// leaves sit below each folder and how many of them are unplayed, seen through
// the same ancestor folder marks effectivePlayedSQL applies. A folder with no
// leaves is absent from the map.
func (s *Store) LeafCountsFor(ctx context.Context, userID storage.UserID, folderIDs []storage.ItemID) (map[storage.ItemID]storage.LeafCount, error) {
	out := make(map[storage.ItemID]storage.LeafCount, len(folderIDs))
	if len(folderIDs) == 0 {
		return out, nil
	}
	u := string(userID)
	args := make([]any, 0, len(folderIDs)+3)
	args = append(args, u, u, u)
	for _, id := range folderIDs {
		args = append(args, string(id))
	}
	query := `SELECT anc, COUNT(*), COALESCE(SUM(CASE WHEN eff = 0 THEN 1 ELSE 0 END), 0) FROM (
		SELECT a.ancestor_id AS anc, ` + effectivePlayedSQL("leaf.id") + ` AS eff
		FROM item_ancestors a JOIN items leaf ON leaf.id = a.item_id AND leaf.type NOT IN ` + folderTypeSQL() + `
		WHERE a.ancestor_id IN (` + sqlPlaceholders(len(folderIDs)) + `)) GROUP BY anc`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: counting leaves of %d folders: %w", len(folderIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			c  storage.LeafCount
		)
		if err := rows.Scan(&id, &c.Total, &c.Unplayed); err != nil {
			return nil, fmt.Errorf("sqlite: scanning leaf counts: %w", err)
		}
		out[storage.ItemID(id)] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating leaf counts: %w", err)
	}
	return out, nil
}

// ChildCountsFor answers childCountOf's direct-children count for many parents
// at once: the items whose ParentID is the parent, plus the members a
// collection links (item_relations, kind "collection"), with extras excluded —
// the same three clauses the single-parent query builds. A parent with no
// children is absent from the map.
func (s *Store) ChildCountsFor(ctx context.Context, parentIDs []storage.ItemID) (map[storage.ItemID]int, error) {
	out := make(map[storage.ItemID]int, len(parentIDs))
	if len(parentIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, 2*len(parentIDs))
	for _, id := range parentIDs {
		args = append(args, string(id))
	}
	for _, id := range parentIDs {
		args = append(args, string(id))
	}
	ph := sqlPlaceholders(len(parentIDs))
	query := `SELECT p, COUNT(DISTINCT id) FROM (
		SELECT items.parent_id AS p, items.id AS id FROM items WHERE items.parent_id IN (` + ph + `) AND items.extra_type = ''
		UNION ALL
		SELECT lm.parent_id AS p, items.id AS id FROM item_relations lm JOIN items ON items.id = lm.child_id
		WHERE lm.parent_id IN (` + ph + `) AND lm.kind = 'collection' AND items.extra_type = '') GROUP BY p`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: counting children of %d items: %w", len(parentIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("sqlite: scanning child counts: %w", err)
		}
		out[storage.ItemID(id)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating child counts: %w", err)
	}
	return out, nil
}

// OwnedExtraCountsFor answers the merged-season lookup childCountOf adds for a
// series: how many extras of one type the owner holds. Those rows are owned,
// not parented, so they are not part of ChildCountsFor.
func (s *Store) OwnedExtraCountsFor(ctx context.Context, ownerIDs []storage.ItemID, extraType string) (map[storage.ItemID]int, error) {
	out := make(map[storage.ItemID]int, len(ownerIDs))
	if len(ownerIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ownerIDs)+1)
	for _, id := range ownerIDs {
		args = append(args, string(id))
	}
	args = append(args, extraType)
	query := `SELECT items.owner_id, COUNT(*) FROM items WHERE items.owner_id IN (` + sqlPlaceholders(len(ownerIDs)) +
		`) AND items.extra_type = ? GROUP BY items.owner_id`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: counting owned extras of %d items: %w", len(ownerIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("sqlite: scanning owned extra counts: %w", err)
		}
		out[storage.ItemID(id)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating owned extra counts: %w", err)
	}
	return out, nil
}

// RecursiveItemCountsFor answers recursiveItemCountOf for many parents at once:
// how many non-folder descendants each holds, extras excluded.
func (s *Store) RecursiveItemCountsFor(ctx context.Context, parentIDs []storage.ItemID, excludeTypes []storage.ItemType) (map[storage.ItemID]int, error) {
	out := make(map[storage.ItemID]int, len(parentIDs))
	if len(parentIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(parentIDs)+len(excludeTypes))
	for _, id := range parentIDs {
		args = append(args, string(id))
	}
	notIn := ""
	if len(excludeTypes) > 0 {
		for _, t := range excludeTypes {
			args = append(args, string(t))
		}
		notIn = " AND items.type NOT IN (" + sqlPlaceholders(len(excludeTypes)) + ")"
	}
	query := `SELECT ia.ancestor_id, COUNT(*) FROM item_ancestors ia JOIN items ON items.id = ia.item_id
		WHERE ia.ancestor_id IN (` + sqlPlaceholders(len(parentIDs)) + `) AND items.extra_type = ''` + notIn + `
		GROUP BY ia.ancestor_id`
	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: counting descendants of %d items: %w", len(parentIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("sqlite: scanning recursive counts: %w", err)
		}
		out[storage.ItemID(id)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating recursive counts: %w", err)
	}
	return out, nil
}
