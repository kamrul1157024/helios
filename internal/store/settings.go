package store

import (
	"database/sql"
	"strconv"
)

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

// Memory budget settings. The warm pool holds a running agent per session, so
// left alone it grows until the machine does. See docs/specs/42-cold-sessions.md.
const (
	SettingBudgetFraction = "memory.budget_fraction"
	SettingEvictEnabled   = "memory.evict"

	// DefaultBudgetFraction matches the quarter hostStats has always reported,
	// so enforcing the budget does not also change it.
	DefaultBudgetFraction = 0.25
)

// MemoryBudgetFraction is the share of physical memory the warm pool may hold.
//
// A fraction rather than a byte count: the same install runs on a 16 GB laptop
// and a 64 GB desktop. Zero means no limit. An unreadable or out-of-range value
// falls back to the default rather than to no limit, so a malformed setting
// cannot quietly disable the budget.
func (s *Store) MemoryBudgetFraction() float64 {
	raw, err := s.GetSetting(SettingBudgetFraction)
	if err != nil || raw == "" {
		return DefaultBudgetFraction
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 1 {
		return DefaultBudgetFraction
	}
	return value
}

// EvictionEnabled reports whether the daemon may let idle sessions go cold.
//
// Off unless asked for. Eviction kills a running agent and takes its terminal
// scrollback, and it reverses a decision made twice before — see
// docs/specs/42-cold-sessions.md. That is not something to start doing to
// somebody's machine because they upgraded.
func (s *Store) EvictionEnabled() bool {
	raw, err := s.GetSetting(SettingEvictEnabled)
	if err != nil || raw == "" {
		return false
	}
	return raw == "true"
}
