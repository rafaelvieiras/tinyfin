-- Named items: genres, studios, people and years are not rows of items; they
-- exist while an item carries the name. Their identifier is derived from the
-- kind and the name (storage.NamedID), so resolving an identifier back to a
-- name needs this index. It is filled when items are written and, for a
-- database that predates it, rebuilt on the first lookup that misses.
CREATE TABLE named_items (
    id   TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL
);
CREATE INDEX idx_named_items_kind ON named_items(kind, name);
