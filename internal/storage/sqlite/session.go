package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const sessionSelect = `SELECT device_id, user_id, session_id, user_name, client, device_name, app_version, remote_end_point, capabilities, last_activity_date
	FROM sessions`

func scanSession(rs rowScanner) (storage.Session, error) {
	var s storage.Session
	var deviceID, userID string
	var last *time.Time
	if err := rs.Scan(&deviceID, &userID, &s.SessionID, &s.UserName, &s.Client, &s.DeviceName, &s.AppVersion, &s.RemoteEndPoint, &s.Capabilities, &last); err != nil {
		return storage.Session{}, err
	}
	s.DeviceID = storage.DeviceID(deviceID)
	s.UserID = storage.UserID(userID)
	if last != nil {
		s.LastActivityDate = *last
	}
	return s, nil
}

func (s *Store) QuerySessions(ctx context.Context) ([]storage.Session, error) {
	rows, err := s.ex.QueryContext(ctx, sessionSelect+` ORDER BY last_activity_date DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying sessions: %w", err)
	}
	defer rows.Close()

	var out []storage.Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scanning session row: %w", err)
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating session rows: %w", err)
	}
	return out, nil
}

func (s *Store) SaveSession(ctx context.Context, session storage.Session) error {
	if session.SessionID == "" {
		session.SessionID = newID()
	}
	if session.Capabilities == "" {
		session.Capabilities = "{}"
	}
	const query = `INSERT INTO sessions (device_id, user_id, session_id, user_name, client, device_name, app_version, remote_end_point, capabilities, last_activity_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			user_id=excluded.user_id, session_id=excluded.session_id, user_name=excluded.user_name,
			client=excluded.client, device_name=excluded.device_name, app_version=excluded.app_version,
			remote_end_point=excluded.remote_end_point, capabilities=excluded.capabilities,
			last_activity_date=excluded.last_activity_date`

	if _, err := s.exec(ctx, query,
		string(session.DeviceID), string(session.UserID), session.SessionID, session.UserName,
		session.Client, session.DeviceName, session.AppVersion, session.RemoteEndPoint,
		session.Capabilities, session.LastActivityDate,
	); err != nil {
		return fmt.Errorf("sqlite: saving session %q: %w", session.DeviceID, err)
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, deviceID storage.DeviceID) error {
	if _, err := s.exec(ctx, `DELETE FROM sessions WHERE device_id = ?`, string(deviceID)); err != nil {
		return fmt.Errorf("sqlite: deleting session %q: %w", deviceID, err)
	}
	return nil
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID storage.UserID) error {
	if _, err := s.exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, string(userID)); err != nil {
		return fmt.Errorf("sqlite: deleting sessions of user %q: %w", userID, err)
	}
	return nil
}
