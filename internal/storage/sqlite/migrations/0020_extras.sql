-- Extras: trailers, special features, theme songs and videos, and the
-- additional parts of a stacked movie are items that belong to another item
-- (owner_id) and carry an ExtraType. They have no parent, so they never take
-- part in a folder listing or a count; queries leave extra_type <> '' rows out
-- unless asked for them.
ALTER TABLE items ADD COLUMN extra_type TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN owner_id TEXT REFERENCES items(id) ON DELETE CASCADE;
CREATE INDEX idx_items_owner ON items(owner_id) WHERE owner_id IS NOT NULL;
