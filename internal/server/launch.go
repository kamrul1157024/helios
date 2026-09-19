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
	"time"

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
	// ForkOf is the session whose conversation this one begins with, and is nil
	// for a session started from nothing.
	//
	// The whole session rather than its id: a provider needs both of the
	// parent's ids to name the conversation, and which of the two it reads is
	// the provider's business. See provider.Forker.
	ForkOf *store.Session
	// ForkWorkspace records how the fork was given somewhere to work:
	// "worktree" for ground of its own, "same" for the parent's. Ignored unless
	// ForkOf is set.
	ForkWorkspace string
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

	var launch provider.Launch
	if req.ForkOf != nil {
		launch, err = forkLaunch(req, sessionID)
	} else {
		launch, err = prov.Launch(provider.SessionSpec{
			SessionID:       sessionID,
			Prompt:          req.Prompt,
			Model:           req.Model,
			CWD:             req.CWD,
			PermissionMode:  req.PermissionMode,
			SkipPermissions: req.SkipPermissions,
		})
	}
	if err != nil {
		return nil, err
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
	if req.ForkOf != nil {
		forkedAt := time.Now().UTC().Format(time.RFC3339)
		sess.ForkedFrom = req.ForkOf.SessionID
		sess.ForkedAt = &forkedAt
		sess.ForkWorkspace = req.ForkWorkspace
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

// forkLaunch asks the provider for the argv that continues the parent's
// conversation under the new session's id.
//
// Both of the parent's ids go across, unresolved. Which one names the
// conversation is the provider's business: Claude takes the id Helios minted
// and leaves resume_id nil, Codex mints its own and reports it. A handler that
// picked one here would work for exactly one of them.
//
// Empty argv is the provider saying this session cannot be forked — for Codex,
// that its parent never reported an id. It is a 409 rather than a 500: nothing
// failed, the request cannot be honoured.
func forkLaunch(req NewSession, sessionID string) (provider.Launch, error) {
	forker := provider.ForkerFor(req.Provider)
	if forker == nil {
		return provider.Launch{}, statusError(http.StatusNotImplemented,
			"%s sessions cannot be forked", req.Provider)
	}

	mode := req.PermissionMode
	if mode == "" && req.ForkOf.PermissionMode != nil {
		mode = *req.ForkOf.PermissionMode
	}
	resumeID := ""
	if req.ForkOf.ResumeID != nil {
		resumeID = *req.ForkOf.ResumeID
	}

	launch, err := forker.Fork(sessionID, req.ForkOf.SessionID, resumeID, mode)
	if err != nil {
		return provider.Launch{}, statusError(http.StatusInternalServerError,
			"failed to build fork: %v", err)
	}
	if len(launch.Argv) == 0 {
		return provider.Launch{}, statusError(http.StatusConflict,
			"session %s has no conversation to fork yet", req.ForkOf.SessionID)
	}

	// Some agents scope their conversations by directory, so a fork given a
	// worktree of its own cannot see its parent until the transcript is put
	// there.
	//
	// Before the terminal starts, not after: the agent resolves the resume on
	// its first line, and when it cannot it exits without ever calling a hook.
	// The daemon is then left holding a session that reads as idle and contains
	// nothing, which is how this shipped broken the first time.
	if prep := provider.ForkPreparerFor(req.Provider); prep != nil {
		err := prep.PrepareFork(req.ForkOf.CWD, req.CWD, req.ForkOf.SessionID, resumeID)
		if errors.Is(err, provider.ErrNoConversation) {
			return provider.Launch{}, statusError(http.StatusConflict,
				"session %s has no conversation to fork yet", req.ForkOf.SessionID)
		}
		if err != nil {
			return provider.Launch{}, statusError(http.StatusInternalServerError,
				"failed to prepare the fork: %v", err)
		}
	}
	return launch, nil
}

// awaitAgent waits for a spawned agent to report in before anything is typed
// at it.
//
// The status is read again after subscribing, and that order is the point: the
// hook fires the signal and writes the status, and nothing is remembered by a
// signal nobody was waiting on. Checking only before would wait out the full
// timeout for an agent that reported in a moment earlier.
func (sh *Shared) awaitAgent(id string) error {
	ready := sh.Signals.Await(SignalAgentReady, id)
	defer ready.Release()

	if session, err := sh.DB.GetSession(id); err == nil && session != nil && session.Status != "starting" {
		return nil
	}
	if !ready.Wait(agentBootTimeout) {
		log.Printf("session-send: session %s never reported in within %s", id, agentBootTimeout)
		return statusError(http.StatusGatewayTimeout, "the session is still starting up")
	}
	return nil
}

// awaitQuietScreen waits for a booting agent to finish painting its terminal.
//
// The ready hook fires from the agent process, which is running well before its
// TUI has claimed the terminal, and the raw-mode switch that claim performs
// discards whatever is sitting in the input buffer. Text typed into that window
// is not late — it is gone, with no error anywhere and a session left looking
// idle because nothing was ever asked of it. That is the failure this exists
// for; a prompt merely read late is the ack budget's problem, not this one.
//
// A settled screen is the proof, and it costs nothing to ask for: the daemon
// already mirrors every session, so this is a map lookup and a string compare.
// A booting TUI repaints continuously; once two captures a settle apart agree,
// the composer is up and reading.
//
// Giving up on the settle is deliberately silent and deliberately not an
// error. Typing at an agent whose screen never settles is exactly what this
// did before, so the worst case is the behaviour it replaced.
//
// A trust dialog is the one thing it refuses over. See below.
func (sh *Shared) awaitQuietScreen(id string) error {
	deadline := time.Now().Add(screenSettleTimeout)
	blankUntil := time.Now().Add(screenBlankGrace)
	last := ""
	for time.Now().Before(deadline) {
		screen, err := sh.Backend.Capture(id)
		if err != nil {
			return nil
		}
		// Blank does not count as settled: a terminal shows nothing at all in
		// the moment before its TUI paints, which is the moment this exists to
		// wait out. But it cannot be waited on indefinitely either — a backend
		// with no mirror to read answers empty forever, and holding every send
		// for the full timeout to learn nothing from it is worse than typing.
		if screen == "" {
			if time.Now().After(blankUntil) {
				return nil
			}
		} else if screen == last {
			// Still, but not ready. A modal is as motionless as a composer,
			// and the agent behind it is not reading prose — it is reading a
			// menu selection, so a prompt typed here does not queue, it
			// answers. Codex makes this the ordinary case rather than the rare
			// one: on a fresh install it shows two of these back to back,
			// directory trust and then hook trust, before it reads anything.
			//
			// Refused rather than waited out. The watcher has already raised
			// the dialog's own notification, so there is something to answer
			// and something to say about why this did not go.
			if prompt := matchTrustPrompt(screen); prompt != nil {
				log.Printf("session-send: session %s is blocked on %s", id, prompt.Type)
				return statusError(http.StatusConflict,
					"the session is waiting on a prompt of its own: %s", prompt.Title)
			}
			return nil
		}
		last = screen
		time.Sleep(screenSettleInterval)
	}
	log.Printf("session-send: session %s was still repainting after %s", id, screenSettleTimeout)
	return nil
}

// EndSession kills a session's terminal and records it as terminated.
//
// The handler used to be the only way to end one, so the scheduler — which ends
// every run it starts — would have had to fake an HTTP response to reach it.
func (sh *Shared) EndSession(id string) {
	if err := sh.Backend.Kill(id); err != nil {
		log.Printf("terminate: kill terminal for %s: %v", id, err)
	}
	sh.DB.UpdateSessionStatus(id, "terminated", "Terminate")
	sh.SSE.Broadcast(SSEEvent{
		Type: "session_status",
		Data: map[string]interface{}{
			"session_id": id,
			"status":     "terminated",
		},
	})
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

/*
justReportedIn says the agent has announced itself and done nothing since.

SessionStart means the agent process is up. It does not mean the TUI has
finished claiming the terminal, and the raw-mode switch that claim performs
throws away whatever is already in the input buffer — so a prompt typed in that
window is not late, it is gone.

Read off the last event rather than off a clock. A wall-clock window would be a
guess about how slow the machine is, and would be wrong on the machine that is
slow enough to matter. The event moves the instant the agent does anything at
all, so this stops applying by itself and costs a settle only on the first
prompt after a boot.
*/
func justReportedIn(session *store.Session) bool {
	return session.LastEvent != nil && *session.LastEvent == "SessionStart"
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
	// Whether this prompt is going at an agent that is still coming up, which
	// is the case both timeouts below have to be generous about.
	booting := false
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
		booting = resumed
	} else if session.Status == "starting" {
		// A launched session has the same gap as a woken one: StartSession
		// returns as soon as the terminal is up, and "starting" means no
		// SessionStart hook has arrived, so the agent is spawned but not yet
		// reading. The gap is only reachable through the API — the app creates
		// a session and prompts it a moment later, which is what attaching a
		// file to a new session has to do, because the upload needs an id that
		// only the create returns.
		if err := sh.awaitAgent(id); err != nil {
			return PromptResult{}, err
		}
		booting = true
	} else if justReportedIn(session) {
		// The same gap, reached from the other side. "starting" only catches a
		// session whose hook has not landed yet; the moment it lands the status
		// is idle, and a prompt arriving just after reads as an ordinary send
		// and skips the settle below — while the TUI is still painting, which
		// is exactly what the settle exists to wait out.
		booting = true
	}

	// The hook says the agent process is alive. It does not say the terminal is
	// being read, and the gap between the two is where a prompt is lost rather
	// than merely late.
	if booting {
		if err := sh.awaitQuietScreen(id); err != nil {
			return PromptResult{Resumed: resumed}, err
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

	budget := promptAckTimeout
	if booting {
		budget = bootPromptAckTimeout
	}
	if !ack.Wait(budget) {
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
