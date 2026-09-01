package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const deviceColumns = `id, name, custom_name, app_name, app_version, last_user_id, last_user_name, date_last_activity`

func scanDevice(rs rowScanner) (storage.Device, error) {
	var d storage.Device
	var id, lastUserID string
	if err := rs.Scan(&id, &d.Name, &d.CustomName, &d.AppName, &d.AppVersion, &lastUserID, &d.LastUserName, &d.DateLastActivity); err != nil {
		return storage.Device{}, err
	}
	d.ID = storage.DeviceID(id)
	d.LastUserID = storage.UserID(lastUserID)
	return d, nil
}

func (s *Store) GetDevice(ctx context.Context, id storage.DeviceID) (storage.Device, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, string(id))
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
	rows, err := s.ex.QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE last_user_id = ? ORDER BY date_last_activity DESC`, string(userID))
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

	const query = `INSERT INTO devices (id, name, custom_name, app_name, app_version, last_user_id, last_user_name, date_last_activity)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, custom_name=excluded.custom_name, app_name=excluded.app_name, app_version=excluded.app_version,
			last_user_id=excluded.last_user_id, last_user_name=excluded.last_user_name, date_last_activity=excluded.date_last_activity`

	_, err := s.exec(ctx, query,
		string(device.ID), device.Name, device.CustomName, device.AppName, device.AppVersion,
		string(device.LastUserID), device.LastUserName, device.DateLastActivity,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upserting device %q: %w", device.ID, err)
	}
	return nil
}

func (s *Store) DeleteDevice(ctx context.Context, id storage.DeviceID) error {
	if _, err := s.exec(ctx, `DELETE FROM devices WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("sqlite: deleting device %q: %w", id, err)
	}
	return nil
}
