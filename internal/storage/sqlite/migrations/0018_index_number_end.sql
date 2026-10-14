-- The last episode number of a multi-episode file (S01E05E06, an .nfo with
-- several <episodedetails>): BaseItemDto.IndexNumberEnd.
ALTER TABLE items ADD COLUMN index_number_end INTEGER;
