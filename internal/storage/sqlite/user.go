package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

const userColumns = `id, name, password_hash, is_administrator, is_disabled, enable_auto_login, policy_json, last_login_date, last_activity_date`

func scanUser(rs rowScanner) (storage.User, error) {
	var (
		id, name, passwordHash, policyJSON      string
		isAdministrator, isDisabled, enableAuto bool
		lastLoginDate, lastActivityDate         sql.NullTime
	)
	if err := rs.Scan(&id, &name, &passwordHash, &isAdministrator, &isDisabled, &enableAuto, &policyJSON, &lastLoginDate, &lastActivityDate); err != nil {
		return storage.User{}, err
	}
	user := storage.User{
		ID:              storage.UserID(id),
		Name:            name,
		PasswordHash:    passwordHash,
		IsAdministrator: isAdministrator,
		IsDisabled:      isDisabled,
		EnableAutoLogin: enableAuto,
		Policy:          policyJSON,
	}
	if lastLoginDate.Valid {
		v := lastLoginDate.Time
		user.LastLoginDate = &v
	}
	if lastActivityDate.Valid {
		v := lastActivityDate.Time
		user.LastActivityDate = &v
	}
	return user, nil
}

func (s *Store) GetUser(ctx context.Context, id storage.UserID) (storage.User, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, string(id))
	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.User{}, fmt.Errorf("sqlite: user %q not found: %w", id, err)
		}
		return storage.User{}, fmt.Errorf("sqlite: getting user %q: %w", id, err)
	}
	return user, nil
}

func (s *Store) GetUserByName(ctx context.Context, name string) (storage.User, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE name = ? COLLATE NOCASE ORDER BY name = ? DESC LIMIT 1`, name, name)
	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.User{}, fmt.Errorf("sqlite: user %q not found: %w", name, err)
		}
		return storage.User{}, fmt.Errorf("sqlite: getting user by name %q: %w", name, err)
	}
	return user, nil
}

func (s *Store) QueryUsers(ctx context.Context) ([]storage.User, error) {
	rows, err := s.ex.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying users: %w", err)
	}
	defer rows.Close()

	var users []storage.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scanning user row: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating user rows: %w", err)
	}
	return users, nil
}

// CreateUser always mints a fresh ID via newID, ignoring any ID the caller
// set on user — "Create" (unlike the Upsert* methods elsewhere in this
// package) returns the new ID specifically because the store, not the
// caller, is the ID authority here.
func (s *Store) CreateUser(ctx context.Context, user storage.User) (storage.UserID, error) {
	id := storage.UserID(newID())

	const query = `INSERT INTO users (id, name, password_hash, is_administrator, is_disabled, enable_auto_login, policy_json, last_login_date, last_activity_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.exec(ctx, query,
		string(id), user.Name, user.PasswordHash, user.IsAdministrator, user.IsDisabled, user.EnableAutoLogin, user.Policy,
		ptrToAny(user.LastLoginDate), ptrToAny(user.LastActivityDate),
	)
	if err != nil {
		return "", fmt.Errorf("sqlite: creating user %q: %w", user.Name, err)
	}
	return id, nil
}

func (s *Store) UpdateUser(ctx context.Context, user storage.User) error {
	const query = `UPDATE users SET name = ?, password_hash = ?, is_administrator = ?, is_disabled = ?, enable_auto_login = ?, policy_json = ?, last_login_date = ?, last_activity_date = ?
		WHERE id = ?`

	res, err := s.exec(ctx, query,
		user.Name, user.PasswordHash, user.IsAdministrator, user.IsDisabled, user.EnableAutoLogin, user.Policy,
		ptrToAny(user.LastLoginDate), ptrToAny(user.LastActivityDate),
		string(user.ID),
	)
	if err != nil {
		return fmt.Errorf("sqlite: updating user %q: %w", user.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: updating user %q: %w", user.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite: updating user %q: %w", user.ID, sql.ErrNoRows)
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id storage.UserID) error {
	return s.withTx(ctx, func(ex dbtx) error {
		// The playlists the user owns go with the account, and the shares
		// made to them are withdrawn.
		if _, err := ex.ExecContext(ctx, `DELETE FROM items WHERE id IN (SELECT playlist_id FROM playlist_access WHERE owner_id = ?)`, string(id)); err != nil {
			return fmt.Errorf("sqlite: deleting playlists of user %q: %w", id, err)
		}
		if _, err := ex.ExecContext(ctx, `DELETE FROM playlist_shares WHERE user_id = ?`, string(id)); err != nil {
			return fmt.Errorf("sqlite: deleting playlist shares of user %q: %w", id, err)
		}
		if _, err := ex.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, string(id)); err != nil {
			return fmt.Errorf("sqlite: deleting user %q: %w", id, err)
		}
		return nil
	})
}

// IssueToken mints a token bound to (userID, deviceID). A token is bound to
// exactly one such pair, so any earlier token of the same pair is revoked
// (users.auth.R10); tokens of other devices and other users are untouched.
func (s *Store) IssueToken(ctx context.Context, userID storage.UserID, deviceID storage.DeviceID) (storage.AuthToken, error) {
	return s.IssueSessionToken(ctx, userID, deviceID, "", "")
}

// IssueSessionToken is IssueToken recording the session's device name and
// remote address on the token.
func (s *Store) IssueSessionToken(ctx context.Context, userID storage.UserID, deviceID storage.DeviceID, deviceName, remoteAddress string) (storage.AuthToken, error) {
	token := storage.AuthToken{
		Token:         newToken(),
		UserID:        userID,
		DeviceID:      deviceID,
		IssuedAt:      time.Now().UTC(),
		DeviceName:    deviceName,
		RemoteAddress: remoteAddress,
	}

	if _, err := s.exec(ctx, `DELETE FROM auth_tokens WHERE user_id = ? AND device_id = ?`, string(userID), string(deviceID)); err != nil {
		return storage.AuthToken{}, fmt.Errorf("sqlite: revoking previous token of user %q: %w", userID, err)
	}

	const query = `INSERT INTO auth_tokens (token, user_id, device_id, issued_at, device_name, remote_address) VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.exec(ctx, query, token.Token, string(token.UserID), string(token.DeviceID), token.IssuedAt, token.DeviceName, token.RemoteAddress); err != nil {
		return storage.AuthToken{}, fmt.Errorf("sqlite: issuing token for user %q: %w", userID, err)
	}
	return token, nil
}

func (s *Store) LookupToken(ctx context.Context, token string) (storage.AuthToken, error) {
	row := s.ex.QueryRowContext(ctx, `SELECT token, user_id, device_id, issued_at, device_name, remote_address FROM auth_tokens WHERE token = ?`, token)

	var t storage.AuthToken
	var tok, userID, deviceID string
	if err := row.Scan(&tok, &userID, &deviceID, &t.IssuedAt, &t.DeviceName, &t.RemoteAddress); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.AuthToken{}, fmt.Errorf("sqlite: token not found: %w", err)
		}
		return storage.AuthToken{}, fmt.Errorf("sqlite: looking up token: %w", err)
	}
	t.Token = tok
	t.UserID = storage.UserID(userID)
	t.DeviceID = storage.DeviceID(deviceID)
	return t, nil
}

func (s *Store) QueryUserTokens(ctx context.Context, userID storage.UserID) ([]storage.AuthToken, error) {
	rows, err := s.ex.QueryContext(ctx, `SELECT token, device_id, issued_at, device_name, remote_address FROM auth_tokens WHERE user_id = ? ORDER BY issued_at, rowid`, string(userID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying tokens of user %q: %w", userID, err)
	}
	defer rows.Close()

	var out []storage.AuthToken
	for rows.Next() {
		t := storage.AuthToken{UserID: userID}
		var deviceID string
		if err := rows.Scan(&t.Token, &deviceID, &t.IssuedAt, &t.DeviceName, &t.RemoteAddress); err != nil {
			return nil, fmt.Errorf("sqlite: scanning token row: %w", err)
		}
		t.DeviceID = storage.DeviceID(deviceID)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating token rows: %w", err)
	}
	return out, nil
}

func (s *Store) RevokeToken(ctx context.Context, token string) error {
	if _, err := s.exec(ctx, `DELETE FROM auth_tokens WHERE token = ?`, token); err != nil {
		return fmt.Errorf("sqlite: revoking token: %w", err)
	}
	return nil
}

func (s *Store) RevokeUserTokens(ctx context.Context, userID storage.UserID, keepDevices []storage.DeviceID) error {
	query := `DELETE FROM auth_tokens WHERE user_id = ?`
	args := []any{string(userID)}
	if len(keepDevices) > 0 {
		query += ` AND device_id NOT IN (?` + strings.Repeat(`, ?`, len(keepDevices)-1) + `)`
		for _, d := range keepDevices {
			args = append(args, string(d))
		}
	}
	if _, err := s.exec(ctx, query, args...); err != nil {
		return fmt.Errorf("sqlite: revoking tokens for user %q: %w", userID, err)
	}
	return nil
}

func (s *Store) GetDisplayPreferences(ctx context.Context, userID storage.UserID, client string) (storage.DisplayPreferences, error) {
	const query = `SELECT view_type, index_by, sort_by, sort_order, remember_indexing, remember_sorting, scroll_direction, show_backdrop, show_sidebar, primary_image_width, primary_image_height, custom_prefs
		FROM display_preferences WHERE user_id = ? AND client = ?`

	var prefs storage.DisplayPreferences
	var customPrefsJSON string
	row := s.ex.QueryRowContext(ctx, query, string(userID), client)
	err := row.Scan(&prefs.ViewType, &prefs.IndexBy, &prefs.SortBy, &prefs.SortOrder, &prefs.RememberIndexing, &prefs.RememberSorting,
		&prefs.ScrollDirection, &prefs.ShowBackdrop, &prefs.ShowSidebar, &prefs.PrimaryImageWidth, &prefs.PrimaryImageHeight, &customPrefsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No preferences saved yet for this (user, client) pair is a
			// normal state, not an error — return the zero value.
			return storage.DisplayPreferences{}, nil
		}
		return storage.DisplayPreferences{}, fmt.Errorf("sqlite: getting display preferences for user %q client %q: %w", userID, client, err)
	}
	if err := unmarshalJSONInto(customPrefsJSON, &prefs.CustomPrefs); err != nil {
		return storage.DisplayPreferences{}, err
	}
	return prefs, nil
}

func (s *Store) SetDisplayPreferences(ctx context.Context, userID storage.UserID, client string, prefs storage.DisplayPreferences) error {
	customPrefsJSON, err := marshalJSON(prefs.CustomPrefs)
	if err != nil {
		return err
	}

	const query = `INSERT INTO display_preferences (user_id, client, view_type, index_by, sort_by, sort_order, remember_indexing, remember_sorting, scroll_direction, show_backdrop, show_sidebar, primary_image_width, primary_image_height, custom_prefs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, client) DO UPDATE SET
			view_type=excluded.view_type, index_by=excluded.index_by, sort_by=excluded.sort_by, sort_order=excluded.sort_order,
			remember_indexing=excluded.remember_indexing, remember_sorting=excluded.remember_sorting, scroll_direction=excluded.scroll_direction,
			show_backdrop=excluded.show_backdrop, show_sidebar=excluded.show_sidebar,
			primary_image_width=excluded.primary_image_width, primary_image_height=excluded.primary_image_height,
			custom_prefs=excluded.custom_prefs`

	_, err = s.exec(ctx, query,
		string(userID), client, prefs.ViewType, prefs.IndexBy, prefs.SortBy, prefs.SortOrder,
		prefs.RememberIndexing, prefs.RememberSorting, prefs.ScrollDirection, prefs.ShowBackdrop, prefs.ShowSidebar,
		prefs.PrimaryImageWidth, prefs.PrimaryImageHeight, customPrefsJSON,
	)
	if err != nil {
		return fmt.Errorf("sqlite: setting display preferences for user %q client %q: %w", userID, client, err)
	}
	return nil
}

func (s *Store) GetUserConfiguration(ctx context.Context, userID storage.UserID) (string, error) {
	var doc string
	err := s.ex.QueryRowContext(ctx, `SELECT json FROM user_configuration WHERE user_id = ?`, string(userID)).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: getting configuration of user %q: %w", userID, err)
	}
	return doc, nil
}

func (s *Store) SetUserConfiguration(ctx context.Context, userID storage.UserID, configuration string) error {
	const query = `INSERT INTO user_configuration (user_id, json) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET json = excluded.json`
	if _, err := s.exec(ctx, query, string(userID), configuration); err != nil {
		return fmt.Errorf("sqlite: setting configuration of user %q: %w", userID, err)
	}
	return nil
}
