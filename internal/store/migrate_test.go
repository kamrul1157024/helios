package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The sessions table as it stood while archived was still a flag of its own.
const legacySessionsSchema = `CREATE TABLE sessions (
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
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	ended_at TEXT
)`

// writeLegacyDB lays down a database as an older Helios left it, so Open has a
// real upgrade to perform rather than a fresh schema to create.
func writeLegacyDB(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(legacySessionsSchema); err != nil {
		t.Fatalf("create legacy sessions: %v", err)
	}
	for _, row := range rows {
		if _, err := db.Exec(row); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}
	return path
}

func TestMigrate_ArchivedBecomesTerminated(t *testing.T) {
	path := writeLegacyDB(t,
		`INSERT INTO sessions (session_id, cwd, project, status, archived) VALUES ('put-away', '/tmp/a', 'a', 'idle', 1)`,
		`INSERT INTO sessions (session_id, cwd, project, status, archived) VALUES ('working', '/tmp/b', 'b', 'idle', 0)`,
	)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	away, err := s.GetSession("put-away")
	if err != nil {
		t.Fatalf("get put-away: %v", err)
	}
	if away.Status != "terminated" {
		t.Errorf("archived session status = %q, want terminated", away.Status)
	}
	// Without a stamp the session is terminated but claims never to have ended,
	// and every "ended when?" in the UI reads blank.
	if away.EndedAt == nil || *away.EndedAt == "" {
		t.Error("archived session kept no ended_at")
	}

	working, err := s.GetSession("working")
	if err != nil {
		t.Fatalf("get working: %v", err)
	}
	if working.Status != "idle" {
		t.Errorf("unarchived session status = %q, want idle", working.Status)
	}
}

// A session terminated before it was archived already knows when it ended, and
// the migration must not restamp it as ending today.
func TestMigrate_KeepsAnExistingEndedAt(t *testing.T) {
	path := writeLegacyDB(t,
		`INSERT INTO sessions (session_id, cwd, project, status, archived, ended_at)
		 VALUES ('old', '/tmp/a', 'a', 'terminated', 1, '2020-01-01T00:00:00Z')`,
	)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sess, err := s.GetSession("old")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.EndedAt == nil || *sess.EndedAt != "2020-01-01T00:00:00Z" {
		t.Errorf("ended_at = %v, want it untouched", sess.EndedAt)
	}
}

func TestMigrate_DropsTheArchivedColumn(t *testing.T) {
	path := writeLegacyDB(t,
		`INSERT INTO sessions (session_id, cwd, project, status, archived) VALUES ('put-away', '/tmp/a', 'a', 'idle', 1)`,
	)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	has, err := s.hasColumn("sessions", "archived")
	if err != nil {
		t.Fatalf("has column: %v", err)
	}
	if has {
		t.Error("archived column survived the migration")
	}
}

// Opening the same database twice is the ordinary case — every daemon restart
// does it — and the second pass has no column left to read.
func TestMigrate_IsSafeToRunTwice(t *testing.T) {
	path := writeLegacyDB(t,
		`INSERT INTO sessions (session_id, cwd, project, status, archived) VALUES ('put-away', '/tmp/a', 'a', 'idle', 1)`,
	)

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()

	sess, err := second.GetSession("put-away")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.Status != "terminated" {
		t.Errorf("status = %q, want terminated", sess.Status)
	}
}

// A fresh database never had the column, and the migration must not object.
func TestMigrate_FreshDatabaseHasNoArchivedColumn(t *testing.T) {
	s := setupTestStore(t)

	has, err := s.hasColumn("sessions", "archived")
	if err != nil {
		t.Fatalf("has column: %v", err)
	}
	if has {
		t.Error("fresh schema still declares archived")
	}
}
