package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// An in-memory database lives inside its connection, so a second pooled
	// connection would open a separate, empty one. Pin the pool to a single
	// connection to keep concurrent callers on the same database.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'claude',
			source_session TEXT NOT NULL,
			cwd TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			title TEXT,
			detail TEXT,
			payload TEXT,
			response TEXT,
			resolved_at TEXT,
			resolved_source TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'claude',
			cwd TEXT NOT NULL,
			project TEXT,
			transcript_path TEXT,
			model TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			last_event TEXT,
			last_event_at TEXT,
			last_user_message TEXT,
			pinned INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_source ON sessions(source)`,
		`CREATE TABLE IF NOT EXISTS subagents (
			agent_id TEXT PRIMARY KEY,
			parent_session_id TEXT NOT NULL,
			agent_type TEXT,
			description TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			transcript_path TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_subagents_parent ON subagents(parent_session_id)`,
		`CREATE TABLE IF NOT EXISTS devices (
			kid TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			public_key TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			platform TEXT NOT NULL DEFAULT '',
			browser TEXT NOT NULL DEFAULT '',
			last_seen_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		// Migration: add platform/browser columns if missing (for existing DBs)
		`CREATE TABLE IF NOT EXISTS _migrations (id TEXT PRIMARY KEY)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications(type)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_source_session ON notifications(source_session)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_status ON devices(status)`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint TEXT NOT NULL UNIQUE,
			p256dh TEXT NOT NULL,
			auth TEXT NOT NULL,
			device_kid TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS pairing_tokens (
			token TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'pending',
			claimed_by TEXT,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pairing_tokens_status ON pairing_tokens(status)`,
		`CREATE TABLE IF NOT EXISTS reviewed_files (
			root TEXT NOT NULL,
			base TEXT NOT NULL,
			path TEXT NOT NULL,
			reviewed_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (root, base, path)
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}

	// Terminated is the one archival state now, so every archived session has
	// to be filed as terminated before the drop below takes the column away.
	//
	// Deliberately not an entry in the loop underneath, which throws away every
	// error: an UPDATE that failed there would still be recorded as done, and
	// the drop on the next line would destroy the rows it was meant to save.
	if err := s.promoteArchivedToTerminated(); err != nil {
		return fmt.Errorf("promote archived sessions: %w", err)
	}

	// Column migrations for existing DBs
	columnMigrations := []struct {
		id  string
		sql string
	}{
		{"add_devices_platform", `ALTER TABLE devices ADD COLUMN platform TEXT NOT NULL DEFAULT ''`},
		{"add_devices_browser", `ALTER TABLE devices ADD COLUMN browser TEXT NOT NULL DEFAULT ''`},
		{"migrate_hook_sessions_to_sessions", `INSERT OR IGNORE INTO sessions (session_id, source, cwd, project, status, last_event, last_event_at, created_at)
			SELECT claude_session_id, 'claude', cwd, '', 'ended', last_event, last_event_at, created_at
			FROM hook_sessions WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='hook_sessions')`},
		{"add_sessions_last_user_message", `ALTER TABLE sessions ADD COLUMN last_user_message TEXT`},
		{"add_sessions_pinned", `ALTER TABLE sessions ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`},
		{"add_sessions_archived", `ALTER TABLE sessions ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`},
		{"add_sessions_title", `ALTER TABLE sessions ADD COLUMN title TEXT`},
		{"create_settings_table", `CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`},
		{"drop_sessions_tmux_pane", `ALTER TABLE sessions DROP COLUMN tmux_pane`},
		{"drop_sessions_tmux_pid", `ALTER TABLE sessions DROP COLUMN tmux_pid`},
		{"add_sessions_autotitle_attempts", `ALTER TABLE sessions ADD COLUMN autotitle_attempts INTEGER NOT NULL DEFAULT 0`},
		{"add_sessions_managed", `ALTER TABLE sessions ADD COLUMN managed INTEGER NOT NULL DEFAULT 0`},
		{"add_sessions_permission_mode", `ALTER TABLE sessions ADD COLUMN permission_mode TEXT`},
		// Lower sorts first, and it goes negative: a new session takes one less
		// than the smallest, which puts it on top without renumbering the rest.
		{"add_sessions_sort_order", `ALTER TABLE sessions ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`},
		{"drop_sessions_managed", `ALTER TABLE sessions DROP COLUMN managed`},
		// When a human last looked at the session, as opposed to when its agent
		// last did something. Eviction needs the first, and last_event_at only
		// answers the second.
		{"add_sessions_last_interacted_at", `ALTER TABLE sessions ADD COLUMN last_interacted_at TEXT`},
		{"drop_sessions_archived", `ALTER TABLE sessions DROP COLUMN archived`},
		{"create_groups_table", `CREATE TABLE IF NOT EXISTS groups (
			key      TEXT PRIMARY KEY,
			name     TEXT NOT NULL,
			position INTEGER NOT NULL
		)`},
		// A JSON array of group keys, outermost first: '["g_work","g_opal"]'.
		// An array rather than fixed columns because the depth limit belongs to
		// the picker, not to the database: a fourth level should not cost a
		// migration on the busiest table.
		{"add_sessions_groups", `ALTER TABLE sessions ADD COLUMN groups TEXT`},
	}

	for _, cm := range columnMigrations {
		var exists int
		s.db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE id = ?`, cm.id).Scan(&exists)
		if exists > 0 {
			continue
		}
		// Ignore error — column may already exist from fresh schema
		s.db.Exec(cm.sql)
		s.db.Exec(`INSERT OR IGNORE INTO _migrations (id) VALUES (?)`, cm.id)
	}

	return nil
}

// promoteArchivedToTerminated files every archived session as terminated, which
// is what archiving always meant. It does nothing once the column is gone, and
// running it twice changes nothing the first run did not already change.
func (s *Store) promoteArchivedToTerminated() error {
	has, err := s.hasColumn("sessions", "archived")
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	_, err = s.db.Exec(`UPDATE sessions
		   SET status = 'terminated',
		       ended_at = COALESCE(ended_at, datetime('now'))
		 WHERE archived = 1`)
	return err
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("read %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("scan %s column: %w", table, err)
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}
