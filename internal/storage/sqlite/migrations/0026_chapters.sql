-- Chapter marks of a media file (ffprobe -show_chapters), a JSON array of
-- {StartTicks, Name}. Existing items are probed again on the next scan (their
-- scan signatures are dropped) so they pick the chapters up.
ALTER TABLE items ADD COLUMN chapters TEXT NOT NULL DEFAULT '[]';
DELETE FROM scan_signatures;
