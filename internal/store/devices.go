package store

import (
	"database/sql"
	"time"
)

type Device struct {
	KID         string  `json:"kid"`
	Name        string  `json:"name"`
	PublicKey   string  `json:"public_key"`
	Status      string  `json:"status"`
	Platform    string  `json:"platform"`
	Browser     string  `json:"browser"`
	PushEnabled bool    `json:"push_enabled"`
	LastSeenAt  *string `json:"last_seen_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

func (s *Store) CreateDevice(d *Device) error {
	_, err := s.db.Exec(
		`INSERT INTO devices (kid, name, public_key, status) VALUES (?, ?, ?, ?)`,
		d.KID, d.Name, d.PublicKey, d.Status,
	)
	return err
}

func (s *Store) GetDevice(kid string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(
		`SELECT d.kid, d.name, d.public_key, d.status, d.platform, d.browser, d.last_seen_at, d.created_at,
		        EXISTS(SELECT 1 FROM push_subscriptions ps WHERE ps.device_kid = d.kid) as push_enabled
		 FROM devices d WHERE d.kid = ?`, kid,
	).Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.Platform, &d.Browser, &d.LastSeenAt, &d.CreatedAt, &d.PushEnabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) GetActiveDevice(kid string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(
		`SELECT d.kid, d.name, d.public_key, d.status, d.platform, d.browser, d.last_seen_at, d.created_at,
		        EXISTS(SELECT 1 FROM push_subscriptions ps WHERE ps.device_kid = d.kid) as push_enabled
		 FROM devices d WHERE d.kid = ? AND d.status = 'active'`, kid,
	).Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.Platform, &d.Browser, &d.LastSeenAt, &d.CreatedAt, &d.PushEnabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(
		`SELECT d.kid, d.name, d.public_key, d.status, d.platform, d.browser, d.last_seen_at, d.created_at,
		        EXISTS(SELECT 1 FROM push_subscriptions ps WHERE ps.device_kid = d.kid) as push_enabled
		 FROM devices d ORDER BY d.created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.Platform, &d.Browser, &d.LastSeenAt, &d.CreatedAt, &d.PushEnabled); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) RevokeDevice(kid string) error {
	_, err := s.db.Exec(
		`UPDATE devices SET status = 'revoked' WHERE kid = ?`, kid,
	)
	return err
}

func (s *Store) UpdateDeviceLastSeen(kid string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`UPDATE devices SET last_seen_at = ? WHERE kid = ?`, now, kid,
	)
	return err
}

func (s *Store) ActivateDevice(kid string) error {
	_, err := s.db.Exec(
		`UPDATE devices SET status = 'active' WHERE kid = ? AND status = 'pending'`, kid,
	)
	return err
}

func (s *Store) UpdateDeviceMetadata(kid, name, platform, browser string) error {
	_, err := s.db.Exec(
		`UPDATE devices SET name = ?, platform = ?, browser = ? WHERE kid = ?`,
		name, platform, browser, kid,
	)
	return err
}

func (s *Store) CountDevices() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&count)
	return count, err
}

func (s *Store) CountActiveDevices() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE status = 'active'`).Scan(&count)
	return count, err
}

func (s *Store) RekeyDevice(kid, newPublicKey string) error {
	_, err := s.db.Exec(
		`UPDATE devices SET public_key = ?, status = 'pending' WHERE kid = ?`,
		newPublicKey, kid,
	)
	return err
}

// GetDeviceByPublicKey finds a device using a specific public key.
// Returns nil if no device uses this key.
func (s *Store) GetDeviceByPublicKey(publicKey string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(
		`SELECT kid, name, public_key, status, platform, browser, last_seen_at, created_at, 0 as push_enabled
		 FROM devices WHERE public_key = ? AND status != 'revoked' LIMIT 1`, publicKey,
	).Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.Platform, &d.Browser, &d.LastSeenAt, &d.CreatedAt, &d.PushEnabled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

// UpsertDevice creates a new device or updates an existing one's public key.
func (s *Store) UpsertDevice(kid, publicKey string) error {
	_, err := s.db.Exec(
		`INSERT INTO devices (kid, name, public_key, status) VALUES (?, '', ?, 'pending')
		 ON CONFLICT(kid) DO UPDATE SET public_key = ?, status = 'pending'`,
		kid, publicKey, publicKey,
	)
	return err
}
