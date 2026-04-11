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
			archived INTEGER NOT NULL DEFAULT 0,
			tmux_pane TEXT,
			tmux_pid INTEGER,
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
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
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
