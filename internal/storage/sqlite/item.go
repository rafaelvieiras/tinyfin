package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const itemColumns = `items.id, items.type, items.library_id, items.parent_id, items.name, items.original_title, items.sort_name, items.overview,
	items.taglines, items.genres, items.tags, items.studios,
	items.production_year, items.premiere_date, items.end_date, items.official_rating, items.community_rating, items.critic_rating, items.run_time_ticks,
	items.index_number, items.parent_index_number,
	items.series_id, items.season_id,
	items.album, items.album_artist, items.artists,
	items.provider_ids,
	items.path, items.container, items.size,
	items.collection_type,
	items.media_streams,
	items.people,
	items.date_created`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scanItem works
// for both GetItem (a single row) and QueryItems (a cursor).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(rs rowScanner) (storage.Item, error) {
	var (
		id, typ, libraryID                      string
		parentID                                sql.NullString
		name, originalTitle, sortName, overview string
		taglinesJSON, genresJSON, tagsJSON      string
		studiosJSON                             string
		productionYear                          sql.NullInt64
		premiereDate, endDate                   sql.NullTime
		officialRating                          string
		communityRating, criticRating           sql.NullFloat64
		runTimeTicks                            sql.NullInt64
		indexNumber, parentIndexNumber          sql.NullInt64
		seriesID, seasonID                      sql.NullString
		album, albumArtist                      string
		artistsJSON                             string
		providerIDsJSON                         string
		path, container                         string
		size                                    int64
		collectionType                          string
		mediaStreamsJSON                        string
		peopleJSON                              string
		dateCreated                             time.Time
	)

	if err := rs.Scan(
		&id, &typ, &libraryID, &parentID, &name, &originalTitle, &sortName, &overview,
		&taglinesJSON, &genresJSON, &tagsJSON, &studiosJSON,
		&productionYear, &premiereDate, &endDate, &officialRating, &communityRating, &criticRating, &runTimeTicks,
		&indexNumber, &parentIndexNumber,
		&seriesID, &seasonID,
		&album, &albumArtist, &artistsJSON,
		&providerIDsJSON,
		&path, &container, &size,
		&collectionType,
		&mediaStreamsJSON,
		&peopleJSON,
		&dateCreated,
	); err != nil {
		return storage.Item{}, err
	}

	item := storage.Item{
		ID:             storage.ItemID(id),
		Type:           storage.ItemType(typ),
		LibraryID:      storage.LibraryID(libraryID),
		Name:           name,
		OriginalTitle:  originalTitle,
		SortName:       sortName,
		Overview:       overview,
		OfficialRating: officialRating,
		Album:          album,
		AlbumArtist:    albumArtist,
		Path:           path,
		Container:      container,
		Size:           size,
		CollectionType: storage.CollectionType(collectionType),
		DateCreated:    dateCreated,
	}

	if parentID.Valid {
		v := storage.ItemID(parentID.String)
		item.ParentID = &v
	}
	if seriesID.Valid {
		v := storage.ItemID(seriesID.String)
		item.SeriesID = &v
	}
	if seasonID.Valid {
		v := storage.ItemID(seasonID.String)
		item.SeasonID = &v
	}
	if productionYear.Valid {
		v := int(productionYear.Int64)
		item.ProductionYear = &v
	}
	if premiereDate.Valid {
		v := premiereDate.Time
		item.PremiereDate = &v
	}
	if endDate.Valid {
		v := endDate.Time
		item.EndDate = &v
	}
	if communityRating.Valid {
		v := communityRating.Float64
		item.CommunityRating = &v
	}
	if criticRating.Valid {
		v := criticRating.Float64
		item.CriticRating = &v
	}
	if runTimeTicks.Valid {
		v := runTimeTicks.Int64
		item.RunTimeTicks = &v
	}
	if indexNumber.Valid {
		v := int(indexNumber.Int64)
		item.IndexNumber = &v
	}
	if parentIndexNumber.Valid {
		v := int(parentIndexNumber.Int64)
		item.ParentIndexNumber = &v
	}

	if err := unmarshalJSONInto(taglinesJSON, &item.Taglines); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(genresJSON, &item.Genres); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(tagsJSON, &item.Tags); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(studiosJSON, &item.Studios); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(artistsJSON, &item.Artists); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(providerIDsJSON, &item.ProviderIDs); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(mediaStreamsJSON, &item.MediaStreams); err != nil {
		return storage.Item{}, err
	}
	if err := unmarshalJSONInto(peopleJSON, &item.People); err != nil {
		return storage.Item{}, err
	}

	return item, nil
}

func (s *Store) GetItem(ctx context.Context, id storage.ItemID) (storage.Item, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE items.id = ?`, string(id))
	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.Item{}, fmt.Errorf("sqlite: item %q not found: %w", id, err)
		}
		return storage.Item{}, fmt.Errorf("sqlite: getting item %q: %w", id, err)
	}
	return item, nil
}

func ptrToAny[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func idPtrToAny(p *storage.ItemID) any {
	if p == nil {
		return nil
	}
	return string(*p)
}

func itemSearchText(item storage.Item) string {
	parts := []string{
		item.Name, item.OriginalTitle, item.SortName, item.Overview,
		item.Album, item.AlbumArtist,
		strings.Join(item.Tags, " "),
		strings.Join(item.Genres, " "),
		strings.Join(item.Artists, " "),
		strings.Join(item.Taglines, " "),
	}
	return strings.Join(parts, " ")
}

// UpsertItem inserts item, or updates it in place if item.ID already exists.
//
// date_created is deliberately absent from the ON CONFLICT DO UPDATE SET
// clause below: every scan pass (not just first discovery) upserts every
// item it walks, so treating date_created like every other column would
// reset "date added" to the scan's wall-clock time on every single rescan,
// scrambling the reference server's /Users/{userId}/Items/Latest ordering
// into scan/traversal order instead of true historical add order (found live
// on a long-lived test deployment). Once a row exists, its stored date_created always
// wins; only a brand-new row takes the value passed in here (falling back to
// time.Now() below if the caller left it zero).
func (s *Store) UpsertItem(ctx context.Context, item storage.Item) error {
	if item.ID == "" {
		item.ID = storage.ItemID(newID())
	}
	if item.DateCreated.IsZero() {
		item.DateCreated = time.Now().UTC()
	}

	taglinesJSON, err := marshalJSON(item.Taglines)
	if err != nil {
		return err
	}
	genresJSON, err := marshalJSON(item.Genres)
	if err != nil {
		return err
	}
	tagsJSON, err := marshalJSON(item.Tags)
	if err != nil {
		return err
	}
	studiosJSON, err := marshalJSON(item.Studios)
	if err != nil {
		return err
	}
	artistsJSON, err := marshalJSON(item.Artists)
	if err != nil {
		return err
	}
	providerIDsJSON, err := marshalJSON(item.ProviderIDs)
	if err != nil {
		return err
	}
	mediaStreamsJSON, err := marshalJSON(item.MediaStreams)
	if err != nil {
		return err
	}
	peopleJSON, err := marshalJSON(item.People)
	if err != nil {
		return err
	}

	const query = `INSERT INTO items (
		id, type, library_id, parent_id, name, original_title, sort_name, overview,
		taglines, genres, tags, studios,
		production_year, premiere_date, end_date, official_rating, community_rating, critic_rating, run_time_ticks,
		index_number, parent_index_number,
		series_id, season_id,
		album, album_artist, artists,
		provider_ids,
		path, container, size,
		collection_type,
		media_streams,
		people,
		search_text,
		date_created
	) VALUES (?,?,?,?,?,?,?,?, ?,?,?,?, ?,?,?,?,?,?,?, ?,?, ?,?, ?,?,?, ?, ?,?,?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		type=excluded.type, library_id=excluded.library_id, parent_id=excluded.parent_id, name=excluded.name,
		original_title=excluded.original_title, sort_name=excluded.sort_name, overview=excluded.overview,
		taglines=excluded.taglines, genres=excluded.genres, tags=excluded.tags, studios=excluded.studios,
		production_year=excluded.production_year, premiere_date=excluded.premiere_date, end_date=excluded.end_date,
		official_rating=excluded.official_rating, community_rating=excluded.community_rating, critic_rating=excluded.critic_rating,
		run_time_ticks=excluded.run_time_ticks, index_number=excluded.index_number, parent_index_number=excluded.parent_index_number,
		series_id=excluded.series_id, season_id=excluded.season_id,
		album=excluded.album, album_artist=excluded.album_artist, artists=excluded.artists,
		provider_ids=excluded.provider_ids,
		path=excluded.path, container=excluded.container, size=excluded.size,
		collection_type=excluded.collection_type,
		media_streams=excluded.media_streams,
		people=excluded.people,
		search_text=excluded.search_text`

	err = s.withTx(ctx, func(ex dbtx) error {
		if _, err := ex.ExecContext(ctx, query,
			string(item.ID), string(item.Type), string(item.LibraryID), idPtrToAny(item.ParentID), item.Name, item.OriginalTitle, item.SortName, item.Overview,
			taglinesJSON, genresJSON, tagsJSON, studiosJSON,
			ptrToAny(item.ProductionYear), ptrToAny(item.PremiereDate), ptrToAny(item.EndDate), item.OfficialRating, ptrToAny(item.CommunityRating), ptrToAny(item.CriticRating), ptrToAny(item.RunTimeTicks),
			ptrToAny(item.IndexNumber), ptrToAny(item.ParentIndexNumber),
			idPtrToAny(item.SeriesID), idPtrToAny(item.SeasonID),
			item.Album, item.AlbumArtist, artistsJSON,
			providerIDsJSON,
			item.Path, item.Container, item.Size,
			string(item.CollectionType),
			mediaStreamsJSON,
			peopleJSON,
			itemSearchText(item),
			item.DateCreated,
		); err != nil {
			return fmt.Errorf("sqlite: upserting item %q: %w", item.ID, err)
		}

		if err := recomputeAncestors(ctx, ex, item.ID); err != nil {
			return fmt.Errorf("sqlite: recomputing ancestors for item %q: %w", item.ID, err)
		}
		return nil
	})
	return err
}

func (s *Store) DeleteItem(ctx context.Context, id storage.ItemID) error {
	// Descendants, item_ancestors rows, playstates, image/subtitle/lyric
	// refs, and item_relations all reference items.id with ON DELETE
	// CASCADE, so a single delete here cleans up the whole subtree.
	if _, err := s.exec(ctx, `DELETE FROM items WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting item %q: %w", id, err)
	}
	return nil
}

// ancestorChain walks Item.ParentID upward from id (exclusive) and returns
// every ancestor id encountered, nearest first. A seen-set guards against a
// corrupt cycle turning this into an infinite loop.
func ancestorChain(ctx context.Context, ex dbtx, id storage.ItemID) ([]storage.ItemID, error) {
	var chain []storage.ItemID
	seen := map[storage.ItemID]bool{id: true}
	current := id

	for {
		var parent sql.NullString
		err := ex.QueryRowContext(ctx, `SELECT parent_id FROM items WHERE id = ?`, string(current)).Scan(&parent)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			return nil, err
		}
		if !parent.Valid || parent.String == "" {
			break
		}
		p := storage.ItemID(parent.String)
		if seen[p] {
			break
		}
		seen[p] = true
		chain = append(chain, p)
		current = p
	}
	return chain, nil
}

// recomputeAncestors rebuilds id's row(s) in item_ancestors from the
// current items.parent_id chain, then recurses into every existing direct
// child so a parent move/rename propagates through the whole subtree — the
// materialized-ancestor-closure write-time maintenance docs/05 calls for.
func recomputeAncestors(ctx context.Context, ex dbtx, id storage.ItemID) error {
	chain, err := ancestorChain(ctx, ex, id)
	if err != nil {
		return err
	}

	if _, err := ex.ExecContext(ctx, `DELETE FROM item_ancestors WHERE item_id = ?`, string(id)); err != nil {
		return err
	}
	for _, a := range chain {
		if _, err := ex.ExecContext(ctx, `INSERT INTO item_ancestors (item_id, ancestor_id) VALUES (?, ?)`, string(id), string(a)); err != nil {
			return err
		}
	}

	rows, err := ex.QueryContext(ctx, `SELECT id FROM items WHERE parent_id = ?`, string(id))
	if err != nil {
		return err
	}
	var children []storage.ItemID
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		children = append(children, storage.ItemID(cid))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, c := range children {
		if err := recomputeAncestors(ctx, ex, c); err != nil {
			return err
		}
	}
	return nil
}

var sortColumns = map[storage.SortBy]string{
	storage.SortByName:              "items.sort_name",
	storage.SortByDateCreated:       "items.date_created",
	storage.SortByPremiereDate:      "items.premiere_date",
	storage.SortByProductionYear:    "items.production_year",
	storage.SortByCommunityRating:   "items.community_rating",
	storage.SortByIndexNumber:       "items.index_number",
	storage.SortByParentIndexNumber: "items.parent_index_number",
	// series_sort.sort_name is only non-NULL when the series self-join
	// below was added (an item whose series_id doesn't resolve, or a
	// Movie/Series with no series_id at all, leaves it NULL) — COALESCE
	// falls back to the item's own sort_name so a type with no series
	// context still sorts sensibly, matching the reference server's
	// behavior for SortBy=SeriesSortName on non-episode types.
	storage.SortBySeriesName: "COALESCE(series_sort.sort_name, items.sort_name)",
}

// needsSeriesSortJoin reports whether fields asks to sort by
// storage.SortBySeriesName, the one sort key that isn't a plain items
// column (buildSeriesSortJoin's doc comment).
func needsSeriesSortJoin(fields []storage.SortField) bool {
	for _, sf := range fields {
		if sf.By == storage.SortBySeriesName {
			return true
		}
	}
	return false
}

func buildOrderBy(fields []storage.SortField) string {
	parts := make([]string, 0, len(fields))
	for _, sf := range fields {
		if sf.By == storage.SortByRandom {
			parts = append(parts, "RANDOM()")
			continue
		}
		col, ok := sortColumns[sf.By]
		if !ok {
			continue
		}
		dir := "ASC"
		if sf.Descending {
			dir = "DESC"
		}
		parts = append(parts, col+" "+dir)
	}
	if len(parts) == 0 {
		return "ORDER BY items.sort_name ASC"
	}
	return "ORDER BY " + strings.Join(parts, ", ")
}

// ftsMatchQuery turns free-text search input into an FTS5 MATCH expression:
// each whitespace-separated token becomes a quoted prefix term, so "the mat"
// matches names/overviews containing a word starting with "mat" and a word
// starting with "the" (implicit AND between terms in FTS5).
func ftsMatchQuery(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return `""`
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"*`
	}
	return strings.Join(parts, " ")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) QueryItems(ctx context.Context, filter storage.ItemFilter) (storage.ItemPage, error) {
	if filter.Limit <= 0 {
		return storage.ItemPage{}, fmt.Errorf("sqlite: QueryItems: Limit must be > 0, got %d", filter.Limit)
	}

	var (
		joins, wheres       []string
		joinArgs, whereArgs []any
	)

	if filter.LibraryID != nil {
		wheres = append(wheres, "items.library_id = ?")
		whereArgs = append(whereArgs, string(*filter.LibraryID))
	}

	if filter.ParentID != nil {
		if filter.Recursive {
			joins = append(joins, "JOIN item_ancestors ia ON ia.item_id = items.id")
			wheres = append(wheres, "ia.ancestor_id = ?")
			whereArgs = append(whereArgs, string(*filter.ParentID))
		} else {
			wheres = append(wheres, "items.parent_id = ?")
			whereArgs = append(whereArgs, string(*filter.ParentID))
		}
	}

	if filter.SeriesID != nil {
		wheres = append(wheres, "items.series_id = ?")
		whereArgs = append(whereArgs, string(*filter.SeriesID))
	}

	if filter.Path != "" {
		wheres = append(wheres, "items.path = ?")
		whereArgs = append(whereArgs, filter.Path)
	}

	if len(filter.ItemTypes) > 0 {
		ph := make([]string, len(filter.ItemTypes))
		for i, t := range filter.ItemTypes {
			ph[i] = "?"
			whereArgs = append(whereArgs, string(t))
		}
		wheres = append(wheres, "items.type IN ("+strings.Join(ph, ",")+")")
	}

	if len(filter.ExcludeItemTypes) > 0 {
		ph := make([]string, len(filter.ExcludeItemTypes))
		for i, t := range filter.ExcludeItemTypes {
			ph[i] = "?"
			whereArgs = append(whereArgs, string(t))
		}
		wheres = append(wheres, "items.type NOT IN ("+strings.Join(ph, ",")+")")
	}

	if filter.LibraryIDs != nil {
		if len(filter.LibraryIDs) == 0 {
			wheres = append(wheres, "0")
		} else {
			ph := make([]string, len(filter.LibraryIDs))
			for i, l := range filter.LibraryIDs {
				ph[i] = "?"
				whereArgs = append(whereArgs, string(l))
			}
			wheres = append(wheres, "items.library_id IN ("+strings.Join(ph, ",")+")")
		}
	}

	for _, g := range filter.Genres {
		wheres = append(wheres, "EXISTS (SELECT 1 FROM json_each(items.genres) WHERE value = ?)")
		whereArgs = append(whereArgs, g)
	}

	if filter.SearchText != "" {
		joins = append(joins, "JOIN items_fts ON items_fts.rowid = items.rowid")
		wheres = append(wheres, "items_fts MATCH ?")
		whereArgs = append(whereArgs, ftsMatchQuery(filter.SearchText))
	}

	if needsSeriesSortJoin(filter.SortBy) {
		// Self-join to the owning series' row so ORDER BY can read its
		// sort_name (sortColumns' storage.SortBySeriesName entry above) —
		// items.series_id is populated on both Episode and Season rows
		// (scan.go), so this covers both without a type-specific case.
		joins = append(joins, "LEFT JOIN items series_sort ON series_sort.id = items.series_id")
	}

	if filter.UserID != nil {
		joins = append(joins, "LEFT JOIN playstates ps ON ps.item_id = items.id AND ps.user_id = ?")
		joinArgs = append(joinArgs, string(*filter.UserID))
		if filter.Played != nil {
			wheres = append(wheres, "COALESCE(ps.played, 0) = ?")
			whereArgs = append(whereArgs, boolToInt(*filter.Played))
		}
		if filter.Favorite != nil {
			wheres = append(wheres, "COALESCE(ps.is_favorite, 0) = ?")
			whereArgs = append(whereArgs, boolToInt(*filter.Favorite))
		}
		if filter.Resume {
			wheres = append(wheres, "ps.playback_position_ticks > 0 AND COALESCE(ps.played, 0) = 0")
		}
	}

	joinSQL := strings.Join(joins, " ")
	whereSQL := ""
	if len(wheres) > 0 {
		whereSQL = "WHERE " + strings.Join(wheres, " AND ")
	}

	args := make([]any, 0, len(joinArgs)+len(whereArgs))
	args = append(args, joinArgs...)
	args = append(args, whereArgs...)

	var total int
	countQuery := "SELECT COUNT(*) FROM items " + joinSQL + " " + whereSQL
	if err := s.ex.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return storage.ItemPage{}, fmt.Errorf("sqlite: counting items: %w", err)
	}

	orderSQL := buildOrderBy(filter.SortBy)
	if filter.Resume && filter.UserID != nil && len(filter.SortBy) == 0 {
		orderSQL = "ORDER BY ps.last_played_date DESC, items.sort_name ASC"
	}
	dataQuery := "SELECT " + itemColumns + " FROM items " + joinSQL + " " + whereSQL + " " + orderSQL + " LIMIT ? OFFSET ?"
	dataArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)

	rows, err := s.ex.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return storage.ItemPage{}, fmt.Errorf("sqlite: querying items: %w", err)
	}
	defer rows.Close()

	var items []storage.Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return storage.ItemPage{}, fmt.Errorf("sqlite: scanning item row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return storage.ItemPage{}, fmt.Errorf("sqlite: iterating item rows: %w", err)
	}

	return storage.ItemPage{Items: items, TotalCount: total, Offset: filter.Offset}, nil
}

// sqliteAggregateTimeLayout is the format modernc.org/sqlite writes a bound
// time.Time parameter as (conn.go's formatTime, which defaults to
// t.String() — this driver isn't configured with an explicit
// _time_format DSN param, store.go's DSN). A plain column selection
// (itemColumns/scanItem) never needs this: the driver auto-converts a
// column with a TIMESTAMP decltype straight to a time.Time value. An
// aggregate like MAX(date_created) has no decltype, though, so the driver
// hands the raw stored text back unconverted instead — parseAggregateTime
// is that missing conversion step, done explicitly.
const sqliteAggregateTimeLayout = "2006-01-02 15:04:05.999999999 -0700 MST"

// parseAggregateTime parses raw (sqliteAggregateTimeLayout's format) back
// into a time.Time. Every DateCreated this project writes goes through
// time.Now().UTC() (UpsertItem's own zero-value default, item.go, and
// every explicit caller — internal/scan/scan.go, cmd/devseed), so in
// practice raw is always the "+0000 UTC" shape and carries no monotonic
// reading. The "m=" strip below is defense in depth, not load-bearing
// today: .UTC() already strips a time.Time's monotonic clock reading
// before formatTime ever sees it (a bare, non-UTC time.Now() does carry
// one, which t.String() appends as a trailing " m=+<seconds>" this
// layout doesn't otherwise account for) — matching the driver's own read
// path for a plain time.Time column (parseTimeString, conn.go), which
// strips the identical suffix before parsing.
func parseAggregateTime(raw string) (time.Time, error) {
	if i := strings.Index(raw, "m="); i > 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	return time.Parse(sqliteAggregateTimeLayout, raw)
}

// QuerySeriesLatestActivity implements storage.ItemStore's method of the
// same name: a GROUP BY series_id aggregate ranking distinct series by
// their own MAX(date_created), rather than QueryItems' per-item ranking —
// see storage.SeriesActivity's doc comment for why the two aren't
// interchangeable for GET /Items/Latest's grouping.
func (s *Store) QuerySeriesLatestActivity(ctx context.Context, libraryID storage.LibraryID, itemTypes []storage.ItemType, limit int) ([]storage.SeriesActivity, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("sqlite: QuerySeriesLatestActivity: limit must be > 0, got %d", limit)
	}

	wheres := []string{"items.library_id = ?", "items.series_id IS NOT NULL"}
	args := []any{string(libraryID)}

	if len(itemTypes) > 0 {
		ph := make([]string, len(itemTypes))
		for i, t := range itemTypes {
			ph[i] = "?"
			args = append(args, string(t))
		}
		wheres = append(wheres, "items.type IN ("+strings.Join(ph, ",")+")")
	}

	query := "SELECT items.series_id, MAX(items.date_created) AS latest FROM items WHERE " +
		strings.Join(wheres, " AND ") +
		" GROUP BY items.series_id ORDER BY latest DESC, items.series_id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying series latest activity: %w", err)
	}
	defer rows.Close()

	var out []storage.SeriesActivity
	for rows.Next() {
		var seriesID, latestRaw string
		if err := rows.Scan(&seriesID, &latestRaw); err != nil {
			return nil, fmt.Errorf("sqlite: scanning series activity row: %w", err)
		}
		latest, err := parseAggregateTime(latestRaw)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parsing series activity timestamp %q: %w", latestRaw, err)
		}
		out = append(out, storage.SeriesActivity{SeriesID: storage.ItemID(seriesID), Latest: latest})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating series activity rows: %w", err)
	}
	return out, nil
}

func (s *Store) QueryRelations(ctx context.Context, itemID storage.ItemID, kind string, asParent bool) ([]storage.ItemRelation, error) {
	col := "parent_id"
	if !asParent {
		col = "child_id"
	}

	query := `SELECT id, parent_id, child_id, kind, sort_index FROM item_relations WHERE ` + col + ` = ?`
	args := []any{string(itemID)}
	if kind != "" {
		query += " AND kind = ?"
		args = append(args, kind)
	}
	query += " ORDER BY sort_index ASC"

	rows, err := s.ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying relations for item %q: %w", itemID, err)
	}
	defer rows.Close()

	var rels []storage.ItemRelation
	for rows.Next() {
		var rel storage.ItemRelation
		var id, parentID, childID, k string
		if err := rows.Scan(&id, &parentID, &childID, &k, &rel.SortIndex); err != nil {
			return nil, fmt.Errorf("sqlite: scanning relation row: %w", err)
		}
		rel.ID = storage.ItemRelationID(id)
		rel.ParentID = storage.ItemID(parentID)
		rel.ChildID = storage.ItemID(childID)
		rel.Kind = k
		rels = append(rels, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating relation rows: %w", err)
	}
	return rels, nil
}

func (s *Store) UpsertRelation(ctx context.Context, rel storage.ItemRelation) error {
	if rel.ID == "" {
		rel.ID = storage.ItemRelationID(newID())
	}

	const query = `INSERT INTO item_relations (id, parent_id, child_id, kind, sort_index) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, child_id=excluded.child_id, kind=excluded.kind, sort_index=excluded.sort_index`

	if _, err := s.exec(ctx, query, string(rel.ID), string(rel.ParentID), string(rel.ChildID), rel.Kind, rel.SortIndex); err != nil {
		return fmt.Errorf("sqlite: upserting relation %q: %w", rel.ID, err)
	}
	return nil
}

func (s *Store) DeleteRelation(ctx context.Context, id storage.ItemRelationID) error {
	if _, err := s.exec(ctx, `DELETE FROM item_relations WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting relation %q: %w", id, err)
	}
	return nil
}
