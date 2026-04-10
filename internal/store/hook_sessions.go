package store

import "time"

type HookSession struct {
	ClaudeSessionID string  `json:"claude_session_id"`
	CWD             string  `json:"cwd"`
	LastEvent       *string `json:"last_event,omitempty"`
	LastEventAt     *string `json:"last_event_at,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

func (s *Store) UpsertHookSession(sessionID, cwd, event string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO hook_sessions (claude_session_id, cwd, last_event, last_event_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(claude_session_id) DO UPDATE SET
		   cwd = excluded.cwd,
		   last_event = excluded.last_event,
		   last_event_at = excluded.last_event_at`,
		sessionID, cwd, event, now,
	)
	return err
}

func (s *Store) GetHookSession(sessionID string) (*HookSession, error) {
	h := &HookSession{}
	err := s.db.QueryRow(
		`SELECT claude_session_id, cwd, last_event, last_event_at, created_at
		 FROM hook_sessions WHERE claude_session_id = ?`, sessionID,
	).Scan(&h.ClaudeSessionID, &h.CWD, &h.LastEvent, &h.LastEventAt, &h.CreatedAt)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (s *Store) ListHookSessions() ([]HookSession, error) {
	rows, err := s.db.Query(
		`SELECT claude_session_id, cwd, last_event, last_event_at, created_at
		 FROM hook_sessions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []HookSession
	for rows.Next() {
		var h HookSession
		if err := rows.Scan(&h.ClaudeSessionID, &h.CWD, &h.LastEvent, &h.LastEventAt, &h.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, rows.Err()
}
