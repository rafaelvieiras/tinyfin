-- A cast/crew credit's own photo, cached — the picture a client draws on an
-- item's detail page next to the actor's name. Its own table for the reason
-- library_images (0063) is: image_refs.item_id is a foreign key into
-- items(id), and a person is not a catalog item here. A person exists only as
-- a name an item carries (named_items), so there is no items row to hang a
-- picture on.
--
-- source is the absolute URL the picture is published at — the <thumb> of a
-- Kodi/Jellyfin NFO credit, or the profile URL a metadata provider answered
-- with. BaseItemPerson.PrimaryImageTag is derived from it, so the client asks
-- for a picture that is downloaded on the first request instead of during the
-- scan; tag is the same hash, kept here so a request's ?tag= can be matched
-- against a row without recomputing it.
--
-- The rest (path/tag/blur_hash/width/height/size) mirrors image_refs: a
-- pointer to a file on disk, never the bytes. failed_at records when a
-- download of source last failed, so a URL that 404s or times out is not
-- retried on every request; a changed source clears it. 0 means no failure on
-- record.
--
-- No index beyond the primary key: every read is by person id, either one (an
-- image request) or a credit list's worth at once (a detail response), and
-- nothing queries by source or by path.
CREATE TABLE person_images (
    person_id TEXT PRIMARY KEY,
    source    TEXT NOT NULL DEFAULT '',
    path      TEXT NOT NULL DEFAULT '',
    tag       TEXT NOT NULL DEFAULT '',
    blur_hash TEXT NOT NULL DEFAULT '',
    width     INTEGER NOT NULL DEFAULT 0,
    height    INTEGER NOT NULL DEFAULT 0,
    size      INTEGER NOT NULL DEFAULT 0,
    failed_at INTEGER NOT NULL DEFAULT 0
);
