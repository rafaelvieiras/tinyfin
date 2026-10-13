-- Adds the per-account policy document (permissions, restrictions, lockout
-- and authentication settings) as an opaque JSON column on users. An empty
-- string means the policy was never set; the API layer then serves defaults.
-- is_administrator and is_disabled stay dedicated columns because
-- authentication reads them on every request.
ALTER TABLE users ADD COLUMN policy_json TEXT NOT NULL DEFAULT '';
