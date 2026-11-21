-- The key frame positions of a media file, so a copied picture's HLS playlist
-- can be laid out without reading the file again.
--
-- A copied picture is cut at the file's own key frames (playback.hls.R3), so
-- the media playlist needs to know where they are. Reading them from the
-- container's index (Matroska Cues) costs a few milliseconds; the packet sweep
-- it replaced read every packet header — 156 143 of them for a 3.4 GB film,
-- 28 s cold. The answer is kept here because it belongs to the file and not to
-- the process: the cache that used to hold it lived under the transcode work
-- directory, which is /tmp inside the container, so every recreate threw it
-- away and the first playback of a large file paid the scan again.
--
-- size and mtime are the guard: a row is used only when they still match the
-- file on disk, so a re-encode, a remux or a restored backup invalidates it
-- without anything having to look. ticks is the list of key frame times as
-- 100 ns ticks, ascending, stored as a BLOB of varint deltas against the
-- previous entry (a two hour film is about 1.5 kB).
--
-- No index beyond the primary key and no foreign key to items: every read is
-- by item id, and a row whose item was deleted is harmless — the item id of a
-- re-added file is the same, and the size/mtime guard then decides, which is
-- exactly what a cascade would have achieved with more moving parts.
CREATE TABLE keyframe_data (
    item_id TEXT PRIMARY KEY,
    size    INTEGER NOT NULL,
    mtime   INTEGER NOT NULL,
    ticks   BLOB NOT NULL
);
