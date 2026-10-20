-- The probed container format of a file (ffprobe's format_name, with Matroska named mkv and MPEG-TS named ts) and the
-- timestamp state of a transport stream (None, Zero, Valid); the Container of the item DTO and the Timestamp of its
-- sources come from them. Empty until the file is probed again.
ALTER TABLE items ADD COLUMN format TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN ts_timestamp TEXT NOT NULL DEFAULT '';
