package store

import (
	"testing"
)

func seedNotification(t *testing.T, s *Store, id, session, nType, status string) {
	t.Helper()
	n := &Notification{
		ID:            id,
		Source:        "claude",
		SourceSession: session,
		CWD:           "/tmp/test",
		Type:          nType,
		Status:        status,
	}
	if err := s.CreateNotification(n); err != nil {
		t.Fatalf("create notification %s: %v", id, err)
	}
}

func statusOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	n, err := s.GetNotification(id)
	if err != nil {
		t.Fatalf("get notification %s: %v", id, err)
	}
	return n.Status
}

// Answering a question must not also clear a permission the same session is
// still blocked on — that is the whole reason this exists rather than reusing
// ResolveSessionNotifications.
func TestResolveSessionNotificationsByType_LeavesOtherTypes(t *testing.T) {
	s := setupTestStore(t)
	seedNotification(t, s, "n-question", "sess-1", "claude.question", "pending")
	seedNotification(t, s, "n-permission", "sess-1", "claude.permission", "pending")

	ids, err := s.ResolveSessionNotificationsByType("sess-1", "claude.question", "resolved", "claude")
	if err != nil {
		t.Fatalf("resolve by type: %v", err)
	}
	if len(ids) != 1 || ids[0] != "n-question" {
		t.Fatalf("ids = %v, want [n-question]", ids)
	}
	if got := statusOf(t, s, "n-question"); got != "resolved" {
		t.Errorf("question status = %q, want resolved", got)
	}
	if got := statusOf(t, s, "n-permission"); got != "pending" {
		t.Errorf("permission status = %q, want pending", got)
	}
}

func TestResolveSessionNotificationsByType_LeavesOtherSessions(t *testing.T) {
	s := setupTestStore(t)
	seedNotification(t, s, "n-mine", "sess-1", "claude.question", "pending")
	seedNotification(t, s, "n-theirs", "sess-2", "claude.question", "pending")

	if _, err := s.ResolveSessionNotificationsByType("sess-1", "claude.question", "resolved", "claude"); err != nil {
		t.Fatalf("resolve by type: %v", err)
	}
	if got := statusOf(t, s, "n-theirs"); got != "pending" {
		t.Errorf("other session status = %q, want pending", got)
	}
}

// The PostToolUse and idle_prompt paths both call this, so a question that is
// already answered goes through it a second time.
func TestResolveSessionNotificationsByType_NoPendingIsNotAnError(t *testing.T) {
	s := setupTestStore(t)
	seedNotification(t, s, "n-done", "sess-1", "claude.question", "resolved")

	ids, err := s.ResolveSessionNotificationsByType("sess-1", "claude.question", "resolved", "claude")
	if err != nil {
		t.Fatalf("resolve by type: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want none", ids)
	}
}
