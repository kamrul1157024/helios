package store

import (
	"testing"
	"time"
)

func scheduleStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func timer(id, name, next string) *Schedule {
	return &Schedule{
		ID: id, Name: name, Kind: "timer", Mode: "new", Enabled: true,
		Cron: "0 9 * * *", Prompt: "do the thing", NextRunAt: next,
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	s := scheduleStore(t)
	sc := timer("a", "morning", "2026-03-02T09:00:00Z")
	sc.CheckArgs = []string{"--threshold", "5000"}
	if err := s.CreateSchedule(sc); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetSchedule("a")
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "morning" || got.Cron != "0 9 * * *" || !got.Enabled {
		t.Fatalf("round trip lost something: %+v", got)
	}
	if len(got.CheckArgs) != 2 || got.CheckArgs[1] != "5000" {
		t.Fatalf("check args did not survive: %v", got.CheckArgs)
	}

	byName, err := s.ScheduleByName("morning")
	if err != nil || byName == nil || byName.ID != "a" {
		t.Fatalf("by name: %v %+v", err, byName)
	}
}

func TestDueSchedulesOnlyReturnsWhatIsDue(t *testing.T) {
	s := scheduleStore(t)
	now := time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)

	past := timer("past", "past", "2026-03-02T09:00:00Z")
	future := timer("future", "future", "2026-03-02T10:00:00Z")
	paused := timer("paused", "paused", "2026-03-02T09:00:00Z")
	paused.Enabled = false
	// An 'after' has no clock at all and must never be picked up here.
	waiting := &Schedule{ID: "child", Name: "child", Kind: "after", Mode: "new",
		Enabled: true, AfterID: "past", AfterWhen: "success", Prompt: "then this"}

	for _, sc := range []*Schedule{past, future, paused, waiting} {
		if err := s.CreateSchedule(sc); err != nil {
			t.Fatalf("create %s: %v", sc.Name, err)
		}
	}

	due, err := s.DueSchedules(now)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 || due[0].ID != "past" {
		t.Fatalf("want only the past one, got %d: %+v", len(due), due)
	}
}

// Claiming is what stops two ticks firing the same schedule.
func TestClaimScheduleIsOnceOnly(t *testing.T) {
	s := scheduleStore(t)
	sc := timer("a", "nightly", "2026-03-02T09:00:00Z")
	if err := s.CreateSchedule(sc); err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := s.ClaimSchedule("a", "2026-03-02T09:00:00Z", "2026-03-03T09:00:00Z", "2026-03-02T09:00:05Z")
	if err != nil || !first {
		t.Fatalf("first claim should win: %v %v", first, err)
	}
	// The second tick still holds the old value, and must lose.
	second, err := s.ClaimSchedule("a", "2026-03-02T09:00:00Z", "2026-03-03T09:00:00Z", "2026-03-02T09:00:05Z")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second {
		t.Fatal("second claim should have lost")
	}
}

func TestDeleteSchedulePausesItsChildren(t *testing.T) {
	s := scheduleStore(t)
	parent := timer("p", "parent", "2026-03-02T09:00:00Z")
	child := &Schedule{ID: "c", Name: "child", Kind: "after", Mode: "new", Enabled: true,
		AfterID: "p", AfterWhen: "success", Prompt: "then this"}
	s.CreateSchedule(parent)
	s.CreateSchedule(child)

	if err := s.DeleteSchedule("p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.GetSchedule("c")
	if got == nil {
		t.Fatal("the child should survive its parent")
	}
	if got.Enabled {
		t.Fatal("an orphan with no clock should be paused, not left enabled and silent")
	}
	if got.LastStatus != "blocked" || got.LastError == "" {
		t.Fatalf("the reason should be on the row: %+v", got)
	}
}

func TestScheduleAncestorsFindsALoop(t *testing.T) {
	s := scheduleStore(t)
	s.CreateSchedule(timer("a", "a", "2026-03-02T09:00:00Z"))
	s.CreateSchedule(&Schedule{ID: "b", Name: "b", Kind: "after", Mode: "new",
		Enabled: true, AfterID: "a", Prompt: "p"})
	s.CreateSchedule(&Schedule{ID: "c", Name: "c", Kind: "after", Mode: "new",
		Enabled: true, AfterID: "b", Prompt: "p"})

	chain, err := s.ScheduleAncestors("c")
	if err != nil {
		t.Fatalf("ancestors: %v", err)
	}
	if len(chain) != 2 || chain[0] != "b" || chain[1] != "a" {
		t.Fatalf("want [b a], got %v", chain)
	}
}

func TestListSchedulesPutsChildrenUnderParents(t *testing.T) {
	s := scheduleStore(t)
	// Created out of order on purpose.
	s.CreateSchedule(&Schedule{ID: "c", Name: "grandchild", Kind: "after", Mode: "new",
		Enabled: true, AfterID: "b", Prompt: "p", CreatedAt: "2026-03-01T00:00:03Z"})
	s.CreateSchedule(&Schedule{ID: "b", Name: "child", Kind: "after", Mode: "new",
		Enabled: true, AfterID: "a", Prompt: "p", CreatedAt: "2026-03-01T00:00:02Z"})
	parent := timer("a", "parent", "2026-03-02T09:00:00Z")
	parent.CreatedAt = "2026-03-01T00:00:01Z"
	s.CreateSchedule(parent)

	list, err := s.ListSchedules()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var order []string
	for _, sc := range list {
		order = append(order, sc.Name)
	}
	if len(order) != 3 || order[0] != "parent" || order[1] != "child" || order[2] != "grandchild" {
		t.Fatalf("want parent, child, grandchild — got %v", order)
	}
}

func TestFailStreakCountsAndClears(t *testing.T) {
	s := scheduleStore(t)
	s.CreateSchedule(timer("a", "flaky", "2026-03-02T09:00:00Z"))
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if err := s.RecordOutcome("a", "failed", "cwd is gone", now); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	got, _ := s.GetSchedule("a")
	if got.FailStreak != 3 {
		t.Fatalf("want a streak of 3, got %d", got.FailStreak)
	}
	if got.FailingSince == "" {
		t.Fatal("failing_since should hold the first failure, which is what 'since Tuesday' reads from")
	}
	first := got.FailingSince

	// A later failure does not move the start of the streak.
	s.RecordOutcome("a", "failed", "still gone", now.Add(time.Hour))
	got, _ = s.GetSchedule("a")
	if got.FailingSince != first {
		t.Fatalf("failing_since moved: %s → %s", first, got.FailingSince)
	}

	// A good run clears it.
	s.RecordOutcome("a", "ok", "", now.Add(2*time.Hour))
	got, _ = s.GetSchedule("a")
	if got.FailStreak != 0 || got.FailingSince != "" || got.LastError != "" {
		t.Fatalf("a good run should clear the streak: %+v", got)
	}
}

func TestFiresTodayResetsOnANewDay(t *testing.T) {
	s := scheduleStore(t)
	s.CreateSchedule(timer("a", "busy", "2026-03-02T09:00:00Z"))
	day := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	s.RecordFire("a", "s1", "running", "", day)
	s.RecordFire("a", "s2", "running", "", day.Add(time.Hour))
	got, _ := s.GetSchedule("a")
	if got.FiresToday != 2 {
		t.Fatalf("want 2 fires today, got %d", got.FiresToday)
	}

	s.RecordFire("a", "s3", "running", "", day.AddDate(0, 0, 1))
	got, _ = s.GetSchedule("a")
	if got.FiresToday != 1 {
		t.Fatalf("a new day starts at 1, got %d", got.FiresToday)
	}
}

// The sidebar's list must not show what the clock started, and the runs list
// must show nothing else.
func TestSessionJobsFilter(t *testing.T) {
	s := scheduleStore(t)
	mine := &Session{SessionID: "mine", Source: "claude", CWD: "/tmp", Status: "idle"}
	job := &Session{SessionID: "job", Source: "claude", CWD: "/tmp", Status: "idle", ScheduleID: "sched-1"}
	other := &Session{SessionID: "other-job", Source: "claude", CWD: "/tmp", Status: "idle", ScheduleID: "sched-2"}
	for _, sess := range []*Session{mine, job, other} {
		if err := s.UpsertSession(sess); err != nil {
			t.Fatalf("upsert %s: %v", sess.SessionID, err)
		}
	}

	// Neutral by default, which is what the reaper and the evictor rely on.
	all, err := s.ListSessions()
	if err != nil || len(all) != 3 {
		t.Fatalf("the store should hide nothing by default: %d %v", len(all), err)
	}

	sidebar, _ := s.SearchSessions(SessionQuery{Jobs: "exclude"})
	if len(sidebar) != 1 || sidebar[0].SessionID != "mine" {
		t.Fatalf("sidebar should hold only what a person started: %+v", sidebar)
	}

	jobs, _ := s.SearchSessions(SessionQuery{Jobs: "only"})
	if len(jobs) != 2 {
		t.Fatalf("the jobs list should hold both runs, got %d", len(jobs))
	}

	one, _ := s.SearchSessions(SessionQuery{Jobs: "only", ScheduleID: "sched-1"})
	if len(one) != 1 || one[0].SessionID != "job" {
		t.Fatalf("one schedule's runs: %+v", one)
	}

	// And the flag survives the round trip, which is what the clients read.
	got, _ := s.GetSession("job")
	if got.ScheduleID != "sched-1" {
		t.Fatalf("schedule_id lost: %+v", got)
	}
}
