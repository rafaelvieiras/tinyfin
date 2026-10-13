-- Playback entries (start/stop) of the activity log name the item they are
-- about; login entries leave it NULL.
ALTER TABLE activity_log ADD COLUMN item_id TEXT;
