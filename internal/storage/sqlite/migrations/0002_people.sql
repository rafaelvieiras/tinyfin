-- Adds storage.Item.People (cast/crew credits) — same JSON-column pattern
-- as taglines/genres/tags in 0001_init.sql (marshalled/unmarshalled by
-- item.go's marshalJSON/unmarshalJSONInto, same as those columns).
ALTER TABLE items ADD COLUMN people TEXT NOT NULL DEFAULT '[]';
