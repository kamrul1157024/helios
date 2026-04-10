package store

type PushSubscription struct {
	ID        int64  `json:"id"`
	Endpoint  string `json:"endpoint"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	DeviceKID string `json:"device_kid,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreatePushSubscription(sub *PushSubscription) error {
	_, err := s.db.Exec(
		`INSERT INTO push_subscriptions (endpoint, p256dh, auth, device_kid)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		   p256dh = excluded.p256dh,
		   auth = excluded.auth,
		   device_kid = excluded.device_kid`,
		sub.Endpoint, sub.P256dh, sub.Auth, sub.DeviceKID,
	)
	return err
}

func (s *Store) ListPushSubscriptions() ([]PushSubscription, error) {
	rows, err := s.db.Query(
		`SELECT id, endpoint, p256dh, auth, device_kid, created_at FROM push_subscriptions`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.DeviceKID, &sub.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	return result, rows.Err()
}

func (s *Store) DeletePushSubscription(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}
