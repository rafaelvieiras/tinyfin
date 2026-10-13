-- A token also records the session it stands for: the device name and the
-- remote address of the login that created it, so ending the session later
-- (replacement, logout, revocation) can write the activity entry with the
-- values of the session that ended, not of whatever replaced it.
ALTER TABLE auth_tokens ADD COLUMN device_name TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_tokens ADD COLUMN remote_address TEXT NOT NULL DEFAULT '';
