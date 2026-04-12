package store

import "database/sql"

// GetSetting retrieves a setting value by key. Returns "" if not found.
func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting upserts a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?`,
		key, value, value,
	)
	return err
}

// DeleteSetting removes a setting by key.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// GetAllSettings returns all settings as a key-value map.
func (s *Store) GetAllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

// SetSettings bulk-upserts multiple settings.
func (s *Store) SetSettings(settings map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, value := range settings {
		if _, err := stmt.Exec(key, value, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
