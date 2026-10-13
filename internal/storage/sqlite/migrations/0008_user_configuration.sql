-- Per-account preferences document (subtitle mode, language preferences,
-- home screen ordering, ...) as opaque JSON; the API layer owns its schema.
-- A missing row means the account never saved preferences (defaults apply).
CREATE TABLE user_configuration (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    json    TEXT NOT NULL
);
