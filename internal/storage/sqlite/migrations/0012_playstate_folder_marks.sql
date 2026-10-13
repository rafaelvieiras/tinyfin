-- Folder marks (POST/DELETE /UserPlayedItems/{id} on a series or season) are a
-- state of the folder itself and never write to the leaves below it. The
-- derived played view of a leaf therefore needs to know which happened last:
-- the leaf's own change or an ancestor's mark.
--
--   touched_at        UnixNano of the last change to the row's own played
--                     flag, play count, position or last-played date.
--   folder_marked_at  UnixNano of the last played/unplayed mark applied to
--                     this row as a folder (NULL for rows never marked).
ALTER TABLE playstates ADD COLUMN touched_at INTEGER;
ALTER TABLE playstates ADD COLUMN folder_marked_at INTEGER;
