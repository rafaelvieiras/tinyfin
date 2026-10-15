-- Playlists: a playlist is an item of type Playlist in the hidden playlists
-- library. Its entries are rows of item_relations (kind 'playlist', ordered
-- by sort_index, an item may repeat); who may see and edit it lives here.
--
--   playlist_access: the owner, whether every user may read it (open_access)
--     and the media type of its entries ('' = decided from the entries);
--   playlist_shares: the users it is shared with and whether each may edit.
CREATE TABLE playlist_access (
    playlist_id TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    owner_id    TEXT NOT NULL,
    open_access INTEGER NOT NULL DEFAULT 0,
    media_type  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_playlist_access_owner ON playlist_access(owner_id);

CREATE TABLE playlist_shares (
    playlist_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL,
    can_edit    INTEGER NOT NULL DEFAULT 0,
    position    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (playlist_id, user_id)
);
CREATE INDEX idx_playlist_shares_user ON playlist_shares(user_id);
