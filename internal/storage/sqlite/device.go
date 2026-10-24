package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// deviceSelect joins the per-DeviceId custom name onto the device entries.
const deviceSelect = `SELECT d.id, d.name, o.custom_name, d.app_name, d.app_version, d.last_user_id, d.last_user_name, d.date_last_activity
	FROM devices d LEFT JOIN device_options o ON o.device_id = d.id`

func scanDevice(rs rowScanner) (storage.Device, error) {
	var d storage.Device
	var id, lastUserID string
	var custom sql.NullString
	if err := rs.Scan(&id, &d.Name, &custom, &d.AppName, &d.AppVersion, &lastUserID, &d.LastUserName, &d.DateLastActivity); err != nil {
		return storage.Device{}, err
	}
	if custom.Valid {
		d.CustomName = &custom.String
	}
	d.ID = storage.DeviceID(id)
	d.LastUserID = storage.UserID(lastUserID)
	return d, nil
}

func (s *Store) GetDevice(ctx context.Context, id storage.DeviceID) (storage.Device, error) {
	row := s.ex.QueryRowContext(ctx, deviceSelect+` WHERE d.id = ? ORDER BY d.date_last_activity DESC LIMIT 1`, string(id))
	device, err := scanDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.Device{}, fmt.Errorf("sqlite: device %q not found: %w", id, err)
		}
		return storage.Device{}, fmt.Errorf("sqlite: getting device %q: %w", id, err)
	}
	return device, nil
}

func (s *Store) QueryDevices(ctx context.Context, userID storage.UserID) ([]storage.Device, error) {
	// An empty userID lists every device.
	rows, err := s.ex.QueryContext(ctx, deviceSelect+` WHERE (? = '' OR d.last_user_id = ?) ORDER BY d.date_last_activity DESC`, string(userID), string(userID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying devices for user %q: %w", userID, err)
	}
	defer rows.Close()

	var devices []storage.Device
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scanning device row: %w", err)
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating device rows: %w", err)
	}
	return devices, nil
}

func (s *Store) UpsertDevice(ctx context.Context, device storage.Device) error {
	if device.ID == "" {
		device.ID = storage.DeviceID(newID())
	}

	const query = `INSERT INTO devices (id, name, app_name, app_version, last_user_id, last_user_name, date_last_activity)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, last_user_id) DO UPDATE SET
			name=excluded.name, app_name=excluded.app_name, app_version=excluded.app_version,
			last_user_name=excluded.last_user_name, date_last_activity=excluded.date_last_activity`

	_, err := s.exec(ctx, query,
		string(device.ID), device.Name, device.AppName, device.AppVersion,
		string(device.LastUserID), device.LastUserName, device.DateLastActivity,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upserting device %q: %w", device.ID, err)
	}
	return nil
}

func (s *Store) TouchDeviceClient(ctx context.Context, id storage.DeviceID, userID storage.UserID, name, version string) error {
	if _, err := s.exec(ctx, `UPDATE devices SET name = CASE WHEN ? <> '' THEN ? ELSE name END,
			app_version = CASE WHEN ? <> '' THEN ? ELSE app_version END
		WHERE id = ? AND last_user_id = ? AND (name <> ? AND ? <> '' OR app_version <> ? AND ? <> '')`,
		name, name, version, version, string(id), string(userID), name, name, version, version); err != nil {
		return fmt.Errorf("sqlite: touching device %q: %w", id, err)
	}
	return nil
}

func (s *Store) DeleteDevice(ctx context.Context, id storage.DeviceID) error {
	if _, err := s.exec(ctx, `DELETE FROM auth_tokens WHERE device_id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: revoking tokens of device %q: %w", id, err)
	}
	if _, err := s.exec(ctx, `DELETE FROM devices WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting device %q: %w", id, err)
	}
	return nil
}

func (s *Store) GetDeviceOptions(ctx context.Context, id storage.DeviceID) (int64, *string, bool, error) {
	var optID int64
	var name sql.NullString
	err := s.ex.QueryRowContext(ctx, `SELECT id, custom_name FROM device_options WHERE device_id = ?`, string(id)).Scan(&optID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, fmt.Errorf("sqlite: reading options of device %q: %w", id, err)
	}
	if !name.Valid {
		return optID, nil, true, nil
	}
	return optID, &name.String, true, nil
}

func (s *Store) SetDeviceOptions(ctx context.Context, id storage.DeviceID, customName *string) error {
	var v any
	if customName != nil {
		v = *customName
	}
	if _, err := s.exec(ctx, `INSERT INTO device_options (device_id, custom_name) VALUES (?, ?)
		ON CONFLICT(device_id) DO UPDATE SET custom_name = excluded.custom_name`, string(id), v); err != nil {
		return fmt.Errorf("sqlite: saving options of device %q: %w", id, err)
	}
	return nil
}
