-- API keys are credentials that belong to no user (administrator-level
-- access for integrations). They are kept apart from auth_tokens, whose rows
-- are bound to a user and a device.
CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    token        TEXT NOT NULL UNIQUE,
    app_name     TEXT NOT NULL DEFAULT '',
    date_created TIMESTAMP NOT NULL
);

-- Runtime server settings changed through the API (they override the
-- command-line defaults).
CREATE TABLE server_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
