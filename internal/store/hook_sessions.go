package store

import "time"

// UpsertHookSession is a compatibility shim — writes to the sessions table.
// Used by hook handlers that only need to update last_event.
func (s *Store) UpsertHookSession(sessionID, cwd, event string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO sessions (session_id, source, cwd, project, status, last_event, last_event_at)
		 VALUES (?, 'claude', ?, '', 'active', ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   cwd = COALESCE(excluded.cwd, sessions.cwd),
		   last_event = excluded.last_event,
		   last_event_at = excluded.last_event_at`,
		sessionID, cwd, event, now,
	)
	return err
}
