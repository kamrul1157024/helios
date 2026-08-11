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

func TestManagedFlag_DefaultFalse(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{
		SessionID: "disc-session-1",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "terminated",
		LastEvent: strPtr("Discovered"),
	}
	if err := s.InsertDiscoveredSession(sess); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.GetSession("disc-session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Managed {
		t.Error("managed = true, want false for discovered session")
	}
}

func TestManagedFlag_SetOnUpsert(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{
		SessionID: "managed-session-1",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "starting",
		LastEvent: strPtr("Launch"),
		Managed:   true,
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.GetSession("managed-session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Managed {
		t.Error("managed = false, want true")
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
