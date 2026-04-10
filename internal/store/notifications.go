package store

import (
	"database/sql"
	"time"
)

type Notification struct {
	ID               string  `json:"id"`
	ClaudeSessionID  string  `json:"claude_session_id"`
	CWD              string  `json:"cwd"`
	Type             string  `json:"type"`
	Status           string  `json:"status"`
	ToolName         *string `json:"tool_name,omitempty"`
	ToolInput        *string `json:"tool_input,omitempty"`
	Detail           *string `json:"detail,omitempty"`
	ResolvedAt       *string `json:"resolved_at,omitempty"`
	ResolvedSource   *string `json:"resolved_source,omitempty"`
	CreatedAt        string  `json:"created_at"`
}

func (s *Store) CreateNotification(n *Notification) error {
	_, err := s.db.Exec(
		`INSERT INTO notifications (id, claude_session_id, cwd, type, status, tool_name, tool_input, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.ClaudeSessionID, n.CWD, n.Type, n.Status, n.ToolName, n.ToolInput, n.Detail,
	)
	return err
}

func (s *Store) GetNotification(id string) (*Notification, error) {
	n := &Notification{}
	err := s.db.QueryRow(
		`SELECT id, claude_session_id, cwd, type, status, tool_name, tool_input, detail, resolved_at, resolved_source, created_at
		 FROM notifications WHERE id = ?`, id,
	).Scan(&n.ID, &n.ClaudeSessionID, &n.CWD, &n.Type, &n.Status, &n.ToolName, &n.ToolInput, &n.Detail, &n.ResolvedAt, &n.ResolvedSource, &n.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

func (s *Store) ListNotifications(status, nType string) ([]Notification, error) {
	query := `SELECT id, claude_session_id, cwd, type, status, tool_name, tool_input, detail, resolved_at, resolved_source, created_at
			  FROM notifications WHERE 1=1`
	args := []interface{}{}

	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if nType != "" {
		query += " AND type = ?"
		args = append(args, nType)
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.ClaudeSessionID, &n.CWD, &n.Type, &n.Status, &n.ToolName, &n.ToolInput, &n.Detail, &n.ResolvedAt, &n.ResolvedSource, &n.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) LastSessionDetail(sessionID string) string {
	var detail string
	err := s.db.QueryRow(
		`SELECT detail FROM notifications WHERE claude_session_id = ? AND type = 'permission' AND detail IS NOT NULL ORDER BY created_at DESC LIMIT 1`,
		sessionID,
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

type AlreadyResolvedError struct{}

func (e AlreadyResolvedError) Error() string { return "already resolved" }

var ErrAlreadyResolved = &AlreadyResolvedError{}
