package store

import (
	"database/sql"
	"time"
)

type Notification struct {
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	SourceSession  string  `json:"source_session"`
	CWD            string  `json:"cwd"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	Title          *string `json:"title,omitempty"`
	Detail         *string `json:"detail,omitempty"`
	Payload        *string `json:"payload,omitempty"`
	Response       *string `json:"response,omitempty"`
	ResolvedAt     *string `json:"resolved_at,omitempty"`
	ResolvedSource *string `json:"resolved_source,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

func (s *Store) CreateNotification(n *Notification) error {
	_, err := s.db.Exec(
		`INSERT INTO notifications (id, source, source_session, cwd, type, status, title, detail, payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Source, n.SourceSession, n.CWD, n.Type, n.Status, n.Title, n.Detail, n.Payload,
	)
	return err
}

func (s *Store) GetNotification(id string) (*Notification, error) {
	n := &Notification{}
	err := s.db.QueryRow(
		`SELECT id, source, source_session, cwd, type, status, title, detail, payload, response,
		        resolved_at, resolved_source, created_at
		 FROM notifications WHERE id = ?`, id,
	).Scan(&n.ID, &n.Source, &n.SourceSession, &n.CWD, &n.Type, &n.Status,
		&n.Title, &n.Detail, &n.Payload, &n.Response,
		&n.ResolvedAt, &n.ResolvedSource, &n.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

func (s *Store) ListNotifications(source, status, nType string) ([]Notification, error) {
	query := `SELECT id, source, source_session, cwd, type, status, title, detail, payload, response,
	                 resolved_at, resolved_source, created_at
	          FROM notifications WHERE 1=1`
	args := []interface{}{}

	if source != "" {
		query += " AND source = ?"
		args = append(args, source)
	}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if nType != "" {
		query += " AND type = ?"
		args = append(args, nType)
	}

	query += " ORDER BY created_at DESC LIMIT 200"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Source, &n.SourceSession, &n.CWD, &n.Type, &n.Status,
			&n.Title, &n.Detail, &n.Payload, &n.Response,
			&n.ResolvedAt, &n.ResolvedSource, &n.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) UpdateNotificationResponse(id, response string) error {
	_, err := s.db.Exec(`UPDATE notifications SET response = ? WHERE id = ?`, response, id)
	return err
}

func (s *Store) LastSessionDetail(sourceSession string) string {
	var detail string
	err := s.db.QueryRow(
		`SELECT detail FROM notifications WHERE source_session = ? AND type LIKE '%.permission' AND detail IS NOT NULL ORDER BY created_at DESC LIMIT 1`,
		sourceSession,
	).Scan(&detail)
	if err != nil {
		return ""
	}
	return detail
}

func (s *Store) ResolveNotification(id, status, source string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.Exec(
		`UPDATE notifications SET status = ?, resolved_at = ?, resolved_source = ? WHERE id = ? AND status = 'pending'`,
		status, now, source, id,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlreadyResolved
	}
	return nil
}

func (s *Store) TruncateNotifications(keep int) error {
	_, err := s.db.Exec(`
		DELETE FROM notifications
		WHERE id NOT IN (
			SELECT id FROM notifications
			ORDER BY created_at DESC
			LIMIT ?
		)
		AND status != 'pending'
	`, keep)
	return err
}

type AlreadyResolvedError struct{}

func (e AlreadyResolvedError) Error() string { return "already resolved" }

var ErrAlreadyResolved = &AlreadyResolvedError{}
