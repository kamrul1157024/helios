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
