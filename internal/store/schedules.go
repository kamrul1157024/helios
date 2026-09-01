package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Schedule is a saved prompt with something that decides when it runs.
//
// One of four things decides: a cron expression (timer), a single moment
// (once), a command whose result is checked on a cron (monitor), or another
// schedule finishing (after). Kind says which, and the fields for the other
// three are empty.
type Schedule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // timer | once | monitor | after
	Enabled bool   `json:"enabled"`

	// Cron drives timer and monitor; for a monitor it is how often to look.
	Cron string `json:"cron,omitempty"`
	// TZ is the zone the cron is read in, captured when the schedule is saved.
	TZ string `json:"tz,omitempty"`
	// RunAt is the one moment a 'once' schedule fires, RFC3339.
	RunAt string `json:"run_at,omitempty"`

	// AfterID is the schedule this one follows, and AfterWhen is whether a
	// failed parent still counts: "success" or "any".
	AfterID   string `json:"after_id,omitempty"`
	AfterWhen string `json:"after_when,omitempty"`
	// AfterSession is the parent run already acted on, so a parent that sits
	// idle for hours starts its children exactly once.
	AfterSession string `json:"after_session,omitempty"`

	// What to run.
	Mode           string `json:"mode"` // new | resume
	Prompt         string `json:"prompt"`
	CWD            string `json:"cwd,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	TargetSession  string `json:"target_session,omitempty"`

	// A monitor's check: a command through sh -c, or a file run directly.
	CheckCmd   string   `json:"check_cmd,omitempty"`
	CheckFile  string   `json:"check_file,omitempty"`
	CheckArgs  []string `json:"check_args,omitempty"`
	CheckMatch string   `json:"check_match,omitempty"`

	// When it next fires, or next looks. Empty for an 'after', which has no
	// clock, and for a 'once' that has already run.
	NextRunAt string `json:"next_run_at,omitempty"`

	// What happened last time.
	LastFiredAt   string `json:"last_fired_at,omitempty"`
	LastSessionID string `json:"last_session_id,omitempty"`
	LastStatus    string `json:"last_status,omitempty"` // running | ok | failed | missed | blocked
	LastError     string `json:"last_error,omitempty"`
	DoneAt        string `json:"done_at,omitempty"`

	// What the last check saw, for a monitor.
	LastCheckAt   string `json:"last_check_at,omitempty"`
	LastCheckExit *int   `json:"last_check_exit,omitempty"`
	LastCheckOut  string `json:"last_check_out,omitempty"`

	// Health, so a list can say "failing for six nights" and "fired 40 times
	// today" without a history table.
	FailStreak   int    `json:"fail_streak"`
	FailingSince string `json:"failing_since,omitempty"`
	FiresToday   int    `json:"fires_today"`
	FiresDay     string `json:"fires_day,omitempty"`

	CreatedAt string `json:"created_at"`
}

// IsMonitor reports whether this schedule looks before it fires.
func (s *Schedule) IsMonitor() bool { return s.Kind == "monitor" }

const scheduleColumns = `id, name, kind, enabled, cron, tz, run_at, after_id, after_when,
	after_session, mode, prompt, cwd, provider, model, permission_mode, target_session,
	check_cmd, check_file, check_args, check_match, next_run_at, last_fired_at,
	last_session_id, last_status, last_error, done_at, last_check_at, last_check_exit,
	last_check_out, fail_streak, failing_since, fires_today, fires_day, created_at`

func scanSchedule(scan func(...interface{}) error) (*Schedule, error) {
	var s Schedule
	var cron, tz, runAt, afterID, afterWhen, afterSession sql.NullString
	var cwd, prov, model, permMode, target sql.NullString
	var checkCmd, checkFile, checkArgs, checkMatch sql.NullString
	var nextRun, lastFired, lastSession, lastStatus, lastErr, doneAt sql.NullString
	var lastCheckAt, lastCheckOut, failingSince, firesDay sql.NullString
	var lastCheckExit sql.NullInt64

	err := scan(&s.ID, &s.Name, &s.Kind, &s.Enabled, &cron, &tz, &runAt, &afterID, &afterWhen,
		&afterSession, &s.Mode, &s.Prompt, &cwd, &prov, &model, &permMode, &target,
		&checkCmd, &checkFile, &checkArgs, &checkMatch, &nextRun, &lastFired,
		&lastSession, &lastStatus, &lastErr, &doneAt, &lastCheckAt, &lastCheckExit,
		&lastCheckOut, &s.FailStreak, &failingSince, &s.FiresToday, &firesDay, &s.CreatedAt)
	if err != nil {
		return nil, err
	}

	s.Cron, s.TZ, s.RunAt = cron.String, tz.String, runAt.String
	s.AfterID, s.AfterWhen, s.AfterSession = afterID.String, afterWhen.String, afterSession.String
	s.CWD, s.Provider, s.Model = cwd.String, prov.String, model.String
	s.PermissionMode, s.TargetSession = permMode.String, target.String
	s.CheckCmd, s.CheckFile, s.CheckMatch = checkCmd.String, checkFile.String, checkMatch.String
	s.NextRunAt, s.LastFiredAt = nextRun.String, lastFired.String
	s.LastSessionID, s.LastStatus, s.LastError = lastSession.String, lastStatus.String, lastErr.String
	s.DoneAt, s.LastCheckAt, s.LastCheckOut = doneAt.String, lastCheckAt.String, lastCheckOut.String
	s.FailingSince, s.FiresDay = failingSince.String, firesDay.String
	if lastCheckExit.Valid {
		exit := int(lastCheckExit.Int64)
		s.LastCheckExit = &exit
	}
	if checkArgs.String != "" {
		json.Unmarshal([]byte(checkArgs.String), &s.CheckArgs)
	}
	return &s, nil
}

// CreateSchedule writes a new schedule. The caller has already validated it.
func (s *Store) CreateSchedule(sc *Schedule) error {
	args, _ := json.Marshal(sc.CheckArgs)
	if sc.CreatedAt == "" {
		sc.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.Exec(`
		INSERT INTO schedules (`+scheduleColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sc.ID, sc.Name, sc.Kind, sc.Enabled, sc.Cron, sc.TZ, sc.RunAt, sc.AfterID, sc.AfterWhen,
		sc.AfterSession, sc.Mode, sc.Prompt, sc.CWD, sc.Provider, sc.Model, sc.PermissionMode,
		sc.TargetSession, sc.CheckCmd, sc.CheckFile, string(args), sc.CheckMatch, sc.NextRunAt,
		sc.LastFiredAt, sc.LastSessionID, sc.LastStatus, sc.LastError, sc.DoneAt, sc.LastCheckAt,
		sc.LastCheckExit, sc.LastCheckOut, sc.FailStreak, sc.FailingSince, sc.FiresToday,
		sc.FiresDay, sc.CreatedAt)
	if err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}
	return nil
}

// UpdateSchedule replaces every editable field of an existing schedule.
func (s *Store) UpdateSchedule(sc *Schedule) error {
	args, _ := json.Marshal(sc.CheckArgs)
	_, err := s.db.Exec(`
		UPDATE schedules SET
			name = ?, kind = ?, enabled = ?, cron = ?, tz = ?, run_at = ?,
			after_id = ?, after_when = ?, after_session = ?, mode = ?, prompt = ?, cwd = ?,
			provider = ?, model = ?, permission_mode = ?, target_session = ?,
			check_cmd = ?, check_file = ?, check_args = ?, check_match = ?,
			next_run_at = ?, last_fired_at = ?, last_session_id = ?, last_status = ?,
			last_error = ?, done_at = ?, last_check_at = ?, last_check_exit = ?,
			last_check_out = ?, fail_streak = ?, failing_since = ?, fires_today = ?,
			fires_day = ?
		WHERE id = ?`,
		sc.Name, sc.Kind, sc.Enabled, sc.Cron, sc.TZ, sc.RunAt,
		sc.AfterID, sc.AfterWhen, sc.AfterSession, sc.Mode, sc.Prompt, sc.CWD,
		sc.Provider, sc.Model, sc.PermissionMode, sc.TargetSession,
		sc.CheckCmd, sc.CheckFile, string(args), sc.CheckMatch,
		sc.NextRunAt, sc.LastFiredAt, sc.LastSessionID, sc.LastStatus,
		sc.LastError, sc.DoneAt, sc.LastCheckAt, sc.LastCheckExit,
		sc.LastCheckOut, sc.FailStreak, sc.FailingSince, sc.FiresToday,
		sc.FiresDay, sc.ID)
	if err != nil {
		return fmt.Errorf("update schedule %s: %w", sc.ID, err)
	}
	return nil
}

// GetSchedule returns one schedule, or nil when there is none.
func (s *Store) GetSchedule(id string) (*Schedule, error) {
	row := s.db.QueryRow(`SELECT `+scheduleColumns+` FROM schedules WHERE id = ?`, id)
	sc, err := scanSchedule(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule %s: %w", id, err)
	}
	return sc, nil
}

// ScheduleByName resolves the name a person types on the command line.
func (s *Store) ScheduleByName(name string) (*Schedule, error) {
	row := s.db.QueryRow(`SELECT `+scheduleColumns+` FROM schedules WHERE name = ?`, name)
	sc, err := scanSchedule(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule %q: %w", name, err)
	}
	return sc, nil
}

// ListSchedules returns every schedule, parents before children so a client can
// render the tree in one pass.
func (s *Store) ListSchedules() ([]Schedule, error) {
	rows, err := s.db.Query(`SELECT ` + scheduleColumns + ` FROM schedules ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()

	var flat []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		flat = append(flat, *sc)
	}
	return orderByTree(flat), nil
}

// orderByTree puts each child directly after its parent, so a list renders as a
// tree without the client sorting it. Anything whose parent is missing stays in
// creation order at the end rather than disappearing.
func orderByTree(flat []Schedule) []Schedule {
	children := map[string][]Schedule{}
	byID := map[string]bool{}
	for _, sc := range flat {
		byID[sc.ID] = true
	}
	var roots []Schedule
	for _, sc := range flat {
		if sc.AfterID != "" && byID[sc.AfterID] {
			children[sc.AfterID] = append(children[sc.AfterID], sc)
			continue
		}
		roots = append(roots, sc)
	}

	out := make([]Schedule, 0, len(flat))
	var walk func(sc Schedule, depth int)
	walk = func(sc Schedule, depth int) {
		out = append(out, sc)
		// A cycle cannot be written through the API, but a hand-edited database
		// should not hang the daemon.
		if depth > 32 {
			return
		}
		for _, child := range children[sc.ID] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return out
}

// DueSchedules returns the enabled schedules whose moment has passed, oldest
// first, which is the only query the firing loop makes.
func (s *Store) DueSchedules(now time.Time) ([]Schedule, error) {
	rows, err := s.db.Query(`SELECT `+scheduleColumns+`
		FROM schedules
		WHERE enabled = 1 AND next_run_at != '' AND next_run_at <= ?
		ORDER BY next_run_at`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("due schedules: %w", err)
	}
	defer rows.Close()

	var out []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan due schedule: %w", err)
		}
		out = append(out, *sc)
	}
	return out, nil
}

// WaitingSchedules returns the enabled 'after' schedules, which the loop checks
// against their parents' last run.
func (s *Store) WaitingSchedules() ([]Schedule, error) {
	rows, err := s.db.Query(`SELECT ` + scheduleColumns + `
		FROM schedules WHERE enabled = 1 AND kind = 'after' AND after_id != ''`)
	if err != nil {
		return nil, fmt.Errorf("waiting schedules: %w", err)
	}
	defer rows.Close()

	var out []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan waiting schedule: %w", err)
		}
		out = append(out, *sc)
	}
	return out, nil
}

// ClaimSchedule moves a schedule's next run forward, but only if nobody else
// moved it first.
//
// This is what stops a double fire, and it is a conditional update rather than
// a lock because SQLite already gives us the atomicity: two overlapping ticks,
// or a restart in the middle of a fire, cost nothing. Reports whether this
// caller is the one that claimed it.
func (s *Store) ClaimSchedule(id, expectedNext, newNext, firedAt string) (bool, error) {
	res, err := s.db.Exec(`
		UPDATE schedules SET next_run_at = ?, last_fired_at = ?
		WHERE id = ? AND next_run_at = ?`, newNext, firedAt, id, expectedNext)
	if err != nil {
		return false, fmt.Errorf("claim schedule %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim schedule %s: %w", id, err)
	}
	return n == 1, nil
}

// DeleteSchedule removes a schedule and everything chained under it, and
// releases the sessions they produced. It returns every id it deleted, the
// parent first.
//
// The chain goes with the parent because a link has no clock of its own: a
// child left behind can never fire again whether it is paused or not, and a
// list of jobs that will never run is a list nobody can read. Deleting the
// branch is the same fact with nothing left over.
//
// The runs are let go entirely. A session is hidden from the ordinary list only
// because its schedule is the place to find it — delete the schedule and that
// place is gone, so the tag would make real work invisible in every client at
// once, with no way back and its memory still spent. Clearing it turns them
// back into ordinary sessions, which is what they always were.
func (s *Store) DeleteSchedule(id string) ([]string, error) {
	branch, err := s.scheduleBranch(id)
	if err != nil {
		return nil, err
	}
	for _, at := range branch {
		if _, err := s.db.Exec(`UPDATE sessions SET schedule_id = NULL WHERE schedule_id = ?`, at); err != nil {
			return nil, fmt.Errorf("release runs of %s: %w", at, err)
		}
		if _, err := s.db.Exec(`DELETE FROM schedules WHERE id = ?`, at); err != nil {
			return nil, fmt.Errorf("delete schedule %s: %w", at, err)
		}
	}
	return branch, nil
}

// ScheduleBranch is a schedule and everything chained under it, parents before
// children — what a delete will take, so a client can say so before it asks.
func (s *Store) ScheduleBranch(id string) ([]string, error) { return s.scheduleBranch(id) }

// Walked breadth-first with a seen set rather than recursively: after_id is
// editable, and a cycle in it must not become a hang.
func (s *Store) scheduleBranch(id string) ([]string, error) {
	branch := []string{id}
	seen := map[string]bool{id: true}
	for at := 0; at < len(branch); at++ {
		children, err := s.scheduleChildren(branch[at])
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if seen[child] {
				continue
			}
			seen[child] = true
			branch = append(branch, child)
		}
	}
	return branch, nil
}

func (s *Store) scheduleChildren(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM schedules WHERE after_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("children of %s: %w", id, err)
	}
	defer rows.Close()

	var children []string
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			return nil, fmt.Errorf("children of %s: %w", id, err)
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("children of %s: %w", id, err)
	}
	return children, nil
}

// ReleaseOrphanedRuns unhides sessions whose schedule is already gone.
//
// Runs of a schedule deleted before this rule existed are invisible in every
// client and cannot be reached from anywhere. Run once at startup, which is
// where a fix for something already on disk belongs.
func (s *Store) ReleaseOrphanedRuns() (int64, error) {
	res, err := s.db.Exec(`
		UPDATE sessions SET schedule_id = NULL
		WHERE COALESCE(schedule_id, '') != ''
		  AND schedule_id NOT IN (SELECT id FROM schedules)`)
	if err != nil {
		return 0, fmt.Errorf("release orphaned runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("release orphaned runs: %w", err)
	}
	return n, nil
}

// ScheduleAncestors walks the after-chain upwards, which is how a cycle is
// refused before it is written.
func (s *Store) ScheduleAncestors(id string) ([]string, error) {
	var chain []string
	seen := map[string]bool{}
	for at := id; at != ""; {
		if seen[at] {
			break
		}
		seen[at] = true
		var parent sql.NullString
		err := s.db.QueryRow(`SELECT after_id FROM schedules WHERE id = ?`, at).Scan(&parent)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("walk ancestors of %s: %w", id, err)
		}
		if parent.String == "" {
			break
		}
		chain = append(chain, parent.String)
		at = parent.String
	}
	return chain, nil
}

// NameTaken reports whether another schedule already answers to this name.
func (s *Store) NameTaken(name, exceptID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM schedules WHERE name = ? AND id != ?`,
		name, exceptID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check name %q: %w", name, err)
	}
	return count > 0, nil
}

// RecordFire notes what a fire produced. Status is 'running' until the session
// it started goes idle.
func (s *Store) RecordFire(id, sessionID, status, errText string, when time.Time) error {
	day := when.Format("2006-01-02")
	_, err := s.db.Exec(`
		UPDATE schedules SET
			last_session_id = ?, last_status = ?, last_error = ?, last_fired_at = ?,
			fires_today = CASE WHEN fires_day = ? THEN fires_today + 1 ELSE 1 END,
			fires_day = ?
		WHERE id = ?`,
		sessionID, status, errText, when.UTC().Format(time.RFC3339), day, day, id)
	if err != nil {
		return fmt.Errorf("record fire for %s: %w", id, err)
	}
	return nil
}

// RecordOutcome closes the book on a run: ok when its session went idle, failed
// when it did not.
//
// The failure streak is what lets a list say "failing for six nights", which
// last_error alone cannot — it is overwritten every time.
func (s *Store) RecordOutcome(id, status, errText string, when time.Time) error {
	stamp := when.UTC().Format(time.RFC3339)
	var err error
	if status == "ok" {
		_, err = s.db.Exec(`
			UPDATE schedules SET last_status = ?, last_error = '', fail_streak = 0,
				failing_since = '' WHERE id = ?`, status, id)
	} else {
		_, err = s.db.Exec(`
			UPDATE schedules SET last_status = ?, last_error = ?, fail_streak = fail_streak + 1,
				failing_since = CASE WHEN failing_since = '' THEN ? ELSE failing_since END
			WHERE id = ?`, status, errText, stamp, id)
	}
	if err != nil {
		return fmt.Errorf("record outcome for %s: %w", id, err)
	}
	return nil
}

// RecordCheck stores what a monitor's check saw.
func (s *Store) RecordCheck(id string, exit int, output string, when time.Time) error {
	// Only the head of the output is kept on the row; the whole of it goes to
	// the schedule's own log, which is where someone reading history looks.
	const rowLimit = 4096
	if len(output) > rowLimit {
		output = output[:rowLimit]
	}
	_, err := s.db.Exec(`
		UPDATE schedules SET last_check_at = ?, last_check_exit = ?, last_check_out = ?
		WHERE id = ?`, when.UTC().Format(time.RFC3339), exit, output, id)
	if err != nil {
		return fmt.Errorf("record check for %s: %w", id, err)
	}
	return nil
}

// SetScheduleNext writes the next firing, which validation and editing both do.
func (s *Store) SetScheduleNext(id, next string) error {
	if _, err := s.db.Exec(`UPDATE schedules SET next_run_at = ? WHERE id = ?`, next, id); err != nil {
		return fmt.Errorf("set next run for %s: %w", id, err)
	}
	return nil
}

// MarkScheduleDone retires a one-shot: it has had its moment.
func (s *Store) MarkScheduleDone(id string, when time.Time) error {
	_, err := s.db.Exec(`
		UPDATE schedules SET done_at = ?, next_run_at = '', enabled = 0 WHERE id = ?`,
		when.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("mark schedule %s done: %w", id, err)
	}
	return nil
}

// SetScheduleEnabled pauses or resumes a schedule.
func (s *Store) SetScheduleEnabled(id string, enabled bool) error {
	if _, err := s.db.Exec(`UPDATE schedules SET enabled = ? WHERE id = ?`, enabled, id); err != nil {
		return fmt.Errorf("set enabled for %s: %w", id, err)
	}
	return nil
}

// SetScheduleBlocked records that a chain stopped here, and why.
func (s *Store) SetScheduleBlocked(id, reason string) error {
	_, err := s.db.Exec(`UPDATE schedules SET last_status = 'blocked', last_error = ? WHERE id = ?`,
		reason, id)
	if err != nil {
		return fmt.Errorf("block schedule %s: %w", id, err)
	}
	return nil
}

// SetAfterSession records which parent run a child has already acted on.
func (s *Store) SetAfterSession(id, sessionID string) error {
	if _, err := s.db.Exec(`UPDATE schedules SET after_session = ? WHERE id = ?`, sessionID, id); err != nil {
		return fmt.Errorf("record parent run for %s: %w", id, err)
	}
	return nil
}

// ValidName keeps names to what a CLI argument and a log line can carry
// unambiguously.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("a schedule needs a name")
	}
	if len(name) > 64 {
		return fmt.Errorf("name is longer than 64 characters")
	}
	if strings.ContainsAny(name, " \t\n/\\'\"") {
		return fmt.Errorf("name cannot contain spaces, slashes or quotes")
	}
	return nil
}
