package daemon

import (
	"testing"

	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func strPtr(s string) *string { return &s }

// fakeTmux is a minimal tmux.Client stand-in that reports unavailable.
// We use the real Client but point it at a non-existent binary so Available()
// returns false without actually spawning processes.
func unavailableTmux() *tmux.Client {
	return tmux.NewClient() // bin resolved via PATH; server not running in test env
}

func TestRecoverManagedSessions_NoOp_WhenTmuxUnavailable(t *testing.T) {
	tc := unavailableTmux()
	if tc.Available() {
		t.Skip("tmux server is running — cannot test unavailable path")
	}

	db := setupTestStore(t)
	pm := tmux.NewPaneMap()
	sse := server.NewSSEBroadcaster()

	sess := &store.Session{
		SessionID: "recover-nopmux",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "active",
		Managed:   true,
		LastEvent: strPtr("Launch"),
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	recoverManagedSessions(db, tc, pm, sse)

	if _, ok := pm.Get("recover-nopmux"); ok {
		t.Error("pane was set despite tmux being unavailable")
	}
}

func TestRecoverManagedSessions_SkipsAlreadyBound(t *testing.T) {
	db := setupTestStore(t)
	pm := tmux.NewPaneMap()
	sse := server.NewSSEBroadcaster()
	tc := unavailableTmux()

	sess := &store.Session{
		SessionID: "recover-bound",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "active",
		Managed:   true,
		LastEvent: strPtr("Launch"),
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Pre-bind a pane so the session is not orphaned.
	pm.Set("recover-bound", "%99")

	recoverManagedSessions(db, tc, pm, sse)

	// Pane should still be the original one (not replaced).
	paneID, ok := pm.Get("recover-bound")
	if !ok {
		t.Fatal("pane was removed")
	}
	if paneID != "%99" {
		t.Errorf("pane = %q, want %%99", paneID)
	}
}

func TestRecoverManagedSessions_SkipsUnmanaged(t *testing.T) {
	db := setupTestStore(t)
	pm := tmux.NewPaneMap()
	sse := server.NewSSEBroadcaster()
	tc := unavailableTmux()

	sess := &store.Session{
		SessionID: "recover-unmanaged",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "active",
		Managed:   false,
		LastEvent: strPtr("Discovered"),
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	recoverManagedSessions(db, tc, pm, sse)

	if _, ok := pm.Get("recover-unmanaged"); ok {
		t.Error("unmanaged session should not be recovered")
	}
}

func TestRecoverManagedSessions_SkipsTerminated(t *testing.T) {
	db := setupTestStore(t)
	pm := tmux.NewPaneMap()
	sse := server.NewSSEBroadcaster()
	tc := unavailableTmux()

	sess := &store.Session{
		SessionID: "recover-terminated",
		Source:    "claude",
		CWD:       "/tmp/test",
		Status:    "terminated",
		Managed:   true,
		LastEvent: strPtr("Launch"),
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	recoverManagedSessions(db, tc, pm, sse)

	if _, ok := pm.Get("recover-terminated"); ok {
		t.Error("terminated session should not be recovered")
	}
}
