package store

import (
	"fmt"
	"time"
)

type PairingToken struct {
	Token     string  `json:"token"`
	Status    string  `json:"status"`
	ClaimedBy *string `json:"claimed_by,omitempty"`
	ExpiresAt string  `json:"expires_at"`
	CreatedAt string  `json:"created_at"`
}

func (s *Store) CreatePairingToken(token string, expiresAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO pairing_tokens (token, expires_at) VALUES (?, ?)`,
		token, expiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ClaimPairingToken atomically validates and claims a token.
// Returns error if token doesn't exist, is expired, or already claimed.
func (s *Store) ClaimPairingToken(token, kid string) error {
	result, err := s.db.Exec(
		`UPDATE pairing_tokens
		 SET status = 'claimed', claimed_by = ?
		 WHERE token = ? AND status = 'pending' AND expires_at > datetime('now')`,
		kid, token,
	)
	if err != nil {
		return fmt.Errorf("claim token: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("token invalid, expired, or already claimed")
	}
	return nil
}

// CleanExpiredPairingTokens removes tokens that have expired.
func (s *Store) CleanExpiredPairingTokens() error {
	_, err := s.db.Exec(
		`DELETE FROM pairing_tokens WHERE expires_at < datetime('now')`,
	)
	return err
}
