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

func TestUpsertSession_WithTmuxPane(t *testing.T) {
	s := setupTestStore(t)

	paneID := "%5"
	sess := &Session{
		SessionID: "test-session-1",
		Source:    "claude",
		CWD:       "/tmp/test",
		TmuxPane:  &paneID,
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
	if got.TmuxPane == nil || *got.TmuxPane != "%5" {
		t.Errorf("tmux_pane = %v, want %%5", got.TmuxPane)
	}
	if got.Status != "starting" {
		t.Errorf("status = %q, want %q", got.Status, "starting")
	}
}

func TestUpsertSession_DoesNotOverwriteTmuxPane(t *testing.T) {
	s := setupTestStore(t)

	// Step 1: Wrap writes session with pane.
	paneID := "%10"
	sess := &Session{
		SessionID: "test-session-2",
		Source:    "claude",
		CWD:       "/tmp/test",
		TmuxPane:  &paneID,
		Status:    "starting",
		LastEvent: strPtr("Wrap"),
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert wrap: %v", err)
	}

	// Step 2: SessionStart hook upserts same session WITHOUT pane.
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

	// Pane should still be %10 (not overwritten to NULL).
	got, err := s.GetSession("test-session-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TmuxPane == nil || *got.TmuxPane != "%10" {
		t.Errorf("tmux_pane = %v, want %%10 (should not be overwritten)", got.TmuxPane)
	}
	if got.Status != "idle" {
		t.Errorf("status = %q, want %q", got.Status, "idle")
	}
	if got.Model == nil || *got.Model != "opus" {
		t.Errorf("model = %v, want opus", got.Model)
	}
}

func TestUpsertSession_SetsPane_WhenNoPreviousPane(t *testing.T) {
	s := setupTestStore(t)

	// Step 1: Session created without pane (e.g., discovered or non-wrap).
	sess := &Session{
		SessionID: "test-session-3",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "idle",
		LastEvent: strPtr("SessionStart"),
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Step 2: Second upsert provides pane.
	paneID := "%20"
	sess2 := &Session{
		SessionID: "test-session-3",
		Source:    "claude",
		CWD:       "/tmp/test",
		TmuxPane:  &paneID,
		Status:    "idle",
		LastEvent: strPtr("Wrap"),
	}
	if err := s.UpsertSession(sess2); err != nil {
		t.Fatalf("upsert with pane: %v", err)
	}

	got, err := s.GetSession("test-session-3")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TmuxPane == nil || *got.TmuxPane != "%20" {
		t.Errorf("tmux_pane = %v, want %%20", got.TmuxPane)
	}
}

func TestUpdateSessionTmuxPane(t *testing.T) {
	s := setupTestStore(t)

	sess := &Session{
		SessionID: "test-session-4",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "idle",
		LastEvent: strPtr("SessionStart"),
	}
	if err := s.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := s.UpdateSessionTmuxPane("test-session-4", "%15", 12345); err != nil {
		t.Fatalf("update pane: %v", err)
	}

	got, err := s.GetSession("test-session-4")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TmuxPane == nil || *got.TmuxPane != "%15" {
		t.Errorf("tmux_pane = %v, want %%15", got.TmuxPane)
	}
	if got.TmuxPID == nil || *got.TmuxPID != 12345 {
		t.Errorf("tmux_pid = %v, want 12345", got.TmuxPID)
	}
}
