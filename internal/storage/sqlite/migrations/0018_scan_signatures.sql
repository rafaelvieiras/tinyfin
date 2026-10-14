-- Incremental library scan: the size and mtime (ns) of an item's file as of
-- its last successful ffprobe. A scan whose file still matches reuses the
-- item's stored media streams and runtime instead of probing again.
CREATE TABLE scan_signatures (
	item_id  TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
	size     INTEGER NOT NULL,
	mtime_ns INTEGER NOT NULL
);
