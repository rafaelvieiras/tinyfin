package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func (s *Store) CreateAPIKey(ctx context.Context, appName string) (storage.APIKey, error) {
	k := storage.APIKey{Token: newToken(), AppName: appName, DateCreated: time.Now().UTC()}
	res, err := s.exec(ctx, `INSERT INTO api_keys (token, app_name, date_created) VALUES (?, ?, ?)`, k.Token, k.AppName, k.DateCreated)
	if err != nil {
		return storage.APIKey{}, fmt.Errorf("sqlite: creating API key: %w", err)
	}
	k.ID, _ = res.LastInsertId()
	return k, nil
}

func (s *Store) QueryAPIKeys(ctx context.Context) ([]storage.APIKey, error) {
	rows, err := s.ex.QueryContext(ctx, `SELECT id, token, app_name, date_created FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying API keys: %w", err)
	}
	defer rows.Close()
	var out []storage.APIKey
	for rows.Next() {
		var k storage.APIKey
		if err := rows.Scan(&k.ID, &k.Token, &k.AppName, &k.DateCreated); err != nil {
			return nil, fmt.Errorf("sqlite: scanning API key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) LookupAPIKey(ctx context.Context, token string) (storage.APIKey, error) {
	var k storage.APIKey
	err := s.ex.QueryRowContext(ctx, `SELECT id, token, app_name, date_created FROM api_keys WHERE token = ?`, token).
		Scan(&k.ID, &k.Token, &k.AppName, &k.DateCreated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.APIKey{}, fmt.Errorf("sqlite: API key not found: %w", err)
		}
		return storage.APIKey{}, fmt.Errorf("sqlite: looking up API key: %w", err)
	}
	return k, nil
}

func (s *Store) RevokeAPIKey(ctx context.Context, token string) error {
	if _, err := s.exec(ctx, `DELETE FROM api_keys WHERE token = ?`, token); err != nil {
		return fmt.Errorf("sqlite: revoking API key: %w", err)
	}
	return nil
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.ex.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite: reading setting %q: %w", key, err)
	}
	return v, true, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if _, err := s.exec(ctx, `INSERT INTO server_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("sqlite: writing setting %q: %w", key, err)
	}
	return nil
}
