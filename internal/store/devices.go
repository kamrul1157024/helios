package store

import (
	"database/sql"
	"time"
)

type Device struct {
	KID        string  `json:"kid"`
	Name       string  `json:"name"`
	PublicKey  string  `json:"public_key"`
	Status     string  `json:"status"`
	LastSeenAt *string `json:"last_seen_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
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
		`SELECT kid, name, public_key, status, last_seen_at, created_at
		 FROM devices WHERE kid = ?`, kid,
	).Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.LastSeenAt, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) GetActiveDevice(kid string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(
		`SELECT kid, name, public_key, status, last_seen_at, created_at
		 FROM devices WHERE kid = ? AND status = 'active'`, kid,
	).Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.LastSeenAt, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(
		`SELECT kid, name, public_key, status, last_seen_at, created_at
		 FROM devices ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.KID, &d.Name, &d.PublicKey, &d.Status, &d.LastSeenAt, &d.CreatedAt); err != nil {
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

func (s *Store) CountDevices() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&count)
	return count, err
}
