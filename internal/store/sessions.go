package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Session struct {
	SessionID      string  `json:"session_id"`
	Source         string  `json:"source"`
	CWD            string  `json:"cwd"`
	Project        string  `json:"project"`
	Title          *string `json:"title,omitempty"`
	TranscriptPath *string `json:"transcript_path,omitempty"`
	Model          *string `json:"model,omitempty"`
	Status         string  `json:"status"`
	LastEvent      *string `json:"last_event,omitempty"`
	LastEventAt    *string `json:"last_event_at,omitempty"`
	// LastInteractedAt is when a human last looked at this session, which is a
	// different question from when its agent last ran. A session read a minute
	// ago and one untouched for a day both go quiet in last_event_at.
	LastInteractedAt *string `json:"last_interacted_at,omitempty"`
	LastUserMessage  *string `json:"last_user_message,omitempty"`
	Pinned           bool    `json:"pinned"`
	// SortOrder is the session's place in a hand-arranged list. Lower sorts
	// first and the scale is arbitrary — only the relative order is meaningful,
	// and it is ignored entirely unless the list is set to sort manually.
	SortOrder int `json:"sort_order"`
	// PermissionMode is the agent's permission mode. It is stored because the
	// mode is a per-invocation flag rather than conversation state: without a
	// record of it, a session that goes cold comes back in the default mode
	// and silently discards whatever the user chose.
	PermissionMode *string `json:"permission_mode,omitempty"`
	// ResumeID is what the provider needs to wake this session, and is opaque
	// to the daemon. It exists because helios mints the session id but not
	// every agent will take one: Codex generates its own and offers no flag to
	// set it, so the two ids differ and both must be kept. For Claude they are
	// equal and this is nil.
	ResumeID *string `json:"resume_id,omitempty"`
	// Terminal is the handle of the session's live terminal host, injected by
	// the daemon rather than stored: a cold session simply has none.
	Terminal *string `json:"terminal,omitempty"`
	// MemoryBytes is what the live terminal's process tree costs in resident
	// memory. Injected alongside Terminal, and absent for a cold session,
	// which costs nothing until it is woken.
	MemoryBytes         *int64  `json:"memory_bytes,omitempty"`
	CreatedAt           string  `json:"created_at"`
	EndedAt             *string `json:"ended_at,omitempty"`
	SupportsPromptQueue bool    `json:"supports_prompt_queue"`
	// GroupKey is the one group the session is filed under, or empty.
	GroupKey string `json:"group_key,omitempty"`
	// GroupPath is that group and its ancestors, outermost first, resolved by
	// the daemon so no client walks the tree itself. Only filled when the caller
	// asked for grouping, so one that does not group is served what it always
	// was.
	GroupPath []SessionGroup `json:"group_path,omitempty"`
}

// Label returns the session's display label: title, or truncated last user message, or "".
func (s *Session) Label(maxLen int) string {
	if s.Title != nil && *s.Title != "" {
		t := strings.TrimSpace(*s.Title)
		if maxLen > 0 && len(t) > maxLen {
			return t[:maxLen] + "…"
		}
		return t
	}
	if s.LastUserMessage != nil && *s.LastUserMessage != "" {
		msg := strings.TrimSpace(*s.LastUserMessage)
		if maxLen > 0 && len(msg) > maxLen {
			return msg[:maxLen] + "…"
		}
		return msg
	}
	return ""
}

// ComputePromptQueue sets SupportsPromptQueue based on provider capabilities
// and whether the session has a live terminal to queue into. Terminal must be
// injected before calling this.
func (s *Session) ComputePromptQueue(providerSupportsQueue bool) {
	s.SupportsPromptQueue = providerSupportsQueue && s.Terminal != nil && *s.Terminal != ""
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

	// A session inherits the groups of the newest session in the same
	// directory. Assigning a directory once is then enough: every later agent
	// started there joins on its own, including every worktree session, which
	// is what keeps manual grouping from needing an action per session.
	// Inherited on insert only — a later reorganisation does not reach back and
	// rewrite the sessions that already ran.
	inherited, err := s.groupForCWD(sess.CWD)
	if err != nil {
		return fmt.Errorf("inherit groups for %s: %w", sess.CWD, err)
	}

	_, err = s.db.Exec(
		`INSERT INTO sessions (session_id, source, cwd, project, title, transcript_path, model, status, last_event, last_event_at, sort_order, group_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT MIN(sort_order) FROM sessions), 0) - 1, ?)
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
		sess.Title, sess.TranscriptPath, sess.Model, sess.Status, sess.LastEvent, now, inherited,
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
		`INSERT OR IGNORE INTO sessions (session_id, source, cwd, project, title, transcript_path, model, status, last_event, last_event_at, last_user_message)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.SessionID, sess.Source, sess.CWD, sess.Project,
		sess.Title, sess.TranscriptPath, sess.Model, sess.Status, sess.LastEvent, sess.LastEventAt,
		sess.LastUserMessage,
	)
	return err
}

// UpdateSessionStatus updates a session's status and last event.
func (s *Store) UpdateSessionStatus(sessionID, status, event string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	args := []interface{}{status, event, now}
	query := `UPDATE sessions SET status = ?, last_event = ?, last_event_at = ?`

	if status == "terminated" {
		query += `, ended_at = ?`
		args = append(args, now)
	}

	query += ` WHERE session_id = ?`
	args = append(args, sessionID)

	_, err := s.db.Exec(query, args...)
	return err
}

// TouchSession records that a human just looked at this session.
//
// Separate from last_event_at on purpose: that one moves whenever the agent
// does anything, including while nobody is watching, so it cannot answer "is
// anyone still interested in this".
func (s *Store) TouchSession(sessionID string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET last_interacted_at = ? WHERE session_id = ?`,
		time.Now().UTC().Format(time.RFC3339), sessionID,
	)
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

// UpdateSessionPermissionMode records the permission mode a session is running
// under, so waking it later can put it back in the same mode.
func (s *Store) UpdateSessionPermissionMode(sessionID, mode string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET permission_mode = ? WHERE session_id = ?`,
		mode, sessionID,
	)
	return err
}

// UpdateSessionTranscriptPath records where a session's transcript lives now.
//
// Deliberately not write-once. Claude Code names a transcript's directory after
// the session's cwd, so moving into a git worktree moves the file, and the path
// recorded at SessionStart is left pointing at nothing. Every hook carries the
// current path, so the last one to speak wins.
func (s *Store) UpdateSessionTranscriptPath(sessionID, path string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET transcript_path = ?
		 WHERE session_id = ? AND (transcript_path IS NULL OR transcript_path != ?)`,
		path, sessionID, path,
	)
	return err
}

// GetSession retrieves a session by ID.
func (s *Store) GetSession(sessionID string) (*Session, error) {
	sess := &Session{}
	err := s.db.QueryRow(
		`SELECT session_id, source, cwd, project, title, transcript_path, model, status,
		        last_event, last_event_at, last_interacted_at, last_user_message, pinned, sort_order,
		        permission_mode, resume_id, created_at, ended_at
		 FROM sessions WHERE session_id = ?`, sessionID,
	).Scan(&sess.SessionID, &sess.Source, &sess.CWD, &sess.Project,
		&sess.Title, &sess.TranscriptPath, &sess.Model, &sess.Status,
		&sess.LastEvent, &sess.LastEventAt, &sess.LastInteractedAt, &sess.LastUserMessage, &sess.Pinned, &sess.SortOrder,
		&sess.PermissionMode, &sess.ResumeID, &sess.CreatedAt, &sess.EndedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sess, err
}

// SessionQuery is what a caller is asking the session list for. A struct
// rather than a row of positional strings: there are six of them now, and four
// were already the same type.
type SessionQuery struct {
	// Query is free-text, tokenized by spaces; every token must match.
	Query string
	// Status matches exactly. Empty means any.
	Status string
	// Filter is "all" (default), "pinned" or "terminated".
	Filter string
	// CWD matches exactly. Empty means any.
	CWD string
	// Grouped asks for each session's groups, resolved to names and positions.
	// Off by default, so a caller that does not group is served what it always
	// was.
	Grouped bool
	// GroupKey narrows to sessions holding that group at any depth.
	GroupKey string
}

// ListSessions returns all sessions ordered by most recent activity.
func (s *Store) ListSessions() ([]Session, error) {
	return s.SearchSessions(SessionQuery{})
}

// SearchSessions returns sessions matching the query.
func (s *Store) SearchSessions(sq SessionQuery) ([]Session, error) {
	var where []string
	var args []interface{}

	// Tokenized text search
	if sq.Query != "" {
		for _, token := range strings.Fields(sq.Query) {
			pattern := "%" + token + "%"
			where = append(where, `(COALESCE(title,'') || ' ' || COALESCE(last_user_message,'') || ' ' || project || ' ' || cwd || ' ' || session_id) LIKE ?`)
			args = append(args, pattern)
		}
	}

	// Status filter
	if sq.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, sq.Status)
	}

	// Flag-based filter. Terminated is the archival state: there is no separate
	// archived flag to exclude here, and asking for the archive means asking
	// for what has ended.
	switch sq.Filter {
	case "pinned":
		where = append(where, `pinned = 1`)
	case "terminated":
		where = append(where, `status = 'terminated'`)
	}

	// CWD filter
	if sq.CWD != "" {
		where = append(where, `cwd = ?`)
		args = append(args, sq.CWD)
	}

	// Asking for a group means asking for what is under it, so the filter covers
	// the whole branch rather than the one node.
	if sq.GroupKey != "" {
		branch, err := s.descendantsOf(sq.GroupKey)
		if err != nil {
			return nil, err
		}
		where = append(where, `group_key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(branch)), ",")+`)`)
		for _, key := range branch {
			args = append(args, key)
		}
	}

	q := `SELECT session_id, source, cwd, project, title, transcript_path, model, status,
	        last_event, last_event_at, last_interacted_at, last_user_message, pinned, sort_order,
	        permission_mode, resume_id, created_at, ended_at, group_key
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

	// Read the raw arrays alongside the rows and resolve them afterwards, from
	// one read of a table with a handful of rows in it. Joining per level would
	// mean generating SQL from the requested depth to answer the same question.
	var result []Session
	var raw []sql.NullString
	for rows.Next() {
		var sess Session
		var held sql.NullString
		if err := rows.Scan(&sess.SessionID, &sess.Source, &sess.CWD, &sess.Project,
			&sess.Title, &sess.TranscriptPath, &sess.Model, &sess.Status,
			&sess.LastEvent, &sess.LastEventAt, &sess.LastInteractedAt, &sess.LastUserMessage, &sess.Pinned, &sess.SortOrder,
			&sess.PermissionMode, &sess.ResumeID, &sess.CreatedAt, &sess.EndedAt,
			&held); err != nil {
			return nil, err
		}
		result = append(result, sess)
		raw = append(raw, held)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if sq.Grouped {
		if err := s.attachGroups(result, raw); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// attachGroups resolves each session's group into the path from the root down.
// One read of a table with a handful of rows, rather than a recursive CTE per
// query. A key naming a group that is gone resolves to nothing rather than to a
// broken path.
func (s *Store) attachGroups(sessions []Session, raw []sql.NullString) error {
	groups, err := s.ListGroups()
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}
	byKey := make(map[string]Group, len(groups))
	for _, g := range groups {
		byKey[g.Key] = g
	}

	for i := range sessions {
		if !raw[i].Valid || raw[i].String == "" {
			continue
		}
		path := pathOf(raw[i].String, byKey)
		if len(path) == 0 {
			continue
		}
		sessions[i].GroupKey = raw[i].String
		sessions[i].GroupPath = path
	}
	return nil
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

// IncrementAutoTitleAttempts atomically increments autotitle_attempts and returns the new value.
func (s *Store) IncrementAutoTitleAttempts(sessionID string) (int, error) {
	_, err := s.db.Exec(
		`UPDATE sessions SET autotitle_attempts = autotitle_attempts + 1 WHERE session_id = ?`,
		sessionID,
	)
	if err != nil {
		return 0, err
	}
	var count int
	err = s.db.QueryRow(`SELECT autotitle_attempts FROM sessions WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// SetSessionOrder writes a hand-arranged order, first id first.
//
// The whole list at once and in one transaction, rather than a position per
// session: dragging one card shifts every card it passed, so the client
// already knows the arrangement it wants, and sending it whole is both simpler
// and atomic. Numbering starts at zero, and a new session is given one less
// than the smallest, so anything the client did not mention stays above.
func (s *Store) SetSessionOrder(sessionIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE sessions SET sort_order = ? WHERE session_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for position, id := range sessionIDs {
		if _, err := stmt.Exec(position, id); err != nil {
			return fmt.Errorf("order %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// AutoTitleAttempts reports how many attempts a session has already spent,
// without spending another. An attempt is the session's budget for ever being
// named, so it is only worth counting once the model has actually answered.
func (s *Store) AutoTitleAttempts(sessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT autotitle_attempts FROM sessions WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// ResetAutoTitleAttempts resets autotitle_attempts to 0 for a session.
func (s *Store) ResetAutoTitleAttempts(sessionID string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET autotitle_attempts = 0 WHERE session_id = ?`,
		sessionID,
	)
	return err
}

// UpdateSessionPinned updates the pinned flag for a session.
func (s *Store) UpdateSessionPinned(sessionID string, pinned bool) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET pinned = ? WHERE session_id = ?`,
		pinned, sessionID,
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

// UpdateSessionResumeID records what the provider needs to wake this session.
//
// Written by the provider's own session-start hook, because an agent that
// mints its own id is the only thing that knows it.
func (s *Store) UpdateSessionResumeID(sessionID, resumeID string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET resume_id = ? WHERE session_id = ?`,
		resumeID, sessionID,
	)
	return err
}

// SessionByResumeID finds a session by the id its provider uses to wake it.
//
// The reverse lookup exists because an agent that mints its own id knows only
// that one. Correlating on it lets a hook find its session even when the
// helios id did not reach the agent's environment.
func (s *Store) SessionByResumeID(resumeID string) (*Session, error) {
	if resumeID == "" {
		return nil, nil
	}
	var id string
	err := s.db.QueryRow(
		`SELECT session_id FROM sessions WHERE resume_id = ? LIMIT 1`, resumeID,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetSession(id)
}
