package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// The scheduler is exercised by passing it a time rather than by sleeping: a
// missed fire is six hours late, and no test should take six hours to say so.

func newSchedulerTest(t *testing.T) (*Scheduler, *Shared) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	shared := NewShared(db, notifications.NewManager(db), newStubBackend())
	// A log the test can throw away, so a schedule's own log does not land in
	// the developer's real one.
	old := ScheduleLogDir
	ScheduleLogDir = filepath.Join(t.TempDir(), "schedules")
	t.Cleanup(func() { ScheduleLogDir = old })

	return NewScheduler(shared), shared
}

func seedSchedule(t *testing.T, db *store.Store, sc *store.Schedule) *store.Schedule {
	t.Helper()
	if sc.Mode == "" {
		sc.Mode = "new"
	}
	if sc.Prompt == "" {
		sc.Prompt = "do the thing"
	}
	sc.Enabled = true
	if err := db.CreateSchedule(sc); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	return sc
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// A fire inside the grace window runs. One outside it does not — it asks.
func TestTick_LateButInsideTheGraceWindowStillFires(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "a", Name: "nearly", Kind: "timer", Cron: "*/5 * * * *",
		NextRunAt: rfc(now.Add(-2 * time.Minute)),
	})

	s.Tick(now)

	got, _ := shared.DB.GetSchedule("a")
	if got.LastStatus != "running" {
		t.Fatalf("two minutes late should still run, got %q (%s)", got.LastStatus, got.LastError)
	}
	if got.LastSessionID == "" {
		t.Fatal("a fire should have produced a session")
	}
}

func TestTick_TooLateAsksInsteadOfRunning(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "a", Name: "overnight", Kind: "timer", Cron: "0 2 * * *",
		NextRunAt: rfc(now.Add(-7 * time.Hour)),
	})

	s.Tick(now)

	got, _ := shared.DB.GetSchedule("a")
	if got.LastStatus != "missed" {
		t.Fatalf("want missed, got %q", got.LastStatus)
	}
	if got.LastSessionID != "" {
		t.Fatal("a missed fire must not start an agent at a surprising hour")
	}

	notifs, err := shared.Mgr.ListNotifications("", "pending", "")
	if err != nil || len(notifs) != 1 {
		t.Fatalf("want one question, got %d (%v)", len(notifs), err)
	}
	if notifs[0].Type != NotifScheduleMissed {
		t.Fatalf("want %s so the existing card renders it, got %s", NotifScheduleMissed, notifs[0].Type)
	}
	// NOT NULL, and every sweep keys on it.
	if notifs[0].SourceSession != "a" {
		t.Fatalf("the question is about schedule a, got source_session %q", notifs[0].SourceSession)
	}

	// And the next run is computed from now, not backfilled: one question for
	// the gap, not one per missed occurrence.
	next, err := time.Parse(time.RFC3339, got.NextRunAt)
	if err != nil || !next.After(now) {
		t.Fatalf("next run should be in the future, got %q", got.NextRunAt)
	}
}

func TestTick_DisabledScheduleDoesNothing(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	sc := &store.Schedule{ID: "a", Name: "paused", Kind: "timer", Cron: "*/5 * * * *",
		NextRunAt: rfc(now.Add(-time.Minute))}
	seedSchedule(t, shared.DB, sc)
	shared.DB.SetScheduleEnabled("a", false)

	s.Tick(now)

	got, _ := shared.DB.GetSchedule("a")
	if got.LastStatus != "" {
		t.Fatalf("a paused schedule should not fire, got %q", got.LastStatus)
	}
}

// The same tick twice must not fire twice: the claim is what prevents it.
func TestTick_TwiceOverTheSameMomentFiresOnce(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "a", Name: "hourly", Kind: "timer", Cron: "0 * * * *",
		NextRunAt: rfc(now.Add(-time.Minute)),
	})

	s.Tick(now)
	first, _ := shared.DB.GetSchedule("a")
	s.Tick(now)
	second, _ := shared.DB.GetSchedule("a")

	if first.FiresToday != 1 || second.FiresToday != 1 {
		t.Fatalf("want exactly one fire, got %d then %d", first.FiresToday, second.FiresToday)
	}
}

func TestTick_MonitorFiresOnlyWhenTheCheckMatches(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "quiet", Name: "quiet", Kind: "monitor", Cron: "*/5 * * * *",
		CheckCmd: "true", NextRunAt: rfc(now.Add(-time.Minute)),
	})
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "loud", Name: "loud", Kind: "monitor", Cron: "*/5 * * * *",
		CheckCmd: "echo trouble; exit 2", Prompt: "look at this:\n{{output}}",
		NextRunAt: rfc(now.Add(-time.Minute)),
	})

	s.Tick(now)

	quiet, _ := shared.DB.GetSchedule("quiet")
	if quiet.LastSessionID != "" {
		t.Fatal("a quiet check must not start an agent")
	}
	if quiet.LastCheckExit == nil || *quiet.LastCheckExit != 0 {
		t.Fatalf("the check should be recorded even when quiet: %+v", quiet.LastCheckExit)
	}

	loud, _ := shared.DB.GetSchedule("loud")
	if loud.LastSessionID == "" {
		t.Fatal("a matching check should fire")
	}
	if loud.LastCheckOut == "" {
		t.Fatal("what the check saw should be on the row")
	}
}

// A check that cannot run is a failure, never a match: a monitor that fires
// because its own probe broke is backwards.
func TestTick_BrokenCheckFailsRatherThanFiring(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "a", Name: "broken", Kind: "monitor", Cron: "*/5 * * * *",
		CheckFile: "/nope/not/here", NextRunAt: rfc(now.Add(-time.Minute)),
	})

	s.Tick(now)

	got, _ := shared.DB.GetSchedule("a")
	if got.LastSessionID != "" {
		t.Fatal("a broken check must not start an agent")
	}
	if got.LastStatus != "failed" || got.LastError == "" {
		t.Fatalf("want a recorded failure, got %q / %q", got.LastStatus, got.LastError)
	}
}

func TestTick_ThreeFailuresInARowPausesTheSchedule(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "a", Name: "broken", Kind: "monitor", Cron: "* * * * *",
		CheckFile: "/nope/not/here", NextRunAt: rfc(now.Add(-time.Second)),
	})

	for i := 0; i < 3; i++ {
		sc, _ := shared.DB.GetSchedule("a")
		shared.DB.SetScheduleNext("a", rfc(now.Add(-time.Second)))
		_ = sc
		s.Tick(now)
	}

	got, _ := shared.DB.GetSchedule("a")
	if got.Enabled {
		t.Fatalf("a schedule that cannot work should stop trying, streak=%d", got.FailStreak)
	}
}

// A one-shot has its moment and is then done — the row stays, which is what
// answers "did last night work".
func TestTick_OnceFiresAndIsDone(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{
		ID: "a", Name: "tonight", Kind: "once",
		RunAt: rfc(now.Add(-time.Minute)), NextRunAt: rfc(now.Add(-time.Minute)),
	})

	s.Tick(now)
	got, _ := shared.DB.GetSchedule("a")
	if got.DoneAt == "" {
		t.Fatal("a one-shot should be marked done")
	}
	if got.NextRunAt != "" {
		t.Fatalf("a done one-shot has no next run, got %q", got.NextRunAt)
	}

	// And it does not come back on the next pass.
	fires := got.FiresToday
	s.Tick(now.Add(time.Minute))
	got, _ = shared.DB.GetSchedule("a")
	if got.FiresToday != fires {
		t.Fatalf("a one-shot fired twice: %d → %d", fires, got.FiresToday)
	}
}

// The chain: a parent going idle releases a child, and a parent that failed
// releases only the links that said "either way".
func TestTick_ChainWaitsForIdleAndRespectsTheLink(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		afterWhen  string
		wantFired  bool
		wantStatus string
	}{
		{"idle releases a success link", "idle", "success", true, "running"},
		{"idle releases an any link", "idle", "any", true, "running"},
		{"error blocks a success link", "error", "success", false, "blocked"},
		{"error releases an any link", "error", "any", true, "running"},
		{"terminated blocks a success link", "terminated", "success", false, "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, shared := newSchedulerTest(t)
			now := time.Now()

			seedSchedule(t, shared.DB, &store.Schedule{
				ID: "parent", Name: "parent", Kind: "once", RunAt: rfc(now), NextRunAt: "",
			})
			seedSchedule(t, shared.DB, &store.Schedule{
				ID: "child", Name: "child", Kind: "after", AfterID: "parent", AfterWhen: tc.afterWhen,
			})

			// The parent ran and its session reached the state under test.
			seedSessionWithStatus(t, shared.DB, "parent-session", tc.status)
			shared.DB.RecordFire("parent", "parent-session", "running", "", now)

			s.Tick(now)

			child, _ := shared.DB.GetSchedule("child")
			fired := child.LastSessionID != ""
			if fired != tc.wantFired {
				t.Fatalf("fired = %v, want %v (status %q)", fired, tc.wantFired, child.LastStatus)
			}
			if child.LastStatus != tc.wantStatus {
				t.Fatalf("status = %q, want %q", child.LastStatus, tc.wantStatus)
			}
			// Either way, the parent's run has been dealt with exactly once.
			if child.AfterSession != "parent-session" {
				t.Fatalf("the child should remember which run it acted on, got %q", child.AfterSession)
			}
		})
	}
}

// A parent that is still working releases nothing.
func TestTick_ChainWaitsWhileTheParentIsBusy(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()

	seedSchedule(t, shared.DB, &store.Schedule{ID: "parent", Name: "parent", Kind: "once", RunAt: rfc(now)})
	seedSchedule(t, shared.DB, &store.Schedule{ID: "child", Name: "child", Kind: "after",
		AfterID: "parent", AfterWhen: "success"})
	seedSessionWithStatus(t, shared.DB, "parent-session", "active")
	shared.DB.RecordFire("parent", "parent-session", "running", "", now)

	s.Tick(now)

	child, _ := shared.DB.GetSchedule("child")
	if child.LastSessionID != "" || child.AfterSession != "" {
		t.Fatalf("the child should still be waiting: %+v", child)
	}
}

// Two children of one parent both start.
func TestTick_SiblingsBothStart(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()

	seedSchedule(t, shared.DB, &store.Schedule{ID: "parent", Name: "parent", Kind: "once", RunAt: rfc(now)})
	seedSchedule(t, shared.DB, &store.Schedule{ID: "one", Name: "one", Kind: "after", AfterID: "parent"})
	seedSchedule(t, shared.DB, &store.Schedule{ID: "two", Name: "two", Kind: "after", AfterID: "parent"})
	seedSessionWithStatus(t, shared.DB, "parent-session", "idle")
	shared.DB.RecordFire("parent", "parent-session", "running", "", now)

	s.Tick(now)

	for _, id := range []string{"one", "two"} {
		child, _ := shared.DB.GetSchedule(id)
		if child.LastSessionID == "" {
			t.Fatalf("sibling %s did not start", id)
		}
	}
}

// A run is settled when its session goes idle, which is what "done" means.
func TestTick_RunningBecomesOkWhenTheSessionGoesIdle(t *testing.T) {
	s, shared := newSchedulerTest(t)
	now := time.Now()
	seedSchedule(t, shared.DB, &store.Schedule{ID: "a", Name: "nightly", Kind: "timer",
		Cron: "0 2 * * *", NextRunAt: rfc(now.Add(time.Hour))})
	seedSessionWithStatus(t, shared.DB, "run-1", "active")
	shared.DB.RecordFire("a", "run-1", "running", "", now)

	s.Tick(now)
	got, _ := shared.DB.GetSchedule("a")
	if got.LastStatus != "running" {
		t.Fatalf("an active session is not finished, got %q", got.LastStatus)
	}

	shared.DB.UpdateSessionStatus("run-1", "idle", "Stop")
	s.Tick(now)
	got, _ = shared.DB.GetSchedule("a")
	if got.LastStatus != "ok" {
		t.Fatalf("idle means done, got %q", got.LastStatus)
	}
}

func TestFillPrompt(t *testing.T) {
	if got := FillPrompt("before {{output}} after", "MIDDLE"); got != "before MIDDLE after" {
		t.Fatalf("got %q", got)
	}
	// No placeholder means nothing is appended behind the author's back.
	if got := FillPrompt("no placeholder here", "MIDDLE"); got != "no placeholder here" {
		t.Fatalf("got %q", got)
	}
}

func TestRunCheckRules(t *testing.T) {
	cases := []struct {
		name        string
		sc          store.Schedule
		wantMatch   bool
		wantFailure bool
	}{
		{"non-zero exit is the news", store.Schedule{CheckCmd: "exit 1"}, true, false},
		{"zero exit is quiet", store.Schedule{CheckCmd: "exit 0"}, false, false},
		{
			// grep exits 1 when it finds nothing, and that is the good case.
			"a pattern beats the exit code",
			store.Schedule{CheckCmd: "echo nothing to see; exit 1", CheckMatch: "ERROR"},
			false, false,
		},
		{
			"a pattern that matches fires whatever the exit code",
			store.Schedule{CheckCmd: "echo ERROR here", CheckMatch: "ERROR"},
			true, false,
		},
		{"a missing file is a failure", store.Schedule{CheckFile: "/nope/not/here"}, false, true},
		{"a monitor with no check at all", store.Schedule{}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RunCheck(&tc.sc)
			if got.Matched != tc.wantMatch {
				t.Errorf("matched = %v, want %v", got.Matched, tc.wantMatch)
			}
			if got.Failed != tc.wantFailure {
				t.Errorf("failed = %v, want %v (%v)", got.Failed, tc.wantFailure, got.Err)
			}
			if tc.wantFailure && got.Matched {
				t.Error("a failed check must never count as a match")
			}
		})
	}
}

// A check that hangs is a failed check, not a reason to wake an agent.
func TestRunCheckTimesOut(t *testing.T) {
	old := checkTimeoutForTest(50 * time.Millisecond)
	defer old()

	got := RunCheck(&store.Schedule{CheckCmd: "sleep 5"})
	if !got.Failed {
		t.Fatal("a check that never finishes should fail")
	}
	if got.Matched {
		t.Fatal("a timeout must not fire an agent")
	}
}
