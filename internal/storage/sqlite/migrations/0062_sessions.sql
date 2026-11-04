-- The live session registry (internal/api/sessions.go) is a map in memory,
-- so before this a restart erased every session at once: GET /Sessions came
-- back empty until each client happened to report again, and a client that
-- reconnected was given a brand new session id. The registry now writes each
-- session through to this table and reloads it at startup.
--
-- Deliberately absent: live playback state (restoring it would claim an item
-- is still playing when no client is) and transcode bookkeeping (the job
-- dies with the process). See docs/06-session-and-realtime-state.md.
CREATE TABLE sessions (
    device_id          TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL DEFAULT '',
    session_id         TEXT NOT NULL,
    user_name          TEXT NOT NULL DEFAULT '',
    client             TEXT NOT NULL DEFAULT '',
    device_name        TEXT NOT NULL DEFAULT '',
    app_version        TEXT NOT NULL DEFAULT '',
    remote_end_point   TEXT NOT NULL DEFAULT '',
    capabilities       TEXT NOT NULL DEFAULT '{}',
    last_activity_date TIMESTAMP
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
