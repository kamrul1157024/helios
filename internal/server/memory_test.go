package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// usageBackend is a stub that prices its warm terminals, which is what the
// host backend does and the tmux one never could.
type usageBackend struct {
	*stubBackend
	usage map[string]int64
}

func (b *usageBackend) Usage() map[string]int64 { return b.usage }

func (b *usageBackend) Status() backend.Status {
	var total int64
	for _, rss := range b.usage {
		total += rss
	}
	return backend.Status{Name: "stub", Available: true, Warm: len(b.usage), WarmRSS: total}
}

func newMemoryTest(t *testing.T, usage map[string]int64) (*PublicServer, *Shared, *usageBackend) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	be := &usageBackend{stubBackend: newStubBackend(), usage: usage}
	shared := NewShared(db, notifications.NewManager(db), be)
	return &PublicServer{shared: shared}, shared, be
}

func listSessions(t *testing.T, s *PublicServer) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleListSessions(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return payload
}

// Nothing reclaims memory on its own any more, so the only way a user learns a
// session is expensive is the number the list carries for it.
func TestListSessions_PricesWarmSessions(t *testing.T) {
	s, shared, be := newMemoryTest(t, map[string]int64{"warm": 412 * 1024 * 1024})
	seedSessionWithStatus(t, shared.DB, "warm", "idle")
	seedSessionWithStatus(t, shared.DB, "cold", "idle")
	be.handles["warm"] = "/tmp/warm.sock"

	sessions, ok := listSessions(t, s)["sessions"].([]interface{})
	if !ok {
		t.Fatal("response has no sessions array")
	}

	priced := map[string]interface{}{}
	for _, raw := range sessions {
		sess := raw.(map[string]interface{})
		priced[sess["session_id"].(string)] = sess["memory_bytes"]
	}
	if got := priced["warm"]; got != float64(412*1024*1024) {
		t.Errorf("warm session memory_bytes = %v, want 432013312", got)
	}
	// A cold session runs nothing and so costs nothing: reporting 0 would read
	// as a measurement, when the truth is there is nothing to measure.
	if got, ok := priced["cold"]; ok && got != nil {
		t.Errorf("cold session carries memory_bytes = %v, want it absent", got)
	}
}

func TestListSessions_ReportsPoolAndMachine(t *testing.T) {
	s, shared, be := newMemoryTest(t, map[string]int64{
		"a": 400 * 1024 * 1024,
		"b": 200 * 1024 * 1024,
	})
	seedSessionWithStatus(t, shared.DB, "a", "idle")
	be.handles["a"] = "/tmp/a.sock"

	host, ok := listSessions(t, s)["host"].(map[string]interface{})
	if !ok {
		t.Fatal("response has no host envelope")
	}
	if host["warm"] != float64(2) {
		t.Errorf("warm = %v, want 2", host["warm"])
	}
	if host["warm_rss"] != float64(600*1024*1024) {
		t.Errorf("warm_rss = %v, want 629145600", host["warm_rss"])
	}
	// The budget is advice, not a ceiling — but a client with no number to
	// compare against cannot tell the user the pool has grown.
	if budget, _ := host["budget"].(float64); budget <= 0 {
		t.Errorf("budget = %v, want the advisory threshold", host["budget"])
	}
	// What the pool costs means little without the machine it is costing.
	total, _ := host["memory_total"].(float64)
	used, _ := host["memory_used"].(float64)
	if total <= 0 || used <= 0 || used > total {
		t.Errorf("machine memory = %v of %v, want a usable reading", used, total)
	}
	if load, _ := host["load"].(float64); load < 0 {
		t.Errorf("load = %v, want a per-core fraction", load)
	}
}
