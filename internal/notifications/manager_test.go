package notifications

import (
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewManager(db)
}

// seedNotification inserts a pending notification and returns its ID.
func seedNotification(t *testing.T, m *Manager, id string) string {
	t.Helper()
	if err := m.CreateNotification(&store.Notification{
		ID:            id,
		Source:        "claude",
		SourceSession: "sess-1",
		Type:          "claude.permission",
		Status:        "pending",
	}); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	return id
}

// A client can answer before the hook handler reaches WaitForDecision. The
// decision has to survive that gap instead of leaving the handler blocked.
func TestResolveBeforeWait_DeliversDecision(t *testing.T) {
	m := newTestManager(t)
	id := seedNotification(t, m, "notif-early")

	m.Register(id)
	if err := m.Resolve(id, Decision{Status: "approved"}, "mobile"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got := waitWithTimeout(t, m, id)
	if got.Status != "approved" {
		t.Errorf("status = %q, want approved", got.Status)
	}
}

func TestResolveAfterWait_DeliversDecision(t *testing.T) {
	m := newTestManager(t)
	id := seedNotification(t, m, "notif-late")

	m.Register(id)
	decisions := make(chan Decision, 1)
	go func() {
		d, err := m.WaitForDecision(id)
		if err != nil {
			t.Errorf("wait: %v", err)
		}
		decisions <- d
	}()

	// Give the waiter a moment to block, then resolve.
	for !m.HasPending(id) {
		time.Sleep(time.Millisecond)
	}
	if err := m.Resolve(id, Decision{Status: "denied"}, "mobile"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	select {
	case d := <-decisions:
		if d.Status != "denied" {
			t.Errorf("status = %q, want denied", d.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for decision")
	}
}

// Two clients answering the same notification must not wedge the second one.
func TestDoubleResolve_DoesNotBlock(t *testing.T) {
	m := newTestManager(t)
	id := seedNotification(t, m, "notif-twice")

	m.Register(id)
	if err := m.Resolve(id, Decision{Status: "approved"}, "mobile"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The store rejects the second resolve; the manager must return rather
		// than block on the full decision channel.
		m.Resolve(id, Decision{Status: "denied"}, "tui")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("second Resolve blocked")
	}

	if got := waitWithTimeout(t, m, id); got.Status != "approved" {
		t.Errorf("status = %q, want approved (first decision wins)", got.Status)
	}
}

// Cancelling a registered notification unblocks the waiter as dismissed.
func TestCancelPending_Dismisses(t *testing.T) {
	m := newTestManager(t)
	id := seedNotification(t, m, "notif-cancel")

	m.Register(id)
	m.CancelPending(id)

	if got := waitWithTimeout(t, m, id); got.Status != "dismissed" {
		t.Errorf("status = %q, want dismissed", got.Status)
	}
}

func waitWithTimeout(t *testing.T, m *Manager, id string) Decision {
	t.Helper()
	type result struct {
		d   Decision
		err error
	}
	results := make(chan result, 1)
	go func() {
		d, err := m.WaitForDecision(id)
		results <- result{d, err}
	}()
	select {
	case r := <-results:
		if r.err != nil {
			t.Fatalf("wait: %v", r.err)
		}
		return r.d
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for decision")
		return Decision{}
	}
}
