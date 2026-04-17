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

func TestUpdateSessionManaged(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{
		SessionID: "toggle-session-1",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "idle",
		LastEvent: strPtr("Discovered"),
		Managed:   false,
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := s.UpdateSessionManaged("toggle-session-1", true); err != nil {
		t.Fatalf("update managed: %v", err)
	}

	got, err := s.GetSession("toggle-session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Managed {
		t.Error("managed = false after update, want true")
	}
}

func TestListManagedOrphanedSessions_ExcludesTerminated(t *testing.T) {
	s := setupTestStore(t)

	active := &Session{SessionID: "m-active", Source: "claude", CWD: "/a", Status: "active", Managed: true, LastEvent: strPtr("x")}
	terminated := &Session{SessionID: "m-term", Source: "claude", CWD: "/b", Status: "terminated", Managed: true, LastEvent: strPtr("x")}
	for _, sess := range []*Session{active, terminated} {
		if err := s.UpsertSession(sess); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	result, err := s.ListManagedOrphanedSessions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range result {
		if r.SessionID == "m-term" {
			t.Error("terminated session should not appear in managed orphaned list")
		}
	}
	found := false
	for _, r := range result {
		if r.SessionID == "m-active" {
			found = true
		}
	}
	if !found {
		t.Error("active managed session should appear in managed orphaned list")
	}
}

func TestListManagedOrphanedSessions_ExcludesUnmanaged(t *testing.T) {
	s := setupTestStore(t)

	managed := &Session{SessionID: "m-yes", Source: "claude", CWD: "/a", Status: "idle", Managed: true, LastEvent: strPtr("x")}
	unmanaged := &Session{SessionID: "m-no", Source: "claude", CWD: "/b", Status: "idle", Managed: false, LastEvent: strPtr("x")}
	for _, sess := range []*Session{managed, unmanaged} {
		if err := s.UpsertSession(sess); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	result, err := s.ListManagedOrphanedSessions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range result {
		if r.SessionID == "m-no" {
			t.Error("unmanaged session should not appear in managed orphaned list")
		}
	}
}

func TestListManagedOrphanedSessions_AllActiveStatuses(t *testing.T) {
	s := setupTestStore(t)

	statuses := []string{"starting", "active", "waiting_permission", "compacting", "idle"}
	for i, status := range statuses {
		sess := &Session{
			SessionID: "m-status-" + status,
			Source:    "claude",
			CWD:       "/tmp/" + status,
			Status:    status,
			Managed:   true,
			LastEvent: strPtr("x"),
		}
		_ = i
		if err := s.UpsertSession(sess); err != nil {
			t.Fatalf("upsert %s: %v", status, err)
		}
	}

	result, err := s.ListManagedOrphanedSessions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := make(map[string]bool)
	for _, r := range result {
		found[r.Status] = true
	}
	for _, status := range statuses {
		if !found[status] {
			t.Errorf("status %q not found in managed orphaned list", status)
		}
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
