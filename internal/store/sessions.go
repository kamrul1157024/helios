package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"time"
)

type Session struct {
	SessionID       string  `json:"session_id"`
	Source          string  `json:"source"`
	CWD             string  `json:"cwd"`
	Project         string  `json:"project"`
	Title           *string `json:"title,omitempty"`
	TranscriptPath  *string `json:"transcript_path,omitempty"`
	Model           *string `json:"model,omitempty"`
	Status          string  `json:"status"`
	LastEvent       *string `json:"last_event,omitempty"`
	LastEventAt     *string `json:"last_event_at,omitempty"`
	LastUserMessage *string `json:"last_user_message,omitempty"`
	Pinned          bool    `json:"pinned"`
	Archived        bool    `json:"archived"`
	TmuxPane        *string `json:"tmux_pane,omitempty"`
	TmuxPID         *int    `json:"tmux_pid,omitempty"`
	CreatedAt       string  `json:"created_at"`
	EndedAt         *string `json:"ended_at,omitempty"`
}

type Subagent struct {
	AgentID         string  `json:"agent_id"`
	ParentSessionID string  `json:"parent_session_id"`
	AgentType       *string `json:"agent_type,omitempty"`
	Description     *string `json:"description,omitempty"`
	Status          string  `json:"status"`
	TranscriptPath  *string `json:"transcript_path,omitempty"`
	CreatedAt       string  `json:"created_at"`
	EndedAt         *string `json:"ended_at,omitempty"`
}

// UpsertSession creates or updates a session.
func (s *Store) UpsertSession(sess *Session) error {
	if sess.Project == "" && sess.CWD != "" {
		sess.Project = filepath.Base(sess.CWD)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.Exec(
		`INSERT INTO sessions (session_id, source, cwd, project, title, transcript_path, model, status, last_event, last_event_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   cwd = COALESCE(excluded.cwd, sessions.cwd),
		   project = COALESCE(excluded.project, sessions.project),
		   title = COALESCE(sessions.title, excluded.title),
		   transcript_path = COALESCE(excluded.transcript_path, sessions.transcript_path),
		   model = COALESCE(excluded.model, sessions.model),
		   status = excluded.status,
		   last_event = excluded.last_event,
		   last_event_at = excluded.last_event_at`,
		sess.SessionID, sess.Source, sess.CWD, sess.Project,
		sess.Title, sess.TranscriptPath, sess.Model, sess.Status, sess.LastEvent, now,
	)
	return err
}

// InsertDiscoveredSession inserts a session discovered from transcript files.
// Unlike UpsertSession, it preserves the caller-provided timestamps and
// does not overwrite existing sessions.
func (s *Store) InsertDiscoveredSession(sess *Session) error {
	if sess.Project == "" && sess.CWD != "" {
		sess.Project = filepath.Base(sess.CWD)
	}

	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO sessions (session_id, source, cwd, project, title, transcript_path, model, status, last_event, last_event_at, last_user_message, tmux_pane, tmux_pid)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.SessionID, sess.Source, sess.CWD, sess.Project,
		sess.Title, sess.TranscriptPath, sess.Model, sess.Status, sess.LastEvent, sess.LastEventAt,
		sess.LastUserMessage, sess.TmuxPane, sess.TmuxPID,
	)
	return err
}

// UpdateSessionStatus updates a session's status and last event.
func (s *Store) UpdateSessionStatus(sessionID, status, event string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	args := []interface{}{status, event, now}
	query := `UPDATE sessions SET status = ?, last_event = ?, last_event_at = ?`

	if status == "ended" {
		query += `, ended_at = ?`
		args = append(args, now)
	}

	query += ` WHERE session_id = ?`
	args = append(args, sessionID)

	_, err := s.db.Exec(query, args...)
	return err
}

// UpdateSessionLastUserMessage stores the last user prompt for a session.
func (s *Store) UpdateSessionLastUserMessage(sessionID, message string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET last_user_message = ? WHERE session_id = ?`,
		message, sessionID,
	)
	return err
}

// UpdateSessionTranscriptPath sets the transcript path if not already set.
func (s *Store) UpdateSessionTranscriptPath(sessionID, path string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET transcript_path = ? WHERE session_id = ? AND transcript_path IS NULL`,
		path, sessionID,
	)
	return err
}

// UpdateSessionTmuxPane sets the tmux pane ID for a session.
func (s *Store) UpdateSessionTmuxPane(sessionID, paneID string, pid int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET tmux_pane = ?, tmux_pid = ? WHERE session_id = ?`,
		paneID, pid, sessionID,
	)
	return err
}

// GetSession retrieves a session by ID.
func (s *Store) GetSession(sessionID string) (*Session, error) {
	sess := &Session{}
	err := s.db.QueryRow(
		`SELECT session_id, source, cwd, project, title, transcript_path, model, status,
		        last_event, last_event_at, last_user_message, pinned, archived,
		        tmux_pane, tmux_pid, created_at, ended_at
		 FROM sessions WHERE session_id = ?`, sessionID,
	).Scan(&sess.SessionID, &sess.Source, &sess.CWD, &sess.Project,
		&sess.Title, &sess.TranscriptPath, &sess.Model, &sess.Status,
		&sess.LastEvent, &sess.LastEventAt, &sess.LastUserMessage, &sess.Pinned, &sess.Archived,
		&sess.TmuxPane, &sess.TmuxPID,
		&sess.CreatedAt, &sess.EndedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sess, err
}

// ListSessions returns all non-archived sessions ordered by most recent activity.
func (s *Store) ListSessions() ([]Session, error) {
	return s.SearchSessions("", "", "", "")
}

// SearchSessions returns sessions matching the given filters.
// query: free-text search (tokenized by spaces, all tokens must match).
// status: exact match on session status (empty = no filter).
// filter: "all" (default, excludes archived), "pinned", "archived".
// cwd: exact match on session CWD (empty = no filter).
func (s *Store) SearchSessions(query, status, filter, cwd string) ([]Session, error) {
	var where []string
	var args []interface{}

	// Tokenized text search
	if query != "" {
		for _, token := range strings.Fields(query) {
			pattern := "%" + token + "%"
			where = append(where, `(COALESCE(title,'') || ' ' || COALESCE(last_user_message,'') || ' ' || project || ' ' || cwd || ' ' || session_id) LIKE ?`)
			args = append(args, pattern)
		}
	}

	// Status filter
	if status != "" {
		where = append(where, `status = ?`)
		args = append(args, status)
	}

	// Flag-based filter
	switch filter {
	case "pinned":
		where = append(where, `pinned = 1 AND archived = 0`)
	case "archived":
		where = append(where, `archived = 1`)
	default: // "all" or empty
		where = append(where, `archived = 0`)
	}

	// CWD filter
	if cwd != "" {
		where = append(where, `cwd = ?`)
		args = append(args, cwd)
	}

	q := `SELECT session_id, source, cwd, project, title, transcript_path, model, status,
	        last_event, last_event_at, last_user_message, pinned, archived,
	        tmux_pane, tmux_pid, created_at, ended_at
	 FROM sessions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += ` ORDER BY COALESCE(last_event_at, created_at) DESC LIMIT 1000`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.SessionID, &sess.Source, &sess.CWD, &sess.Project,
			&sess.Title, &sess.TranscriptPath, &sess.Model, &sess.Status,
			&sess.LastEvent, &sess.LastEventAt, &sess.LastUserMessage, &sess.Pinned, &sess.Archived,
			&sess.TmuxPane, &sess.TmuxPID,
			&sess.CreatedAt, &sess.EndedAt); err != nil {
			return nil, err
		}
		result = append(result, sess)
	}
	return result, rows.Err()
}

// DirectoryInfo holds aggregated info about sessions in a given CWD.
type DirectoryInfo struct {
	CWD          string `json:"cwd"`
	Project      string `json:"project"`
	SessionCount int    `json:"session_count"`
	ActiveCount  int    `json:"active_count"`
}

// ListDirectories returns all distinct CWDs with session counts.
func (s *Store) ListDirectories() ([]DirectoryInfo, error) {
	rows, err := s.db.Query(
		`SELECT cwd, project,
		        COUNT(*) as session_count,
		        SUM(CASE WHEN status IN ('active','waiting_permission','compacting','starting') THEN 1 ELSE 0 END) as active_count
		 FROM sessions
		 GROUP BY cwd
		 ORDER BY MAX(COALESCE(last_event_at, created_at)) DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DirectoryInfo
	for rows.Next() {
		var d DirectoryInfo
		if err := rows.Scan(&d.CWD, &d.Project, &d.SessionCount, &d.ActiveCount); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// UpdateSessionTitle sets or clears the user-defined session title.
func (s *Store) UpdateSessionTitle(sessionID, title string) error {
	var titleVal interface{}
	if title != "" {
		titleVal = title
	}
	_, err := s.db.Exec(
		`UPDATE sessions SET title = ? WHERE session_id = ?`,
		titleVal, sessionID,
	)
	return err
}

// UpdateSessionFlags updates the pinned and archived flags for a session.
func (s *Store) UpdateSessionFlags(sessionID string, pinned, archived bool) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET pinned = ?, archived = ? WHERE session_id = ?`,
		pinned, archived, sessionID,
	)
	return err
}

// DeleteSession permanently removes a session and its subagents.
func (s *Store) DeleteSession(sessionID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec(`DELETE FROM subagents WHERE parent_session_id = ?`, sessionID)
	tx.Exec(`DELETE FROM notifications WHERE source_session = ?`, sessionID)
	if _, err := tx.Exec(`DELETE FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSubagent inserts a new subagent record.
func (s *Store) CreateSubagent(sub *Subagent) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO subagents (agent_id, parent_session_id, agent_type, description, status, transcript_path)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sub.AgentID, sub.ParentSessionID, sub.AgentType, sub.Description, sub.Status, sub.TranscriptPath,
	)
	return err
}

// UpdateSubagentStatus marks a subagent as completed.
func (s *Store) UpdateSubagentStatus(agentID, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`UPDATE subagents SET status = ?, ended_at = ? WHERE agent_id = ?`,
		status, now, agentID,
	)
	return err
}

// ListSubagents returns all subagents for a session.
func (s *Store) ListSubagents(parentSessionID string) ([]Subagent, error) {
	rows, err := s.db.Query(
		`SELECT agent_id, parent_session_id, agent_type, description, status, transcript_path, created_at, ended_at
		 FROM subagents WHERE parent_session_id = ? ORDER BY created_at ASC`,
		parentSessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Subagent
	for rows.Next() {
		var sub Subagent
		if err := rows.Scan(&sub.AgentID, &sub.ParentSessionID, &sub.AgentType,
			&sub.Description, &sub.Status, &sub.TranscriptPath,
			&sub.CreatedAt, &sub.EndedAt); err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	return result, rows.Err()
}

// GetSubagent retrieves a subagent by ID.
func (s *Store) GetSubagent(agentID string) (*Subagent, error) {
	sub := &Subagent{}
	err := s.db.QueryRow(
		`SELECT agent_id, parent_session_id, agent_type, description, status, transcript_path, created_at, ended_at
		 FROM subagents WHERE agent_id = ?`, agentID,
	).Scan(&sub.AgentID, &sub.ParentSessionID, &sub.AgentType,
		&sub.Description, &sub.Status, &sub.TranscriptPath,
		&sub.CreatedAt, &sub.EndedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sub, err
}
