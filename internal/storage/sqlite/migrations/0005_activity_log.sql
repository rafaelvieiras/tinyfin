-- Adds the activity_log table backing storage.ActivityLogStore — the
-- durable login-success/login-failure log GET /System/ActivityLog/Entries
-- serves. id is a real auto-increment integer, not this project's usual
-- UUID-shaped id column, matching the reference server's own int64
-- ActivityLogEntry.Id. user_id is nullable: a failed login attempt has no
-- authenticated user to attach.
CREATE TABLE activity_log (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL,
	short_overview TEXT NOT NULL DEFAULT '',
	type           TEXT NOT NULL,
	date           TIMESTAMP NOT NULL,
	user_id        TEXT,
	severity       TEXT NOT NULL
);

-- Backs QueryActivityEntries' MinDate filter and its newest-first ordering.
CREATE INDEX idx_activity_log_date ON activity_log (date);

-- Backs the HasUserID=true/false filter (IS NOT NULL / IS NULL on user_id).
CREATE INDEX idx_activity_log_user_id ON activity_log (user_id);
