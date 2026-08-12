package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// The trust dialog blocks a brand new session until someone answers it, so its
// notification is the one surface a client has. Recording the session only in
// the payload left source_session empty, and every session-scoped sweep —
// Stop, terminate, delete — keys on that column. The row therefore stayed
// pending forever: a permanent approval on every tray and phone for a session
// that had long since finished or been deleted, with nothing left to answer it.
func TestCreateTrustNotification_RecordsItsSession(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	shared := NewShared(db, notifications.NewManager(db), newStubBackend())
	p := &PendingSession{SessionID: "sess-trust", CWD: "/tmp/untrusted"}
	createTrustNotification(shared, p)

	notifs, err := db.ListNotifications("claude", "", "claude.trust")
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("got %d trust notifications, want 1", len(notifs))
	}
	notif := notifs[0]

	if notif.SourceSession != "sess-trust" {
		t.Errorf("source_session = %q, want sess-trust", notif.SourceSession)
	}
	if notif.CWD != "/tmp/untrusted" {
		t.Errorf("cwd = %q, want /tmp/untrusted", notif.CWD)
	}

	// The action handler reads the session from the payload, so both carry it.
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if notif.Payload == nil {
		t.Fatal("notification has no payload")
	}
	if err := json.Unmarshal([]byte(*notif.Payload), &payload); err != nil {
		t.Fatalf("decode payload %q: %v", *notif.Payload, err)
	}
	if payload.SessionID != "sess-trust" {
		t.Errorf("payload session_id = %q, want sess-trust", payload.SessionID)
	}
}

// The consequence the column exists for: once the session reports in or ends,
// the sweep must be able to find and retract the dialog.
func TestTrustNotification_IsResolvedByItsSession(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mgr := notifications.NewManager(db)
	shared := NewShared(db, mgr, newStubBackend())
	createTrustNotification(shared, &PendingSession{SessionID: "sess-trust", CWD: "/tmp/untrusted"})

	ids, err := mgr.ResolveSession("sess-trust", "resolved", "claude")
	if err != nil {
		t.Fatalf("resolve session notifications: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("resolved %d notifications, want 1 — the trust dialog would stay pending forever", len(ids))
	}

	notifs, err := db.ListNotifications("claude", "pending", "claude.trust")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(notifs) != 0 {
		t.Errorf("%d trust notifications still pending after the session resolved", len(notifs))
	}
}

func TestPendingSessionMapAddRemove(t *testing.T) {
	m := NewPendingSessionMap()
	m.Add("sess-1", "/tmp/one")
	m.Add("sess-2", "/tmp/two")

	if got := len(m.List()); got != 2 {
		t.Fatalf("List: got %d entries, want 2", got)
	}
	p, ok := m.Get("sess-1")
	if !ok {
		t.Fatal("sess-1 should be pending")
	}
	if p.CWD != "/tmp/one" {
		t.Fatalf("cwd: got %q want /tmp/one", p.CWD)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be stamped")
	}

	m.Remove("sess-1")
	if _, ok := m.Get("sess-1"); ok {
		t.Fatal("sess-1 should be gone")
	}
	if _, ok := m.Get("sess-2"); !ok {
		t.Fatal("sess-2 should still be pending")
	}
}

func TestPendingSessionMapMarkNotifSent(t *testing.T) {
	m := NewPendingSessionMap()
	m.Add("sess", "/tmp")

	if p, _ := m.Get("sess"); p.NotifSent {
		t.Fatal("NotifSent should start false")
	}
	m.MarkNotifSent("sess")
	if p, _ := m.Get("sess"); !p.NotifSent {
		t.Fatal("NotifSent should be true after marking")
	}
	// Marking an unknown session must not panic or create an entry.
	m.MarkNotifSent("nope")
	if _, ok := m.Get("nope"); ok {
		t.Fatal("marking should not create entries")
	}
}

func TestPendingSessionMapListIsASnapshot(t *testing.T) {
	m := NewPendingSessionMap()
	m.Add("sess", "/tmp")
	list := m.List()
	list[0].CWD = "/mutated"
	if p, _ := m.Get("sess"); p.CWD != "/tmp" {
		t.Fatalf("List must copy; stored cwd became %q", p.CWD)
	}
}

func TestContainsTrustPrompt(t *testing.T) {
	// Rendered screen text, as the emulator would produce it.
	yes := []string{
		"Do you trust the files in this folder?",
		"❯ 1. Yes, I trust this folder",
		"  Quick safety check",
		"only run Claude Code in a directory that is one you trust",
	}
	for _, s := range yes {
		if !containsTrustPrompt(s) {
			t.Fatalf("should detect trust prompt in %q", s)
		}
	}

	no := []string{
		"",
		"Welcome to Claude Code",
		"$ git status",
		"trusted advisor",
	}
	for _, s := range no {
		if containsTrustPrompt(s) {
			t.Fatalf("should not detect trust prompt in %q", s)
		}
	}
}

func TestPendingTTLIsSane(t *testing.T) {
	// The watcher expires entries against this; a zero or negative value would
	// drop every session before its dialog could appear.
	if pendingTTL < time.Minute {
		t.Fatalf("pendingTTL too short: %v", pendingTTL)
	}
}
