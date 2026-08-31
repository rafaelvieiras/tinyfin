-- Initial schema for the embedded SQLite storage driver (docs/05).
--
-- Design notes:
--   * Every item read goes through an indexed query (AGENTS.md principle 1) —
--     see the indexes below, one per ItemFilter field that can drive a scan.
--   * item_ancestors is the materialized ancestor-closure table docs/05 calls
--     for: one row per (descendant, ancestor) pair, kept in sync at write
--     time by the Go layer, so ItemFilter.Recursive is a single indexed join
--     instead of a recursive walk at read time.
--   * ON DELETE CASCADE on items.parent_id means deleting a folder-like item
--     (a series, a season) removes its descendants too, which also keeps
--     item_ancestors, playstates, image_refs, etc. free of orphaned rows
--     without extra application code.
--   * Image/subtitle/lyric/trickplay bytes are never stored here — these
--     tables hold refs and metadata only (docs/05).

CREATE TABLE libraries (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    collection_type TEXT NOT NULL DEFAULT '',
    paths           TEXT NOT NULL DEFAULT '[]',
    last_scanned_at TIMESTAMP
);

CREATE TABLE items (
    id                  TEXT PRIMARY KEY,
    type                TEXT NOT NULL,
    library_id          TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    parent_id           TEXT REFERENCES items(id) ON DELETE CASCADE,

    name                TEXT NOT NULL DEFAULT '',
    original_title      TEXT NOT NULL DEFAULT '',
    sort_name           TEXT NOT NULL DEFAULT '',
    overview            TEXT NOT NULL DEFAULT '',
    taglines            TEXT NOT NULL DEFAULT '[]',
    genres              TEXT NOT NULL DEFAULT '[]',
    tags                TEXT NOT NULL DEFAULT '[]',

    production_year     INTEGER,
    premiere_date       TIMESTAMP,
    end_date            TIMESTAMP,
    official_rating     TEXT NOT NULL DEFAULT '',
    community_rating    REAL,
    critic_rating       REAL,
    run_time_ticks      INTEGER,

    index_number        INTEGER,
    parent_index_number INTEGER,

    series_id           TEXT,
    season_id           TEXT,

    album               TEXT NOT NULL DEFAULT '',
    album_artist        TEXT NOT NULL DEFAULT '',
    artists             TEXT NOT NULL DEFAULT '[]',

    provider_ids        TEXT NOT NULL DEFAULT '{}',

    path                TEXT NOT NULL DEFAULT '',
    container           TEXT NOT NULL DEFAULT '',
    size                INTEGER NOT NULL DEFAULT 0,

    collection_type     TEXT NOT NULL DEFAULT '',

    media_streams       TEXT NOT NULL DEFAULT '[]',

    -- Denormalized, app-populated search blob (name + overview + tags +
    -- genres + music fields) indexed by items_fts below. Built in Go at
    -- write time rather than via a JSON1 trigger expression, to keep the
    -- FTS sync trigger itself trivial.
    search_text         TEXT NOT NULL DEFAULT '',

    date_created        TIMESTAMP NOT NULL
);

CREATE INDEX idx_items_library_id      ON items(library_id);
CREATE INDEX idx_items_parent_id       ON items(parent_id);
CREATE INDEX idx_items_type            ON items(type);
CREATE INDEX idx_items_library_type    ON items(library_id, type);
CREATE INDEX idx_items_series_id       ON items(series_id);
CREATE INDEX idx_items_season_id       ON items(season_id);
CREATE INDEX idx_items_sort_name       ON items(sort_name);
CREATE INDEX idx_items_date_created    ON items(date_created);
CREATE INDEX idx_items_premiere_date   ON items(premiere_date);
CREATE INDEX idx_items_production_year ON items(production_year);
CREATE INDEX idx_items_community_rating ON items(community_rating);
CREATE INDEX idx_items_parent_order    ON items(parent_id, parent_index_number, index_number);

CREATE TABLE item_ancestors (
    item_id     TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    ancestor_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, ancestor_id)
);
CREATE INDEX idx_item_ancestors_ancestor ON item_ancestors(ancestor_id);

CREATE TABLE item_relations (
    id         TEXT PRIMARY KEY,
    parent_id  TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    child_id   TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    sort_index INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_item_relations_parent ON item_relations(parent_id, kind, sort_index);
CREATE INDEX idx_item_relations_child  ON item_relations(child_id, kind);

CREATE TABLE users (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    password_hash      TEXT NOT NULL DEFAULT '',
    is_administrator   INTEGER NOT NULL DEFAULT 0,
    is_disabled        INTEGER NOT NULL DEFAULT 0,
    enable_auto_login  INTEGER NOT NULL DEFAULT 0,
    last_login_date    TIMESTAMP,
    last_activity_date TIMESTAMP
);

CREATE TABLE auth_tokens (
    token     TEXT PRIMARY KEY,
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL DEFAULT '',
    issued_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_auth_tokens_user ON auth_tokens(user_id);

CREATE TABLE display_preferences (
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client               TEXT NOT NULL,

    view_type            TEXT NOT NULL DEFAULT '',
    index_by             TEXT NOT NULL DEFAULT '',
    sort_by              TEXT NOT NULL DEFAULT '',
    sort_order           TEXT NOT NULL DEFAULT '',
    remember_indexing    INTEGER NOT NULL DEFAULT 0,
    remember_sorting     INTEGER NOT NULL DEFAULT 0,
    scroll_direction     TEXT NOT NULL DEFAULT '',
    show_backdrop        INTEGER NOT NULL DEFAULT 0,
    show_sidebar         INTEGER NOT NULL DEFAULT 0,
    primary_image_width  INTEGER NOT NULL DEFAULT 0,
    primary_image_height INTEGER NOT NULL DEFAULT 0,
    custom_prefs         TEXT NOT NULL DEFAULT '{}',

    PRIMARY KEY (user_id, client)
);

CREATE TABLE devices (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL DEFAULT '',
    custom_name        TEXT NOT NULL DEFAULT '',
    app_name           TEXT NOT NULL DEFAULT '',
    app_version        TEXT NOT NULL DEFAULT '',
    last_user_id       TEXT NOT NULL DEFAULT '',
    last_user_name     TEXT NOT NULL DEFAULT '',
    date_last_activity TIMESTAMP
);
CREATE INDEX idx_devices_last_user ON devices(last_user_id);

CREATE TABLE playstates (
    user_id                 TEXT NOT NULL,
    item_id                 TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    played                  INTEGER NOT NULL DEFAULT 0,
    playback_position_ticks INTEGER NOT NULL DEFAULT 0,
    play_count              INTEGER NOT NULL DEFAULT 0,
    is_favorite             INTEGER NOT NULL DEFAULT 0,
    last_played_date        TIMESTAMP,
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX idx_playstates_item ON playstates(item_id);

CREATE TABLE image_refs (
    id        TEXT PRIMARY KEY,
    item_id   TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind      TEXT NOT NULL,
    idx       INTEGER NOT NULL DEFAULT 0,
    path      TEXT NOT NULL DEFAULT '',
    tag       TEXT NOT NULL DEFAULT '',
    blur_hash TEXT NOT NULL DEFAULT '',
    width     INTEGER NOT NULL DEFAULT 0,
    height    INTEGER NOT NULL DEFAULT 0,
    size      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_image_refs_item_kind ON image_refs(item_id, kind);

CREATE TABLE trickplay_info (
    item_id         TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    width           INTEGER NOT NULL,
    height          INTEGER NOT NULL DEFAULT 0,
    tile_width      INTEGER NOT NULL DEFAULT 0,
    tile_height     INTEGER NOT NULL DEFAULT 0,
    thumbnail_count INTEGER NOT NULL DEFAULT 0,
    interval_ms     INTEGER NOT NULL DEFAULT 0,
    bandwidth       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id, width)
);

CREATE TABLE subtitle_refs (
    id                  TEXT PRIMARY KEY,
    item_id             TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    path                TEXT NOT NULL DEFAULT '',
    language            TEXT NOT NULL DEFAULT '',
    format              TEXT NOT NULL DEFAULT '',
    is_forced           INTEGER NOT NULL DEFAULT 0,
    is_hearing_impaired INTEGER NOT NULL DEFAULT 0,
    source              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_subtitle_refs_item ON subtitle_refs(item_id);

CREATE TABLE lyric_refs (
    id        TEXT PRIMARY KEY,
    item_id   TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    path      TEXT NOT NULL DEFAULT '',
    format    TEXT NOT NULL DEFAULT '',
    author    TEXT NOT NULL DEFAULT '',
    is_synced INTEGER NOT NULL DEFAULT 0,
    source    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_lyric_refs_item ON lyric_refs(item_id);

-- FTS5 external-content table backing ItemFilter.SearchText (docs/05, 09).
-- "External content" means items.name/search_text are the only copy of the
-- text; the FTS index stores only the inverted index, not a duplicate of
-- the row data, kept in sync by the triggers below.
CREATE VIRTUAL TABLE items_fts USING fts5(
    name,
    search_text,
    content='items',
    content_rowid='rowid'
);

CREATE TRIGGER items_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, name, search_text) VALUES (new.rowid, new.name, new.search_text);
END;

CREATE TRIGGER items_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, name, search_text) VALUES ('delete', old.rowid, old.name, old.search_text);
END;

CREATE TRIGGER items_au AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, name, search_text) VALUES ('delete', old.rowid, old.name, old.search_text);
    INSERT INTO items_fts(rowid, name, search_text) VALUES (new.rowid, new.name, new.search_text);
END;
