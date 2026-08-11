package server

import (
	"testing"
	"time"
)

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
