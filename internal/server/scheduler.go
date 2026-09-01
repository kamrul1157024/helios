// The firing loop.
//
// One pass is `Tick(now)`, and it takes the time as a parameter rather than
// reading the clock, which is what lets the tests exercise a missed fire, a
// daylight-saving morning and a chain without sleeping. The ticker itself lives
// in the daemon, beside the reaper.
//
// See docs/specs/55-scheduled-runs.md.

package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kamrul1157024/helios/internal/schedule"
	"github.com/kamrul1157024/helios/internal/store"
)

// How late a fire may be and still be run rather than reported missed.
//
// The window covers a 30-second tick, a machine under load, and an ordinary
// daemon restart. Anything later means nobody was home when it was due, and the
// answer to that is a question rather than an agent starting at a surprising
// hour.
const FireGrace = 5 * time.Minute

// How long a fired run may sit in `starting` before it is called a failure.
//
// Generous, because a cold agent loads a transcript, its MCP servers and the
// user's settings before it reports in, and calling a slow boot a failure would
// terminate work that was about to happen. Minutes rather than the 25 seconds a
// resumed session gets: nobody is waiting at a keyboard for this one.
const BootGrace = 3 * time.Minute

// Three failures in a row and a schedule stops trying. A schedule that cannot
// work should say so once, not write the same line into the log every night.
const maxFailStreak = 3

// Scheduler fires schedules. It holds no state of its own: everything it knows
// is in the database, which is what lets a restart pick up mid-chain.
type Scheduler struct {
	shared *Shared
}

func NewScheduler(shared *Shared) *Scheduler { return &Scheduler{shared: shared} }

// Tick is one pass of the loop: settle what is running, start what is due, and
// release whatever was waiting on a job that has now finished.
func (s *Scheduler) Tick(now time.Time) {
	s.settleRunning(now)
	s.startDue(now)
	s.advanceChains(now)
}

// ── What is due ─────────────────────────────────────────────────────────────

func (s *Scheduler) startDue(now time.Time) {
	due, err := s.shared.DB.DueSchedules(now)
	if err != nil {
		log.Printf("scheduler: list due: %v", err)
		return
	}

	for i := range due {
		sc := &due[i]

		next, err := s.nextAfter(sc, now)
		if err != nil {
			// A schedule whose expression no longer parses cannot be advanced,
			// and leaving next_run_at in the past would make it due for ever.
			s.fail(sc, now, fmt.Sprintf("cannot work out when this fires again: %v", err))
			continue
		}

		// Claimed before anything is started. Two overlapping ticks, or a
		// restart mid-fire, then cost nothing.
		claimed, err := s.shared.DB.ClaimSchedule(sc.ID, sc.NextRunAt, next, now.UTC().Format(time.RFC3339))
		if err != nil {
			log.Printf("scheduler: claim %s: %v", sc.Name, err)
			continue
		}
		if !claimed {
			continue
		}

		late := now.Sub(parseTime(sc.NextRunAt))
		switch {
		case sc.IsMonitor():
			// A missed check is not worth a question: looking now answers the
			// same thing the check would have answered then.
			s.runMonitor(sc, now)
		case late > FireGrace:
			s.reportMissed(sc, now, late)
		default:
			s.fire(sc, now, sc.Prompt)
		}
	}
}

// nextAfter is when this schedule fires after now, or "" for one that has no
// clock left — a one-shot that has just had its moment.
func (s *Scheduler) nextAfter(sc *store.Schedule, now time.Time) (string, error) {
	switch sc.Kind {
	case "once":
		return "", nil
	case "timer", "monitor":
		cron, err := schedule.Parse(sc.Cron)
		if err != nil {
			return "", err
		}
		when, ok := cron.Next(now.In(locationOf(sc.TZ)))
		if !ok {
			return "", fmt.Errorf("%q will never fire again", sc.Cron)
		}
		return when.UTC().Format(time.RFC3339), nil
	default:
		return "", nil
	}
}

// ── Firing ──────────────────────────────────────────────────────────────────

// fire starts the work, which is the ordinary create-session path with the
// prompt filled in — or the ordinary send-a-prompt path for a resume schedule.
func (s *Scheduler) fire(sc *store.Schedule, now time.Time, prompt string) {
	if sc.Mode == "resume" {
		s.fireResume(sc, now, prompt)
	} else {
		s.fireNew(sc, now, prompt)
	}

	if sc.Kind == "once" {
		if err := s.shared.DB.MarkScheduleDone(sc.ID, now); err != nil {
			log.Printf("scheduler: mark %s done: %v", sc.Name, err)
		}
	}
	s.broadcast("schedule_fired", sc.ID)
}

func (s *Scheduler) fireNew(sc *store.Schedule, now time.Time, prompt string) {
	cwd := sc.CWD
	if cwd == "" {
		// Nothing on this disk: a schedule that watches pull requests or an
		// inbox wants an agent with its tools, not a checkout.
		home, err := os.UserHomeDir()
		if err != nil {
			s.fail(sc, now, "no cwd and no home directory")
			return
		}
		cwd = home
	}

	started, err := s.shared.StartSession(NewSession{
		Provider:       sc.Provider,
		Prompt:         prompt,
		Model:          sc.Model,
		CWD:            cwd,
		PermissionMode: sc.PermissionMode,
		ScheduleID:     sc.ID,
	})
	if err != nil {
		s.fail(sc, now, err.Error())
		return
	}

	s.logf(sc, "fire   session %s · new · %s", started.SessionID, started.CWD)
	if err := s.shared.DB.RecordFire(sc.ID, started.SessionID, "running", "", now); err != nil {
		log.Printf("scheduler: record fire for %s: %v", sc.Name, err)
	}
	log.Printf("schedule %s (%s): fired → session %s", sc.Name, short(sc.ID), started.SessionID)
}

func (s *Scheduler) fireResume(sc *store.Schedule, now time.Time, prompt string) {
	if sc.TargetSession == "" {
		s.fail(sc, now, "this schedule has no session to resume")
		return
	}
	if _, err := s.shared.SendPrompt(sc.TargetSession, prompt); err != nil {
		// A conversation that cannot come back will fail every night, so it
		// stops rather than repeating itself into the log.
		s.fail(sc, now, fmt.Sprintf("could not prompt session %s: %v", short(sc.TargetSession), err))
		return
	}
	s.logf(sc, "fire   session %s · resume", sc.TargetSession)
	if err := s.shared.DB.RecordFire(sc.ID, sc.TargetSession, "running", "", now); err != nil {
		log.Printf("scheduler: record fire for %s: %v", sc.Name, err)
	}
	log.Printf("schedule %s (%s): fired → session %s (resumed)", sc.Name, short(sc.ID), sc.TargetSession)
}

// ── Monitors ────────────────────────────────────────────────────────────────

// runMonitor looks, and fires only if the check says there is something to do.
func (s *Scheduler) runMonitor(sc *store.Schedule, now time.Time) {
	result := RunCheck(sc)
	if err := s.shared.DB.RecordCheck(sc.ID, result.Exit, result.Output, now); err != nil {
		log.Printf("scheduler: record check for %s: %v", sc.Name, err)
	}

	switch {
	case result.Failed:
		s.logf(sc, "check  failed — %v", result.Err)
		s.fail(sc, now, fmt.Sprintf("the check failed: %v", result.Err))
		return
	case !result.Matched:
		s.logf(sc, "check  exit %d    quiet", result.Exit)
		return
	}

	s.logf(sc, "check  exit %d    MATCH → firing\n%s", result.Exit, indent(result.Output))
	s.fire(sc, now, FillPrompt(sc.Prompt, result.Output))
}

// ── Chains ──────────────────────────────────────────────────────────────────

// advanceChains starts the jobs whose parent has finished.
//
// A job is done when its session goes idle — the Stop hook writes that
// (internal/provider/claude/hooks.go:573). It is deliberately not `terminated`,
// which is the process going away and may not happen for hours on a warm
// session: a chain waiting for that would mostly never run.
func (s *Scheduler) advanceChains(now time.Time) {
	waiting, err := s.shared.DB.WaitingSchedules()
	if err != nil {
		log.Printf("scheduler: list waiting: %v", err)
		return
	}

	for i := range waiting {
		sc := &waiting[i]
		parent, err := s.shared.DB.GetSchedule(sc.AfterID)
		if err != nil || parent == nil {
			continue
		}
		if parent.LastSessionID == "" || parent.LastSessionID == sc.AfterSession {
			continue // never run, or this child has already acted on that run
		}

		// The parent's recorded outcome, not its session's current status: a
		// finished run is terminated moments later, and a chain reading the
		// session would call every parent a failure.
		outcome := recordedOutcome(parent)
		if outcome == "running" {
			continue
		}

		// Whatever happens below, this parent run has been dealt with.
		if err := s.shared.DB.SetAfterSession(sc.ID, parent.LastSessionID); err != nil {
			log.Printf("scheduler: record parent run for %s: %v", sc.Name, err)
		}

		if outcome == "failed" && sc.AfterWhen != "any" {
			reason := fmt.Sprintf("%s failed, and this link only runs on success", parent.Name)
			if err := s.shared.DB.SetScheduleBlocked(sc.ID, reason); err != nil {
				log.Printf("scheduler: block %s: %v", sc.Name, err)
			}
			s.logf(sc, "block  %s", reason)
			log.Printf("schedule %s (%s): blocked — %s", sc.Name, short(sc.ID), reason)
			s.broadcast("schedule_updated", sc.ID)
			continue
		}

		s.fire(sc, now, sc.Prompt)
	}
}

// recordedOutcome reads a run's fate off the schedule row, which settleRunning
// wrote earlier in this same tick.
func recordedOutcome(sc *store.Schedule) string {
	switch sc.LastStatus {
	case "ok":
		return "ok"
	case "running":
		return "running"
	default:
		// failed, missed, blocked — and "" for a schedule that has never run,
		// which the caller has already excluded by checking LastSessionID.
		return "failed"
	}
}

// outcomeOf reads a run's fate from the session it produced.
func (s *Scheduler) outcomeOf(sessionID string) string {
	sess, err := s.shared.DB.GetSession(sessionID)
	if err != nil || sess == nil {
		// The session is gone. Treating that as still running would wedge a
		// chain for ever.
		return "failed"
	}
	switch sess.Status {
	case "idle":
		return "ok"
	case "error", "terminated":
		return "failed"
	default:
		return "running"
	}
}

// settleRunning closes the book on fires whose session has since finished.
func (s *Scheduler) settleRunning(now time.Time) {
	all, err := s.shared.DB.ListSchedules()
	if err != nil {
		return
	}
	for i := range all {
		sc := &all[i]
		if sc.LastStatus != "running" || sc.LastSessionID == "" {
			continue
		}
		switch s.outcomeOf(sc.LastSessionID) {
		case "ok":
			s.shared.DB.RecordOutcome(sc.ID, "ok", "", now)
			s.logf(sc, "done   session %s went idle", sc.LastSessionID)
			s.endRun(sc)
			s.broadcast("schedule_updated", sc.ID)
		case "failed":
			s.finishFailed(sc, now, "the run did not finish cleanly")
			s.endRun(sc)
		default:
			if s.stalledAtBoot(sc, now) {
				s.fail(sc, now, fmt.Sprintf("the agent never started — session %s said nothing in %s",
					short(sc.LastSessionID), BootGrace))
				s.endRun(sc)
			}
		}
	}
}

// stalledAtBoot reports a run whose agent never said anything at all.
//
// A session's status is written by the agent's own hooks, so an agent that dies
// before its first one leaves the row at `starting` and nothing ever moves it
// again: the run reads as still working, the schedule stays `running` for ever,
// and every job chained behind it waits for ever. The reaper is no help — a
// dead terminal is a cold session by design (internal/daemon/reaper.go:16-30).
func (s *Scheduler) stalledAtBoot(sc *store.Schedule, now time.Time) bool {
	sess, err := s.shared.DB.GetSession(sc.LastSessionID)
	if err != nil || sess == nil || sess.Status != "starting" {
		return false
	}
	return now.Sub(parseTime(sc.LastFiredAt)) > BootGrace
}

// endRun closes the terminal a finished run was using.
//
// A job holds a whole agent process for as long as it is warm, and one that
// fires hourly would hold a new one every hour: nobody is going to type into
// them, and the transcript stays readable either way. A resume schedule is the
// exception — the session it keeps talking to is the point of it.
func (s *Scheduler) endRun(sc *store.Schedule) {
	if sc.Mode == "resume" || sc.LastSessionID == "" {
		return
	}
	sess, err := s.shared.DB.GetSession(sc.LastSessionID)
	if err != nil || sess == nil || sess.Status == "terminated" {
		return
	}
	s.shared.EndSession(sc.LastSessionID)
	s.logf(sc, "close  session %s terminated", sc.LastSessionID)
}

// ── Missed fires ────────────────────────────────────────────────────────────

// reportMissed asks rather than running. The question reaches every client
// because it is an ordinary notification of a type the cards already render.
func (s *Scheduler) reportMissed(sc *store.Schedule, now time.Time, late time.Duration) {
	if err := s.shared.DB.RecordOutcome(sc.ID, "missed", "nobody was home when this was due", now); err != nil {
		log.Printf("scheduler: record missed for %s: %v", sc.Name, err)
	}
	s.logf(sc, "missed by %s, asked", humanLate(late))
	log.Printf("schedule %s (%s): missed by %s, asked", sc.Name, short(sc.ID), humanLate(late))

	payload, _ := json.Marshal(map[string]interface{}{
		"schedule_id": sc.ID,
		"questions": []map[string]interface{}{{
			"question": fmt.Sprintf("%s did not run — it was due %s ago. Run it now?",
				sc.Name, humanLate(late)),
			"options": []map[string]string{
				{"label": "Run now"},
				{"label": "Skip"},
			},
		}},
	})

	title := fmt.Sprintf("%s did not run", sc.Name)
	detail := fmt.Sprintf("Due %s ago. Run it now?", humanLate(late))
	body := string(payload)
	notif := &store.Notification{
		ID:     uuid.New().String(),
		Source: SystemProviderID,
		// NOT NULL, and every sweep keys on it. The schedule is what this
		// question is about, so the schedule's id is what belongs here.
		SourceSession: sc.ID,
		CWD:           sc.CWD,
		Type:          NotifScheduleMissed,
		Status:        "pending",
		Title:         &title,
		Detail:        &detail,
		Payload:       &body,
	}
	if err := s.shared.Mgr.CreateNotification(notif); err != nil {
		log.Printf("scheduler: raise missed notification for %s: %v", sc.Name, err)
	}
	s.broadcast("schedule_updated", sc.ID)
}

// RunNow fires a schedule out of turn: the "Run now" button, the CLI's `run`,
// and the answer to a missed-run question all arrive here.
func (s *Scheduler) RunNow(id string) error {
	sc, err := s.shared.DB.GetSchedule(id)
	if err != nil || sc == nil {
		return fmt.Errorf("no schedule %s", id)
	}
	now := time.Now()
	if sc.IsMonitor() {
		s.runMonitor(sc, now)
		return nil
	}
	s.fire(sc, now, sc.Prompt)
	return nil
}

// ── Recording ───────────────────────────────────────────────────────────────

// fail records a fire that never produced a run.
func (s *Scheduler) fail(sc *store.Schedule, now time.Time, reason string) {
	s.finishFailed(sc, now, reason)
	log.Printf("schedule %s (%s): failed — %s", sc.Name, short(sc.ID), reason)
	s.logf(sc, "failed %s", reason)
}

func (s *Scheduler) finishFailed(sc *store.Schedule, now time.Time, reason string) {
	if err := s.shared.DB.RecordOutcome(sc.ID, "failed", reason, now); err != nil {
		log.Printf("scheduler: record failure for %s: %v", sc.Name, err)
	}
	// Re-read: the streak is the database's count, not this pass's guess.
	if fresh, err := s.shared.DB.GetSchedule(sc.ID); err == nil && fresh != nil {
		if fresh.FailStreak >= maxFailStreak && fresh.Enabled {
			if err := s.shared.DB.SetScheduleEnabled(sc.ID, false); err == nil {
				log.Printf("schedule %s (%s): disabled after %d failures",
					sc.Name, short(sc.ID), fresh.FailStreak)
				s.logf(sc, "paused after %d failures in a row", fresh.FailStreak)
			}
		}
	}
	s.broadcast("schedule_updated", sc.ID)
}

func (s *Scheduler) broadcast(event, id string) {
	if s.shared.SSE == nil {
		return
	}
	s.shared.SSE.Broadcast(SSEEvent{Type: event, Data: map[string]interface{}{"schedule_id": id}})
}

// ── Odds and ends ───────────────────────────────────────────────────────────

// locationOf resolves a stored zone name, falling back to the machine's own.
func locationOf(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.Local
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// humanLate is how long ago something was due, in the shortest true words.
// A Duration prints "7h0m0s", which nobody says out loud.
func humanLate(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh %dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "                 --- " + line
	}
	return strings.Join(lines, "\n")
}
