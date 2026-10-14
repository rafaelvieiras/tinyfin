-- Collections (BoxSet): the scanner reads a movie's <set> name into
-- items.set_name and groups by it when the library's
-- AutomaticallyAddToCollection option is on. Membership itself is a row of
-- item_relations (kind 'collection') and must not repeat, so a unique index
-- backs the idempotent add. The ancestor index (item_ancestors) also carries
-- (member, collection) rows; they are rebuilt from the relations whenever a
-- membership changes.
ALTER TABLE items ADD COLUMN set_name TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_items_set_name ON items(library_id, set_name) WHERE set_name <> '';
CREATE UNIQUE INDEX idx_item_relations_collection ON item_relations(parent_id, child_id) WHERE kind = 'collection';
