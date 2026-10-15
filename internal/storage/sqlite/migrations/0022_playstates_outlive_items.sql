-- A user's play state outlives its item: when a scan forgets an item whose file
-- or folder vanished (an unmounted disk, a renamed folder) and the item comes
-- back at the same path it gets its watched state, resume position and
-- favorite flag back. The reference keeps user data the same way (keyed by
-- the item's id, independent of the item row). The row therefore must not
-- cascade away with the item: the table is rebuilt without the foreign key
-- on item_id.
CREATE TABLE playstates_new (
    user_id                 TEXT NOT NULL,
    item_id                 TEXT NOT NULL,
    played                  INTEGER NOT NULL DEFAULT 0,
    playback_position_ticks INTEGER NOT NULL DEFAULT 0,
    play_count              INTEGER NOT NULL DEFAULT 0,
    is_favorite             INTEGER NOT NULL DEFAULT 0,
    last_played_date        TIMESTAMP,
    rating                  REAL,
    likes                   INTEGER,
    touched_at              INTEGER,
    folder_marked_at        INTEGER,
    PRIMARY KEY (user_id, item_id)
);
INSERT INTO playstates_new (user_id, item_id, played, playback_position_ticks, play_count, is_favorite,
                            last_played_date, rating, likes, touched_at, folder_marked_at)
    SELECT user_id, item_id, played, playback_position_ticks, play_count, is_favorite,
           last_played_date, rating, likes, touched_at, folder_marked_at FROM playstates;
DROP TABLE playstates;
ALTER TABLE playstates_new RENAME TO playstates;
CREATE INDEX idx_playstates_item ON playstates(item_id);
