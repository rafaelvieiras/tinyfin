-- Devices are kept per (device id, user): a second account signing in on the
-- same DeviceId adds an entry instead of replacing the first (admin.devices.R4).
-- The custom name moves to a per-DeviceId options table that outlives the
-- device entries (admin.devices.R7).
CREATE TABLE device_options (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id   TEXT NOT NULL UNIQUE,
    custom_name TEXT
);

INSERT INTO device_options (device_id, custom_name)
SELECT id, custom_name FROM devices WHERE custom_name <> '';

CREATE TABLE devices_new (
    id                 TEXT NOT NULL,
    name               TEXT NOT NULL DEFAULT '',
    app_name           TEXT NOT NULL DEFAULT '',
    app_version        TEXT NOT NULL DEFAULT '',
    last_user_id       TEXT NOT NULL DEFAULT '',
    last_user_name     TEXT NOT NULL DEFAULT '',
    date_last_activity TIMESTAMP,
    PRIMARY KEY (id, last_user_id)
);

INSERT INTO devices_new (id, name, app_name, app_version, last_user_id, last_user_name, date_last_activity)
SELECT id, name, app_name, app_version, last_user_id, last_user_name, date_last_activity FROM devices;

DROP TABLE devices;
ALTER TABLE devices_new RENAME TO devices;
CREATE INDEX idx_devices_last_user ON devices(last_user_id);
