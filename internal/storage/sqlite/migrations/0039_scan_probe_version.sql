-- The probe version an item's scan signature was recorded with. A scan whose
-- probe version is newer than the recorded one probes the file again, so items
-- scanned before a probe learned a new field pick it up without a forced scan.
-- Existing signatures are version 0 and therefore stale.
ALTER TABLE scan_signatures ADD COLUMN probe_version INTEGER NOT NULL DEFAULT 0;

-- The container bit rate of the address a .strm item points at, from the probe
-- made when it is first played (MediaSourceInfo.Bitrate has no file size to be
-- computed from).
ALTER TABLE items ADD COLUMN bit_rate INTEGER;
