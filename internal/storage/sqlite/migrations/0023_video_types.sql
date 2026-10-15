-- Disc structures and .strm files. video_type is 'Dvd', 'BluRay' (path is the
-- disc's folder) or 'Iso' (path is a disc image file) and is empty for a plain
-- video file; iso_type is the kind of disc inside an image when known;
-- remote_url is the address a .strm file holds (path stays the .strm file).
ALTER TABLE items ADD COLUMN video_type TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN iso_type TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN remote_url TEXT NOT NULL DEFAULT '';
