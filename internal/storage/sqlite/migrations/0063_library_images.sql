-- A media library can carry one picture of its own — the image a dashboard
-- draws on the library's card — separate from any catalog item's artwork. The
-- reference server publishes it as VirtualFolderInfo.PrimaryImageItemId plus
-- the item's own ImageTags; jellyfin-web's libraries page renders the card
-- image only when that field is present, falling back to the collection-type
-- icon otherwise.
--
-- Kept in its own table rather than in image_refs: image_refs.item_id is a
-- foreign key into items(id), and a library is not a catalog item here — the
-- CollectionFolder the API reports for it is synthesized from the libraries
-- row (api/dto.go), so there is no items row to hang artwork on. Keying by
-- library_id also lets the row cascade away with the library itself.
--
-- kind is part of the key because the image kinds an item supports are not
-- all meaningful for a folder, but Primary is the only one this project
-- stores today; the column exists so adding Backdrop later is not a schema
-- change. See docs/11-jellyfin-web-route-coverage.md.
CREATE TABLE library_images (
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'Primary',
    path       TEXT NOT NULL DEFAULT '',
    tag        TEXT NOT NULL DEFAULT '',
    blur_hash  TEXT NOT NULL DEFAULT '',
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    size       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (library_id, kind)
);
