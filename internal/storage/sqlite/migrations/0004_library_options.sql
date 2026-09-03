-- Adds storage.Library.LibraryOptions — the reference server's
-- LibraryOptions blob for a library, persisted opaquely as raw JSON (not
-- modeled field-by-field) so POST /Library/VirtualFolders/LibraryOptions can
-- save it and GET /Library/VirtualFolders can echo it back honestly.
ALTER TABLE libraries ADD COLUMN library_options TEXT NOT NULL DEFAULT '';
