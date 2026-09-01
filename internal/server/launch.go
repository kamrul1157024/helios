// Starting a session, and prompting one that already exists.
//
// Both were written twice — once for the internal API the CLI talks to and
// once for the public API the apps talk to — and the copies had already begun
// to drift. They live here now because a third caller arrived that is not an
// HTTP handler at all: the scheduler, which fires a saved prompt on a clock and
// must do exactly what pressing "New session" does. A background goroutine with
// its own copy of the launch sequence is how the scheduled path quietly stops
// matching the interactive one.

package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// StatusError carries the HTTP status a failure deserves, so a handler can
// report it without re-deciding what went wrong.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

func statusError(status int, format string, args ...interface{}) *StatusError {
	return &StatusError{Status: status, Message: fmt.Sprintf(format, args...)}
}

// StatusOf reports the status an error asks for, or 500.
func StatusOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return http.StatusInternalServerError
}

// NewSession is everything a caller decides before a session exists.
//
// CWD is required and must already be the caller's choice: the two API handlers
// disagree about what an empty one means — the CLI wants its own directory, the
// apps want home — and that disagreement belongs to them, not here.
type NewSession struct {
	Provider        string
	Prompt          string
	Model           string
	CWD             string
	PermissionMode  string
	SkipPermissions bool
	// ScheduleID marks a session the clock started rather than a person, and
	// is the only thing that distinguishes one. Nothing in the session's
	// behaviour reads it; the lists do.
	ScheduleID string
}

// StartedSession is what the caller gets back, and what both APIs report.
type StartedSession struct {
	SessionID string
	Terminal  string
	CWD       string
}

// StartSession launches an agent and registers the session it belongs to.
func (sh *Shared) StartSession(req NewSession) (*StartedSession, error) {
	if req.Provider == "" {
		req.Provider = "claude"
	}
	prov, known := provider.Get(req.Provider)
	if !known {
		return nil, statusError(http.StatusNotFound, "unknown provider: %s", req.Provider)
	}

	resolved, err := resolveCWD(req.CWD)
	if err != nil {
		return nil, statusError(http.StatusBadRequest, "%s", err.Error())
	}
	req.CWD = resolved

	sessionID := uuid.New().String()
	launch, err := prov.Launch(provider.SessionSpec{
		SessionID:       sessionID,
		Prompt:          req.Prompt,
		Model:           req.Model,
		CWD:             req.CWD,
		PermissionMode:  req.PermissionMode,
		SkipPermissions: req.SkipPermissions,
	})
	if err != nil {
		return nil, statusError(http.StatusInternalServerError, "failed to build launch: %v", err)
	}

	handle, err := startTerminal(sh.Backend, sessionID, req.CWD, launch)
	if err != nil {
		return nil, statusError(http.StatusInternalServerError, "failed to start terminal: %v", err)
	}

	// Registered immediately so the API can report the session before the
	// agent's first hook arrives.
	event := "Launch"
	sess := &store.Session{
		SessionID:  sessionID,
		Source:     req.Provider,
		CWD:        req.CWD,
		Status:     "starting",
		LastEvent:  &event,
		ScheduleID: req.ScheduleID,
	}
	if err := sh.DB.UpsertSession(sess); err != nil {
		log.Printf("create-session: register session %s: %v", sessionID, err)
	}
	// Record the mode the agent launched under, now rather than on the first
	// hook: a session evicted before it reports in would otherwise wake with an
	// empty column, and an empty column means "whatever the CLI defaults to" —
	// which is not what a Helios-launched session was started in.
	if mode := launch.Mode; mode != "" {
		if err := sh.DB.UpdateSessionPermissionMode(sessionID, mode); err != nil {
			log.Printf("create-session: record permission mode for %s: %v", sessionID, err)
		}
	}

	// Watch for the workspace-trust dialog until the agent reports in.
	sh.Pending.Add(sessionID, req.CWD)

	return &StartedSession{SessionID: sessionID, Terminal: handle, CWD: req.CWD}, nil
}

// A prompt can fail in two ways that are the session's state rather than an
// error in the request, and both APIs report them as such.
var (
	ErrSessionBusy       = errors.New("session_busy")
	ErrSessionTerminated = errors.New("session_terminated")
)

// PromptResult says how the prompt reached the agent.
type PromptResult struct {
	// Queued means a busy agent took it through the provider's queue rather
	// than it being typed into a terminal.
	Queued bool
	// Resumed means the session had no terminal and one was started for it.
	Resumed bool
}

// SendPrompt delivers a message to a session, waking it first if it is cold.
func (sh *Shared) SendPrompt(id, message string) (PromptResult, error) {
	session, err := sh.DB.GetSession(id)
	if err != nil || session == nil {
		return PromptResult{}, statusError(http.StatusNotFound, "session not found")
	}

	live := sh.Backend.Alive(id)
	log.Printf("session-send: session=%s status=%s live=%v", id, session.Status, live)

	if session.Status == "active" || session.Status == "waiting_permission" {
		// The provider owns how a prompt reaches a busy agent. A provider that
		// does not implement Queuer has no way to hold one, so the session is
		// reported busy rather than the prompt being dropped.
		queuer := provider.QueuerFor(session.Source)
		if queuer == nil || !live {
			return PromptResult{}, ErrSessionBusy
		}
		resumeID := id
		if session.ResumeID != nil && *session.ResumeID != "" {
			resumeID = *session.ResumeID
		}
		if err := queuer.QueuePrompt(id, resumeID, message); err != nil {
			return PromptResult{}, statusError(http.StatusInternalServerError, "failed to queue: %v", err)
		}
		sh.DB.UpdateSessionLastUserMessage(id, message)
		log.Printf("session-send: queued prompt for session %s", id)
		return PromptResult{Queued: true}, nil
	}

	if session.Status == "terminated" {
		return PromptResult{}, ErrSessionTerminated
	}

	// Idle with no terminal: wake the agent and type into it. Deliberately not
	// `claude --resume -p`, which costs a fresh process per message and leaves
	// nothing for the user to attach to afterwards.
	resumed := false
	if !live {
		waker, ok := sh.Backend.(backend.Waker)
		if !ok {
			return PromptResult{}, statusError(http.StatusConflict,
				"session has no terminal and this backend cannot resume")
		}

		// Subscribed before the wake, never after: the agent can report in
		// while Wake is still returning, and a signal fired with nobody
		// listening is gone.
		ready := sh.Signals.Await(SignalAgentReady, id)
		defer ready.Release()

		woken, err := waker.Wake(id, session.CWD)
		if err != nil {
			log.Printf("session-send: wake %s: %v", id, err)
			return PromptResult{}, statusError(http.StatusInternalServerError, "failed to resume: %v", err)
		}
		resumed = woken

		// The wake only waits for the host's socket, which exists seconds
		// before the agent is reading its terminal. Typing into that gap is
		// how a prompt disappears with no trace.
		if resumed && !ready.Wait(agentBootTimeout) {
			log.Printf("session-send: session %s did not report ready within %s", id, agentBootTimeout)
			return PromptResult{Resumed: true}, statusError(http.StatusGatewayTimeout,
				"the session is still starting up")
		}
	}

	// Likewise subscribed before typing. The agent's own hook is the only
	// proof the prompt landed; anything else is this end guessing.
	ack := sh.Signals.Await(SignalPromptSubmitted, id)
	defer ack.Release()

	if err := sh.Backend.SendText(id, message); err != nil {
		log.Printf("session-send: send failed for %s: %v", id, err)
		return PromptResult{Resumed: resumed}, statusError(http.StatusInternalServerError, "failed to send: %v", err)
	}

	if !ack.Wait(promptAckTimeout) {
		// No retype: the prompt may yet be sitting in a dialog or a composer,
		// and a second copy would be a second turn. The session stays idle,
		// which is what it looks like from here, and the caller can retry.
		log.Printf("session-send: session %s never acknowledged the prompt", id)
		return PromptResult{Resumed: resumed}, statusError(http.StatusGatewayTimeout,
			"the session did not accept the message")
	}

	// Status is the prompt-submit hook's to write, and it has by now. Writing
	// it here as well is how a lost prompt used to look like a working one.
	sh.DB.UpdateSessionLastUserMessage(id, message)
	log.Printf("session-send: session %s accepted the prompt (resumed=%v)", id, resumed)
	return PromptResult{Resumed: resumed}, nil
}
