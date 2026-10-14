-- First-run setup wizard state lives in server_settings under the key
-- StartupWizardCompleted. A database that already holds accounts predates the
-- wizard (or was provisioned out of band), so it is recorded as set up here:
-- upgrading must never re-open the anonymous setup routes.
INSERT OR IGNORE INTO server_settings (key, value)
SELECT 'StartupWizardCompleted', 'true' WHERE EXISTS (SELECT 1 FROM users);
