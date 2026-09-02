-- Adds storage.Item.Studios (network/studio names) — same JSON-column
-- pattern as people in 0002_people.sql (marshalled/unmarshalled by
-- item.go's marshalJSON/unmarshalJSONInto, same as that column).
ALTER TABLE items ADD COLUMN studios TEXT NOT NULL DEFAULT '[]';
