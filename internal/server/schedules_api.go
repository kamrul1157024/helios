package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kamrul1157024/helios/internal/schedule"
	"github.com/kamrul1157024/helios/internal/store"
)

// The same handlers serve both muxes: the apps reach them over the public API
// and the CLI over the internal one, and a schedule is the same thing to both.

// scheduleRequest is the wire shape of a schedule, on the way in.
//
// Pointers where absence has to be told apart from emptiness: a PATCH that
// leaves a field out must not clear it.
type scheduleRequest struct {
	Name           *string   `json:"name,omitempty"`
	Kind           *string   `json:"kind,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
	Cron           *string   `json:"cron,omitempty"`
	TZ             *string   `json:"tz,omitempty"`
	RunAt          *string   `json:"run_at,omitempty"`
	AfterID        *string   `json:"after_id,omitempty"`
	AfterWhen      *string   `json:"after_when,omitempty"`
	Mode           *string   `json:"mode,omitempty"`
	Prompt         *string   `json:"prompt,omitempty"`
	CWD            *string   `json:"cwd,omitempty"`
	Provider       *string   `json:"provider,omitempty"`
	Model          *string   `json:"model,omitempty"`
	PermissionMode *string   `json:"permission_mode,omitempty"`
	TargetSession  *string   `json:"target_session,omitempty"`
	CheckCmd       *string   `json:"check_cmd,omitempty"`
	CheckFile      *string   `json:"check_file,omitempty"`
	CheckArgs      *[]string `json:"check_args,omitempty"`
	CheckMatch     *string   `json:"check_match,omitempty"`
}

func (sh *Shared) listSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := sh.DB.ListSchedules()
	if err != nil {
		jsonError(w, "failed to list schedules", http.StatusInternalServerError)
		return
	}
	if schedules == nil {
		schedules = []store.Schedule{}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"schedules": schedules})
}

func (sh *Shared) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sc := &store.Schedule{
		ID:      uuid.New().String(),
		Kind:    "timer",
		Mode:    "new",
		Enabled: true,
		TZ:      time.Local.String(),
	}
	applySchedule(sc, &req)

	if err := sh.validateSchedule(sc); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	next, err := firstRun(sc, time.Now())
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	sc.NextRunAt = next

	if err := sh.DB.CreateSchedule(sc); err != nil {
		jsonError(w, "failed to create schedule", http.StatusInternalServerError)
		return
	}
	AppendScheduleLog(sc.ID, fmt.Sprintf("created %s", describeTrigger(sc)))
	sh.broadcastSchedule("schedule_created", sc.ID)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"schedule": sc})
}

func (sh *Shared) patchSchedule(w http.ResponseWriter, r *http.Request, id string) {
	sc, err := sh.DB.GetSchedule(id)
	if err != nil || sc == nil {
		jsonError(w, "schedule not found", http.StatusNotFound)
		return
	}

	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	wasTrigger := describeTrigger(sc)
	applySchedule(sc, &req)

	if err := sh.validateSchedule(sc); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The trigger moved, so what was computed from the old one is meaningless.
	if describeTrigger(sc) != wasTrigger {
		next, err := firstRun(sc, time.Now())
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		sc.NextRunAt = next
	}

	if err := sh.DB.UpdateSchedule(sc); err != nil {
		jsonError(w, "failed to update schedule", http.StatusInternalServerError)
		return
	}
	sh.broadcastSchedule("schedule_updated", sc.ID)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"schedule": sc})
}

func (sh *Shared) deleteSchedule(w http.ResponseWriter, id string) {
	// The whole branch goes: a job chained under this one has no clock of its
	// own and could never run again.
	deleted, err := sh.DB.DeleteSchedule(id)
	if err != nil {
		jsonError(w, "failed to delete schedule", http.StatusInternalServerError)
		return
	}
	for _, gone := range deleted {
		RemoveScheduleLog(gone)
		sh.broadcastSchedule("schedule_deleted", gone)
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "deleted": deleted})
}

// runSchedule fires out of turn: the "Run now" button and the CLI's `run`.
func (sh *Shared) runSchedule(w http.ResponseWriter, id string) {
	if scheduler == nil {
		jsonError(w, "the scheduler is not running", http.StatusServiceUnavailable)
		return
	}
	if err := scheduler.RunNow(id); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	sc, _ := sh.DB.GetSchedule(id)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "schedule": sc})
}

// checkSchedule runs a monitor's check once and reports what it saw, without
// firing. This is the "Test now" button: a monitor whose first real check is at
// 3am is a monitor nobody can debug.
func (sh *Shared) checkSchedule(w http.ResponseWriter, id string) {
	sc, err := sh.DB.GetSchedule(id)
	if err != nil || sc == nil {
		jsonError(w, "schedule not found", http.StatusNotFound)
		return
	}
	if !sc.IsMonitor() {
		jsonError(w, "this schedule has no check", http.StatusBadRequest)
		return
	}

	result := RunCheck(sc)
	body := map[string]interface{}{
		"exit":    result.Exit,
		"output":  result.Output,
		"matched": result.Matched,
		"failed":  result.Failed,
	}
	if result.Err != nil {
		body["error"] = result.Err.Error()
	}
	jsonResponse(w, http.StatusOK, body)
}

func (sh *Shared) scheduleLog(w http.ResponseWriter, r *http.Request, id string) {
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			tail = n
		}
	}
	lines, err := TailScheduleLog(id, tail)
	if err != nil {
		jsonError(w, "failed to read the log", http.StatusInternalServerError)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"lines": lines})
}

// ── Validation ──────────────────────────────────────────────────────────────

// validateSchedule refuses at save what would otherwise be discovered at 3am.
func (sh *Shared) validateSchedule(sc *store.Schedule) error {
	if err := store.ValidName(sc.Name); err != nil {
		return err
	}
	taken, err := sh.DB.NameTaken(sc.Name, sc.ID)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("a schedule called %q already exists", sc.Name)
	}
	if strings.TrimSpace(sc.Prompt) == "" {
		return fmt.Errorf("a schedule needs a prompt")
	}

	switch sc.Mode {
	case "new":
	case "resume":
		if sc.TargetSession == "" {
			return fmt.Errorf("a resume schedule needs a session to resume")
		}
	default:
		return fmt.Errorf("mode must be new or resume, not %q", sc.Mode)
	}

	switch sc.Kind {
	case "timer":
		if _, err := schedule.Parse(sc.Cron); err != nil {
			return fmt.Errorf("cron: %w", err)
		}
		// A prompt asking for output it will never be given is a mistake worth
		// naming now, rather than delivering the literal braces to an agent.
		if strings.Contains(sc.Prompt, OutputPlaceholder) {
			return fmt.Errorf("%s only means something in a monitor, which has a check to produce it",
				OutputPlaceholder)
		}
	case "monitor":
		if _, err := schedule.Parse(sc.Cron); err != nil {
			return fmt.Errorf("cron: %w", err)
		}
		if sc.CheckCmd == "" && sc.CheckFile == "" {
			return fmt.Errorf("a monitor needs a check: a command or a script")
		}
		if sc.CheckCmd != "" && sc.CheckFile != "" {
			return fmt.Errorf("a monitor has one check, either a command or a script")
		}
		if sc.CheckFile != "" {
			if err := CheckFileRunnable(sc.CheckFile); err != nil {
				return err
			}
		}
	case "once":
		if sc.RunAt == "" {
			return fmt.Errorf("a one-shot needs a time to run at")
		}
		if _, err := time.Parse(time.RFC3339, sc.RunAt); err != nil {
			return fmt.Errorf("run_at must be RFC3339, like 2026-03-02T22:00:00Z")
		}
		if strings.Contains(sc.Prompt, OutputPlaceholder) {
			return fmt.Errorf("%s only means something in a monitor", OutputPlaceholder)
		}
	case "after":
		if sc.AfterID == "" {
			return fmt.Errorf("an 'after' schedule needs a job to follow")
		}
		parent, err := sh.DB.GetSchedule(sc.AfterID)
		if err != nil || parent == nil {
			return fmt.Errorf("no schedule %s to follow", sc.AfterID)
		}
		if sc.AfterID == sc.ID {
			return fmt.Errorf("a schedule cannot follow itself")
		}
		// Walk up from the intended parent: if this schedule is already an
		// ancestor of it, the link would close a loop.
		ancestors, err := sh.DB.ScheduleAncestors(sc.AfterID)
		if err != nil {
			return err
		}
		for _, id := range ancestors {
			if id == sc.ID {
				return fmt.Errorf("that would make a loop: %s already runs after this one", parent.Name)
			}
		}
		if sc.AfterWhen != "any" {
			sc.AfterWhen = "success"
		}
	default:
		return fmt.Errorf("kind must be timer, once, monitor or after, not %q", sc.Kind)
	}
	return nil
}

// firstRun is when a freshly saved schedule fires next, and is where an
// expression that can never match is caught.
func firstRun(sc *store.Schedule, now time.Time) (string, error) {
	switch sc.Kind {
	case "timer", "monitor":
		cron, err := schedule.Parse(sc.Cron)
		if err != nil {
			return "", fmt.Errorf("cron: %w", err)
		}
		when, ok := cron.Next(now.In(locationOf(sc.TZ)))
		if !ok {
			return "", fmt.Errorf("%q can never fire — check the day and month", sc.Cron)
		}
		return when.UTC().Format(time.RFC3339), nil
	case "once":
		at, err := time.Parse(time.RFC3339, sc.RunAt)
		if err != nil {
			return "", fmt.Errorf("run_at must be RFC3339")
		}
		return at.UTC().Format(time.RFC3339), nil
	default:
		// An 'after' has no clock: its parent finishing is its only trigger.
		return "", nil
	}
}

// applySchedule copies the fields a request actually carried.
func applySchedule(sc *store.Schedule, req *scheduleRequest) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&sc.Name, req.Name)
	set(&sc.Kind, req.Kind)
	set(&sc.Cron, req.Cron)
	set(&sc.TZ, req.TZ)
	set(&sc.RunAt, req.RunAt)
	set(&sc.AfterID, req.AfterID)
	set(&sc.AfterWhen, req.AfterWhen)
	set(&sc.Mode, req.Mode)
	set(&sc.Prompt, req.Prompt)
	set(&sc.CWD, req.CWD)
	set(&sc.Provider, req.Provider)
	set(&sc.Model, req.Model)
	set(&sc.PermissionMode, req.PermissionMode)
	set(&sc.TargetSession, req.TargetSession)
	set(&sc.CheckCmd, req.CheckCmd)
	set(&sc.CheckFile, req.CheckFile)
	set(&sc.CheckMatch, req.CheckMatch)
	if req.CheckArgs != nil {
		sc.CheckArgs = *req.CheckArgs
	}
	if req.Enabled != nil {
		sc.Enabled = *req.Enabled
		// Turning one back on clears the state that paused it, which is what
		// "enable" means to the person pressing it.
		if sc.Enabled && (sc.LastStatus == "blocked" || sc.LastStatus == "failed") {
			sc.LastStatus, sc.LastError, sc.FailStreak, sc.FailingSince = "", "", 0, ""
		}
	}
	// Dragging a timer under a parent makes it an 'after', and a schedule has
	// exactly one trigger: two ways for the same job to start is a race nobody
	// asked for.
	if sc.Kind == "after" {
		sc.Cron, sc.RunAt = "", ""
	} else {
		sc.AfterID, sc.AfterWhen = "", ""
	}
}

// describeTrigger is what a schedule fires on, as one string, so a change of
// trigger is a change of this.
func describeTrigger(sc *store.Schedule) string {
	return strings.Join([]string{sc.Kind, sc.Cron, sc.RunAt, sc.AfterID, sc.TZ}, "|")
}

func (sh *Shared) broadcastSchedule(event, id string) {
	if sh.SSE == nil {
		return
	}
	sh.SSE.Broadcast(SSEEvent{Type: event, Data: map[string]interface{}{"schedule_id": id}})
}

// scheduleRoute dispatches /schedules and /schedules/{id}[/run|/check|/log],
// which is one handler because both muxes mount the same paths.
func (sh *Shared) scheduleRoute(w http.ResponseWriter, r *http.Request, prefix string) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			sh.listSchedules(w, r)
		case http.MethodPost:
			sh.createSchedule(w, r)
		default:
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	id, action, _ := strings.Cut(rest, "/")
	switch {
	case action == "run" && r.Method == http.MethodPost:
		sh.runSchedule(w, id)
	case action == "check" && r.Method == http.MethodPost:
		sh.checkSchedule(w, id)
	case action == "log" && r.Method == http.MethodGet:
		sh.scheduleLog(w, r, id)
	case action != "":
		jsonError(w, "no such action", http.StatusNotFound)
	case r.Method == http.MethodGet:
		sc, err := sh.DB.GetSchedule(id)
		if err != nil || sc == nil {
			jsonError(w, "schedule not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{"schedule": sc})
	case r.Method == http.MethodPatch:
		sh.patchSchedule(w, r, id)
	case r.Method == http.MethodDelete:
		sh.deleteSchedule(w, id)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
