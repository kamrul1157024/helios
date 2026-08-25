package daemon

import (
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

var now = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *string {
	s := now.Add(-d).Format(time.RFC3339)
	return &s
}

func session(id, status string, interacted time.Duration) store.Session {
	return store.Session{
		SessionID:        id,
		Status:           status,
		LastInteractedAt: at(interacted),
		LastEventAt:      at(interacted),
	}
}

func ids(cs []candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.SessionID
	}
	return out
}

// Everything but idle means something is in flight. waiting_permission is the
// one that matters most: it looks idle by the clock, but the human is the
// blocker and taking the terminal discards the question.
func TestCandidates_OnlyIdle(t *testing.T) {
	sessions := []store.Session{
		session("idle", "idle", time.Hour),
		session("active", "active", time.Hour),
		session("starting", "starting", time.Hour),
		session("compacting", "compacting", time.Hour),
		session("permission", "waiting_permission", time.Hour),
		session("input", "waiting_input", time.Hour),
	}
	usage := map[string]int64{}
	for _, s := range sessions {
		usage[s.SessionID] = 100
	}

	got := evictionCandidates(sessions, usage, now)
	if len(got) != 1 || got[0].SessionID != "idle" {
		t.Fatalf("candidates = %v, want [idle]", ids(got))
	}
}

func TestCandidates_SkipsPinnedArchivedAndCold(t *testing.T) {
	pinned := session("pinned", "idle", time.Hour)
	pinned.Pinned = true
	archived := session("archived", "idle", time.Hour)
	archived.Archived = true
	cold := session("cold", "idle", time.Hour)

	sessions := []store.Session{pinned, archived, cold, session("ok", "idle", time.Hour)}
	// cold has no entry in usage, which is how a session with no live terminal
	// presents itself.
	usage := map[string]int64{"pinned": 100, "archived": 100, "ok": 100}

	got := evictionCandidates(sessions, usage, now)
	if len(got) != 1 || got[0].SessionID != "ok" {
		t.Fatalf("candidates = %v, want [ok]", ids(got))
	}
}

// A session woken while the machine is over budget must not be taken straight
// back on the next pass.
func TestCandidates_RespectsMinimumIdleAge(t *testing.T) {
	sessions := []store.Session{
		session("fresh", "idle", time.Minute),
		session("settled", "idle", 10*time.Minute),
	}
	usage := map[string]int64{"fresh": 100, "settled": 100}

	got := evictionCandidates(sessions, usage, now)
	if len(got) != 1 || got[0].SessionID != "settled" {
		t.Fatalf("candidates = %v, want [settled]", ids(got))
	}
}

// The signal is human attention, not agent activity. An agent working alone
// keeps last_event_at fresh while nobody is watching.
func TestCandidates_PrefersInteractionOverAgentActivity(t *testing.T) {
	// Agent ran a minute ago; nobody has looked in two hours.
	unwatched := session("unwatched", "idle", 0)
	unwatched.LastInteractedAt = at(2 * time.Hour)
	unwatched.LastEventAt = at(time.Minute)

	got := evictionCandidates([]store.Session{unwatched}, map[string]int64{"unwatched": 100}, now)
	if len(got) != 1 {
		t.Fatalf("candidates = %v, want [unwatched]", ids(got))
	}
	if got[0].Unread < 2*time.Hour {
		t.Fatalf("unread = %s; agent activity was counted as a human looking", got[0].Unread)
	}
}

// No client has ever shown it, so it is a strong candidate rather than a new one.
func TestCandidates_MissingInteractionFallsBackToAgentActivity(t *testing.T) {
	sess := session("never-opened", "idle", 0)
	sess.LastInteractedAt = nil
	sess.LastEventAt = at(90 * time.Minute)

	got := evictionCandidates([]store.Session{sess}, map[string]int64{"never-opened": 100}, now)
	if len(got) != 1 || got[0].Unread < 90*time.Minute {
		t.Fatalf("unread = %v; a never-opened session was treated as just-read", got)
	}
}

func TestChoose_NothingWhenUnderBudget(t *testing.T) {
	cs := []candidate{{SessionID: "a", RSS: 100, Unread: time.Hour}}
	if got := chooseEvictions(cs, 500, 1000); got != nil {
		t.Fatalf("evicted %v while under budget", ids(got))
	}
}

func TestChoose_NothingWhenBudgetIsUnlimited(t *testing.T) {
	cs := []candidate{{SessionID: "a", RSS: 100, Unread: time.Hour}}
	if got := chooseEvictions(cs, 10_000, 0); got != nil {
		t.Fatalf("evicted %v with no budget set", ids(got))
	}
}

func TestChoose_StopsAsSoonAsUnderBudget(t *testing.T) {
	cs := []candidate{
		{SessionID: "big", RSS: 800, Unread: time.Hour},
		{SessionID: "small", RSS: 100, Unread: time.Hour},
	}
	got := chooseEvictions(cs, 1000, 500)
	if len(got) != 1 || got[0].SessionID != "big" {
		t.Fatalf("evicted %v, want just [big]", ids(got))
	}
}

func TestChoose_SameSizePrefersLongerUnread(t *testing.T) {
	cs := []candidate{
		{SessionID: "recent", RSS: 500, Unread: 10 * time.Minute},
		{SessionID: "stale", RSS: 500, Unread: 5 * time.Hour},
	}
	got := chooseEvictions(cs, 1000, 600)
	if len(got) != 1 || got[0].SessionID != "stale" {
		t.Fatalf("evicted %v, want [stale]", ids(got))
	}
}

func TestChoose_SameAgePrefersLarger(t *testing.T) {
	cs := []candidate{
		{SessionID: "small", RSS: 200, Unread: time.Hour},
		{SessionID: "large", RSS: 900, Unread: time.Hour},
	}
	got := chooseEvictions(cs, 1100, 900)
	if len(got) != 1 || got[0].SessionID != "large" {
		t.Fatalf("evicted %v, want [large]", ids(got))
	}
}

// The whole reason the score is a product rather than either input alone.
func TestChoose_WeighsSizeAgainstHowLongUnread(t *testing.T) {
	cs := []candidate{
		// Huge, but read a few minutes ago — likely still in use.
		{SessionID: "huge-recent", RSS: 900, Unread: 6 * time.Minute},
		// Smaller, but nobody has opened it in hours.
		{SessionID: "modest-stale", RSS: 400, Unread: 4 * time.Hour},
	}
	got := chooseEvictions(cs, 1300, 1000)
	if len(got) != 1 || got[0].SessionID != "modest-stale" {
		t.Fatalf("evicted %v, want [modest-stale]: being read six minutes ago outweighs being twice the size", ids(got))
	}

	// Make the recent one enormous and it wins anyway: the score is a product,
	// not a preference for one input.
	cs = []candidate{
		{SessionID: "vast-recent", RSS: 40_000, Unread: 6 * time.Minute},
		{SessionID: "modest-stale", RSS: 400, Unread: 4 * time.Hour},
	}
	got = chooseEvictions(cs, 40_400, 40_000)
	if len(got) != 1 || got[0].SessionID != "vast-recent" {
		t.Fatalf("evicted %v, want [vast-recent]", ids(got))
	}
}

func TestBudgetBytes(t *testing.T) {
	const sixteenGB = uint64(16) << 30

	if got := budgetBytes(0.25, sixteenGB); got != int64(4)<<30 {
		t.Errorf("quarter of 16 GB = %d, want %d", got, int64(4)<<30)
	}
	// Zero and negative both mean no limit, so eviction cannot be switched on
	// by a malformed setting.
	if got := budgetBytes(0, sixteenGB); got != 0 {
		t.Errorf("zero fraction = %d, want 0", got)
	}
	if got := budgetBytes(-1, sixteenGB); got != 0 {
		t.Errorf("negative fraction = %d, want 0", got)
	}
	if got := budgetBytes(0.25, 0); got != 0 {
		t.Errorf("unknown machine memory = %d, want 0", got)
	}
}
