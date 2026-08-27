package store

import (
	"testing"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func strPtr(s string) *string { return &s }

func TestUpsertSession_Basic(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{
		SessionID: "test-session-1",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "starting",
		LastEvent: strPtr("Wrap"),
	}

	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.GetSession("test-session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "starting" {
		t.Errorf("status = %q, want %q", got.Status, "starting")
	}
}

func TestUpsertSession_UpdatesStatus(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{
		SessionID: "test-session-2",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "starting",
		LastEvent: strPtr("Wrap"),
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert wrap: %v", err)
	}

	sess2 := &Session{
		SessionID:      "test-session-2",
		Source:         "claude",
		CWD:            "/tmp/test",
		TranscriptPath: strPtr("/path/to/transcript.jsonl"),
		Model:          strPtr("opus"),
		Status:         "idle",
		LastEvent:      strPtr("SessionStart"),
	}
	if err := s.UpsertSession(sess2); err != nil {
		t.Fatalf("upsert hook: %v", err)
	}

	got, err := s.GetSession("test-session-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "idle" {
		t.Errorf("status = %q, want %q", got.Status, "idle")
	}
	if got.Model == nil || *got.Model != "opus" {
		t.Errorf("model = %v, want opus", got.Model)
	}
}

// TestPermissionModeRoundTrip covers the column added for mode switching. It
// must survive both read paths: the daemon reads it via GetSession when waking
// a session, and clients see it in the list.
func TestPermissionModeRoundTrip(t *testing.T) {
	db := setupTestStore(t)

	if err := db.UpsertSession(&Session{
		SessionID: "s1", Source: "claude", CWD: "/tmp", Status: "idle",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sess, err := db.GetSession("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.PermissionMode != nil {
		t.Errorf("PermissionMode = %v, want nil before anything sets it", *sess.PermissionMode)
	}

	if err := db.UpdateSessionPermissionMode("s1", "plan"); err != nil {
		t.Fatalf("update mode: %v", err)
	}

	sess, err = db.GetSession("s1")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if sess.PermissionMode == nil || *sess.PermissionMode != "plan" {
		t.Fatalf("PermissionMode = %v, want plan", sess.PermissionMode)
	}

	list, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].PermissionMode == nil || *list[0].PermissionMode != "plan" {
		t.Errorf("ListSessions dropped the mode: %+v", list)
	}
}

// TestPermissionModeSurvivesUpsert pins that a later hook, which upserts the
// session without knowing the mode, does not blank it: the daemon reads this
// column on every wake.
func TestPermissionModeSurvivesUpsert(t *testing.T) {
	db := setupTestStore(t)

	if err := db.UpsertSession(&Session{
		SessionID: "s1", Source: "claude", CWD: "/tmp", Status: "idle",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.UpdateSessionPermissionMode("s1", "acceptEdits"); err != nil {
		t.Fatalf("update mode: %v", err)
	}
	if err := db.UpsertSession(&Session{
		SessionID: "s1", Source: "claude", CWD: "/tmp", Status: "active",
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	sess, err := db.GetSession("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.PermissionMode == nil || *sess.PermissionMode != "acceptEdits" {
		t.Errorf("PermissionMode = %v, want acceptEdits to survive the upsert", sess.PermissionMode)
	}
}

// Reading the count must not spend one. An attempt is the session's budget for
// ever being named, and a timed-out model call used to cost it the same as a
// real answer — five slow minutes and the session could never be titled.
func TestAutoTitleAttempts_ReadingDoesNotSpend(t *testing.T) {
	s := setupTestStore(t)
	if err := s.UpsertSession(&Session{SessionID: "sess-1", Source: "claude", CWD: "/tmp", Status: "idle"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for range 3 {
		spent, err := s.AutoTitleAttempts("sess-1")
		if err != nil {
			t.Fatalf("read attempts: %v", err)
		}
		if spent != 0 {
			t.Fatalf("reading changed the count: got %d, want 0", spent)
		}
	}

	if _, err := s.IncrementAutoTitleAttempts("sess-1"); err != nil {
		t.Fatalf("increment: %v", err)
	}
	spent, err := s.AutoTitleAttempts("sess-1")
	if err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if spent != 1 {
		t.Errorf("after one increment: got %d, want 1", spent)
	}
}

// A hand-arranged order is written whole, and numbering starts at zero.
func TestSetSessionOrder_WritesTheArrangement(t *testing.T) {
	s := setupTestStore(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.UpsertSession(&Session{SessionID: id, Source: "claude", CWD: "/tmp", Status: "idle"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	if err := s.SetSessionOrder([]string{"c", "a", "b"}); err != nil {
		t.Fatalf("set order: %v", err)
	}

	for want, id := range []string{"c", "a", "b"} {
		sess, err := s.GetSession(id)
		if err != nil || sess == nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if sess.SortOrder != want {
			t.Errorf("%s: sort_order %d, want %d", id, sess.SortOrder, want)
		}
	}
}

// A session created after an arrangement belongs on top, without renumbering
// everything that was already placed.
func TestUpsertSession_NewSessionSortsAboveTheArrangement(t *testing.T) {
	s := setupTestStore(t)
	for _, id := range []string{"a", "b"} {
		if err := s.UpsertSession(&Session{SessionID: id, Source: "claude", CWD: "/tmp", Status: "idle"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := s.SetSessionOrder([]string{"a", "b"}); err != nil {
		t.Fatalf("set order: %v", err)
	}

	if err := s.UpsertSession(&Session{SessionID: "fresh", Source: "claude", CWD: "/tmp", Status: "idle"}); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	fresh, _ := s.GetSession("fresh")
	first, _ := s.GetSession("a")
	if fresh.SortOrder >= first.SortOrder {
		t.Errorf("new session sorts at %d, not above the arranged %d", fresh.SortOrder, first.SortOrder)
	}

	// And the arrangement it landed above is untouched.
	second, _ := s.GetSession("b")
	if first.SortOrder != 0 || second.SortOrder != 1 {
		t.Errorf("arrangement moved: a=%d b=%d, want 0 and 1", first.SortOrder, second.SortOrder)
	}
}

// An update to an existing session must not shuffle it back to the top.
func TestUpsertSession_UpdateKeepsItsPlace(t *testing.T) {
	s := setupTestStore(t)
	if err := s.UpsertSession(&Session{SessionID: "a", Source: "claude", CWD: "/tmp", Status: "idle"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.SetSessionOrder([]string{"a"}); err != nil {
		t.Fatalf("set order: %v", err)
	}

	if err := s.UpsertSession(&Session{SessionID: "a", Source: "claude", CWD: "/tmp", Status: "active"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	sess, _ := s.GetSession("a")
	if sess.SortOrder != 0 {
		t.Errorf("an update moved it to %d, want 0", sess.SortOrder)
	}
}

// last_interacted_at answers "is anyone still interested", which last_event_at
// cannot: the agent moves that one while nobody is watching.
func TestTouchSessionIsSeparateFromAgentActivity(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{SessionID: "s1", Source: "claude", CWD: "/tmp", Status: "idle"}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, _ := s.GetSession("s1")
	if got.LastInteractedAt != nil {
		t.Fatalf("a session nobody opened has been interacted with: %v", *got.LastInteractedAt)
	}

	if err := s.TouchSession("s1"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _ = s.GetSession("s1")
	if got.LastInteractedAt == nil {
		t.Fatal("touch did not record anything")
	}

	// Agent activity must not count as a human looking.
	touched := *got.LastInteractedAt
	if err := s.UpdateSessionStatus("s1", "active", "PreToolUse:Read"); err != nil {
		t.Fatalf("status: %v", err)
	}
	got, _ = s.GetSession("s1")
	if *got.LastInteractedAt != touched {
		t.Error("agent activity moved last_interacted_at")
	}
}

// Claude Code moves a transcript when the session's cwd moves, so the path a
// session was registered with is not the last word on where its transcript is.
func TestUpdateSessionTranscriptPath_FollowsAMovedTranscript(t *testing.T) {
	s := setupTestStore(t)
	if err := s.UpsertSession(&Session{
		SessionID:      "a",
		Source:         "claude",
		CWD:            "/tmp/repo",
		Status:         "idle",
		TranscriptPath: strPtr("/projects/-tmp-repo/a.jsonl"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	moved := "/projects/-tmp-repo--worktrees-feature/a.jsonl"
	if err := s.UpdateSessionTranscriptPath("a", moved); err != nil {
		t.Fatalf("update: %v", err)
	}

	sess, _ := s.GetSession("a")
	if sess.TranscriptPath == nil || *sess.TranscriptPath != moved {
		t.Errorf("transcript path = %v, want %q", sess.TranscriptPath, moved)
	}
}
