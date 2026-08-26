package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
)

// meteredBackend reports per-session memory and records what was killed, so a
// whole eviction pass can be driven without a real terminal host.
type meteredBackend struct {
	*fakeBackend
	usage  map[string]int64
	killed []string
}

func newMeteredBackend(usage map[string]int64) *meteredBackend {
	ids := make([]string, 0, len(usage))
	for id := range usage {
		ids = append(ids, id)
	}
	return &meteredBackend{fakeBackend: newFakeBackend(ids...), usage: usage}
}

func (m *meteredBackend) Usage() map[string]int64 { return m.usage }

func (m *meteredBackend) Kill(sessionID string) error {
	m.killed = append(m.killed, sessionID)
	delete(m.usage, sessionID)
	m.live[sessionID] = false
	return nil
}

// evictHarness wires a real store and broadcaster to a metered backend, so the
// pass under test is the one the daemon actually runs.
type evictHarness struct {
	db  *store.Store
	be  *meteredBackend
	sse *server.SSEBroadcaster
}

func newEvictHarness(t *testing.T, usage map[string]int64) *evictHarness {
	t.Helper()
	return &evictHarness{
		db:  setupTestStore(t),
		be:  newMeteredBackend(usage),
		sse: server.NewSSEBroadcaster(),
	}
}

// add records a session as the daemon would see it: a status, and how long ago
// a human last looked.
func (h *evictHarness) add(t *testing.T, id, status string, unread time.Duration, pinned bool) {
	t.Helper()
	// InsertDiscoveredSession rather than UpsertSession: it keeps the
	// timestamps it is given, and UpsertSession stamps last_event_at to now,
	// which would make every session look freshly active.
	stamp := time.Now().UTC().Add(-unread).Format(time.RFC3339)
	if err := h.db.InsertDiscoveredSession(&store.Session{
		SessionID:   id,
		Source:      "claude",
		CWD:         "/tmp/" + id,
		Status:      status,
		LastEventAt: &stamp,
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
	if pinned {
		if err := h.db.UpdateSessionFlags(id, true, false); err != nil {
			t.Fatalf("pin %s: %v", id, err)
		}
	}
}

func (h *evictHarness) run() { evictOverBudget(h.db, h.be, h.sse) }

// Nothing happens until the user asks for it. This is the guarantee that an
// upgrade does not start killing agents.
func TestEvictE2E_DoesNothingUntilEnabled(t *testing.T) {
	h := newEvictHarness(t, map[string]int64{"big": 900 << 20})
	h.add(t, "big", "idle", 2*time.Hour, false)

	// Far below what is warm, so pressure is not the reason nothing happens.
	if err := h.db.SetSetting(store.SettingBudgetFraction, "0.000001"); err != nil {
		t.Fatalf("budget: %v", err)
	}

	h.run()
	if len(h.be.killed) != 0 {
		t.Fatalf("evicted %v while the feature was off", h.be.killed)
	}

	if err := h.db.SetSetting(store.SettingEvictEnabled, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	h.run()
	if len(h.be.killed) != 1 || h.be.killed[0] != "big" {
		t.Fatalf("killed %v, want [big] once enabled", h.be.killed)
	}
}

// The whole point: free memory, and take the right thing.
func TestEvictE2E_TakesTheRightSessionAndStops(t *testing.T) {
	h := newEvictHarness(t, map[string]int64{
		"stale-big":    900 << 20, // unopened for hours
		"fresh-big":    900 << 20, // read minutes ago
		"stale-small":  50 << 20,
		"needs-answer": 900 << 20, // waiting on the human
		"pinned-big":   900 << 20,
	})
	h.add(t, "stale-big", "idle", 3*time.Hour, false)
	h.add(t, "fresh-big", "idle", 6*time.Minute, false)
	h.add(t, "stale-small", "idle", 3*time.Hour, false)
	h.add(t, "needs-answer", "waiting_permission", 5*time.Hour, false)
	h.add(t, "pinned-big", "idle", 5*time.Hour, true)

	enableWithBudget(t, h, "0.0000001")

	h.run()

	if len(h.be.killed) == 0 {
		t.Fatal("nothing was evicted while far over budget")
	}
	for _, id := range h.be.killed {
		if id == "needs-answer" {
			t.Error("evicted a session the human is being asked a question by")
		}
		if id == "pinned-big" {
			t.Error("evicted a pinned session")
		}
	}
	// Long unread and large is the best victim, so it goes first.
	if h.be.killed[0] != "stale-big" {
		t.Errorf("first eviction was %s, want stale-big", h.be.killed[0])
	}
}

// A session going cold must stay a session. The tmux-era reaper marked it
// terminated, which was a dead end.
func TestEvictE2E_KeepsTheSessionAlive(t *testing.T) {
	h := newEvictHarness(t, map[string]int64{"big": 900 << 20})
	h.add(t, "big", "idle", 2*time.Hour, false)
	enableWithBudget(t, h, "0.0000001")

	h.run()

	sess, err := h.db.GetSession("big")
	if err != nil || sess == nil {
		t.Fatalf("session gone after eviction: %v", err)
	}
	if sess.Status == "terminated" {
		t.Error("eviction terminated the session")
	}
	if sess.EndedAt != nil {
		t.Error("eviction set ended_at")
	}
	if h.be.Alive("big") {
		t.Error("the terminal survived the eviction")
	}
}

// The user has to be told, or a session that quietly went cold and then takes
// seconds to answer reads as Helios being slow.
func TestEvictE2E_AnnouncesWhatItFreed(t *testing.T) {
	h := newEvictHarness(t, map[string]int64{"big": 900 << 20})
	h.add(t, "big", "idle", 2*time.Hour, false)
	enableWithBudget(t, h, "0.0000001")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	go func() {
		// The broadcaster drops events for a client that is not yet attached,
		// so the pass runs after the stream is being served.
		time.Sleep(50 * time.Millisecond)
		h.run()
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	h.sse.ServeHTTP(rec, req.WithContext(ctx))

	body := rec.Body.String()
	for _, want := range []string{"session_evicted", "big", "session_updated", "freed"} {
		if !strings.Contains(body, want) {
			t.Errorf("broadcast is missing %q: %s", want, body)
		}
	}
}

// Touching a session takes it out of reach, which is the whole reason the
// timestamp exists.
func TestEvictE2E_TouchProtectsASession(t *testing.T) {
	h := newEvictHarness(t, map[string]int64{"a": 900 << 20, "b": 100 << 20})
	h.add(t, "a", "idle", 5*time.Hour, false)
	h.add(t, "b", "idle", 4*time.Hour, false)
	enableWithBudget(t, h, "0.0000001")

	// Someone opens "a" right now.
	if err := h.db.TouchSession("a"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	h.run()

	for _, id := range h.be.killed {
		if id == "a" {
			t.Fatalf("evicted the session someone just opened; killed %v", h.be.killed)
		}
	}
}

func enableWithBudget(t *testing.T, h *evictHarness, fraction string) {
	t.Helper()
	if err := h.db.SetSetting(store.SettingEvictEnabled, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := h.db.SetSetting(store.SettingBudgetFraction, fraction); err != nil {
		t.Fatalf("budget: %v", err)
	}
}
