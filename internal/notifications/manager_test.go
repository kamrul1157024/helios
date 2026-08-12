package notifications

import (
	"errors"
	"sync"
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

// ─── Announcements ─────────────────────────────────────────────────────────
//
// Clients are a view of the store, so nothing may change a notification's
// status without saying so. A resolution nobody hears about leaves a stale
// approval on every phone and tray until the client next reconnects.

type announcement struct {
	eventType string
	id        string
	action    string
	source    string
}

// recordAnnouncements installs a broadcaster and returns the events it saw.
func recordAnnouncements(m *Manager) *[]announcement {
	var seen []announcement
	var mu sync.Mutex
	m.SetBroadcaster(func(eventType string, data interface{}) {
		fields, _ := data.(map[string]string)
		mu.Lock()
		seen = append(seen, announcement{eventType, fields["id"], fields["action"], fields["source"]})
		mu.Unlock()
	})
	return &seen
}

func TestResolve_Announces(t *testing.T) {
	m := newTestManager(t)
	seen := recordAnnouncements(m)
	id := seedNotification(t, m, "notif-announce")

	if err := m.Resolve(id, Decision{Status: "approved"}, "device:abc"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(*seen) != 1 {
		t.Fatalf("announcements = %d, want 1", len(*seen))
	}
	want := announcement{"notification_resolved", id, "approved", "device:abc"}
	if (*seen)[0] != want {
		t.Errorf("announced %+v, want %+v", (*seen)[0], want)
	}
}

// The row is already resolved, so nothing changed and nothing is announced —
// otherwise two surfaces answering at once would retract it twice.
func TestResolve_AlreadyResolvedStaysQuiet(t *testing.T) {
	m := newTestManager(t)
	id := seedNotification(t, m, "notif-twice")
	if err := m.Resolve(id, Decision{Status: "approved"}, "browser"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	seen := recordAnnouncements(m)
	if err := m.Resolve(id, Decision{Status: "denied"}, "device:abc"); !errors.Is(err, store.ErrAlreadyResolved) {
		t.Fatalf("second resolve err = %v, want ErrAlreadyResolved", err)
	}
	if len(*seen) != 0 {
		t.Errorf("announcements = %+v, want none", *seen)
	}
}

// The regression this whole change exists for: a hook that times out server
// side marked the notification resolved and told no one, so it sat on the
// phone forever.
func TestCancelPending_Announces(t *testing.T) {
	m := newTestManager(t)
	seen := recordAnnouncements(m)
	id := seedNotification(t, m, "notif-timeout")

	m.Register(id)
	m.CancelPending(id)

	if len(*seen) != 1 {
		t.Fatalf("announcements = %d, want 1 (timeout must reach clients)", len(*seen))
	}
	want := announcement{"notification_resolved", id, "timeout", "system"}
	if (*seen)[0] != want {
		t.Errorf("announced %+v, want %+v", (*seen)[0], want)
	}
}

func TestCancelPendingFromClaude_Announces(t *testing.T) {
	m := newTestManager(t)
	seen := recordAnnouncements(m)
	id := seedNotification(t, m, "notif-claude")

	m.CancelPendingFromClaude(id)

	if len(*seen) != 1 {
		t.Fatalf("announcements = %d, want 1", len(*seen))
	}
	if (*seen)[0].action != "resolved" || (*seen)[0].source != "claude" {
		t.Errorf("announced %+v, want resolved/claude", (*seen)[0])
	}
}

func TestResolveSession_AnnouncesEveryNotification(t *testing.T) {
	m := newTestManager(t)
	seedNotification(t, m, "notif-a")
	seedNotification(t, m, "notif-b")
	seen := recordAnnouncements(m)

	ids, err := m.ResolveSession("sess-1", "resolved", "claude")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("resolved %d, want 2", len(ids))
	}
	if len(*seen) != 2 {
		t.Fatalf("announcements = %d, want 2", len(*seen))
	}
	for _, a := range *seen {
		if a.action != "resolved" || a.source != "claude" {
			t.Errorf("announced %+v, want resolved/claude", a)
		}
	}
}

// Narrowed by type so answering a question does not also retract a permission
// the same session is still blocked on.
func TestResolveSessionByType_LeavesOtherTypes(t *testing.T) {
	m := newTestManager(t)
	seedNotification(t, m, "notif-perm") // claude.permission
	if err := m.CreateNotification(&store.Notification{
		ID: "notif-q", Source: "claude", SourceSession: "sess-1",
		Type: "claude.question", Status: "pending",
	}); err != nil {
		t.Fatalf("create question: %v", err)
	}
	seen := recordAnnouncements(m)

	ids, err := m.ResolveSessionByType("sess-1", "claude.question", "resolved", "claude")
	if err != nil {
		t.Fatalf("resolve by type: %v", err)
	}
	if len(ids) != 1 || ids[0] != "notif-q" {
		t.Fatalf("resolved %v, want [notif-q]", ids)
	}
	if len(*seen) != 1 || (*seen)[0].id != "notif-q" {
		t.Fatalf("announcements = %+v, want just notif-q", *seen)
	}

	perm, err := m.GetNotification("notif-perm")
	if err != nil {
		t.Fatalf("get permission: %v", err)
	}
	if perm.Status != "pending" {
		t.Errorf("permission status = %q, want pending", perm.Status)
	}
}

// A manager with no broadcaster wired must still resolve rather than panic;
// the daemon sets one in NewShared, but nothing enforces ordering.
func TestResolve_WithoutBroadcaster(t *testing.T) {
	m := newTestManager(t)
	id := seedNotification(t, m, "notif-quiet")
	if err := m.Resolve(id, Decision{Status: "approved"}, "browser"); err != nil {
		t.Fatalf("resolve: %v", err)
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
