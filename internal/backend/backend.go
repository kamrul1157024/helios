// Package backend abstracts where an agent session's terminal actually lives.
//
// Two implementations exist during the migration off tmux: Tmux, which drives
// panes through the tmux CLI, and Host, which drives per-session `helios
// ptyhost` processes over unix sockets. Callers address sessions by session
// ID; the mapping to a pane ID or socket path is the backend's business.
package backend

import "errors"

// ErrNoTerminal is returned when a session has no live terminal.
var ErrNoTerminal = errors.New("backend: session has no live terminal")

// Key is a named key, so callers do not have to know whether the backend
// speaks tmux key names or raw escape sequences.
type Key string

const (
	KeyEnter  Key = "enter"
	KeyEscape Key = "escape"
	KeyCtrlC  Key = "ctrl-c"
	KeyUp     Key = "up"
	KeyDown   Key = "down"
)

// Status reports backend health for the doctor/status endpoints.
type Status struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
	// Warm is the number of sessions currently holding a live terminal.
	Warm int `json:"warm"`
	// WarmRSS is what those terminals cost in resident memory, in bytes.
	WarmRSS int64 `json:"warm_rss"`
}

// Backend owns the terminals behind agent sessions.
//
// Implementations must be safe for concurrent use: the daemon calls these
// from HTTP handlers, hook handlers, and the reaper goroutine at once.
type Backend interface {
	// Name identifies the implementation ("tmux" or "host").
	Name() string

	// Available reports whether the backend can currently start sessions.
	Available() bool

	// Start launches argv in a new terminal for sessionID and records the
	// mapping. The command is executed directly, not through a shell, so argv
	// needs no quoting and the caller's environment is what it runs under.
	// The returned handle is a pane ID or a socket path depending on the
	// implementation; callers should treat it as opaque and only use it for
	// display or for Adopt.
	Start(sessionID, cwd string, argv []string) (handle string, err error)

	// Adopt records a terminal that was created outside this backend, which
	// is how `helios wrap` binds a session the user started by hand.
	Adopt(sessionID, handle, cwd string) error

	// Handle returns the terminal handle for a session.
	Handle(sessionID string) (string, bool)

	// Alive reports whether the session's terminal still exists.
	Alive(sessionID string) bool

	// Forget drops the mapping without touching the terminal.
	Forget(sessionID string)

	// Snapshot returns sessionID -> handle for every known session.
	Snapshot() map[string]string

	// SendText submits a prompt: the text followed by Enter.
	SendText(sessionID, text string) error

	// SendKey sends a single named key.
	SendKey(sessionID string, key Key) error

	// Interrupt stops the agent's current turn.
	Interrupt(sessionID string) error

	// Kill terminates the session's terminal and drops the mapping.
	Kill(sessionID string) error

	// Capture returns the visible screen as plain text.
	Capture(sessionID string) (string, error)

	// Rename sets the terminal's display name.
	Rename(sessionID, name string) error

	// Sweep drops sessions whose terminal has died and returns their IDs.
	Sweep() []string

	// Status reports backend health.
	Status() Status
}

// Usager is implemented by backends that can price their terminals. Nothing
// acts on the number: it is shown to the user, who decides what to close.
type Usager interface {
	// Usage reports resident bytes per warm session.
	Usage() map[string]int64
}

// Evicter is implemented by backends that can take a session's terminal away
// without ending the session.
//
// Kill does the same thing to the process, but the difference has to survive
// the kill: the agent runs its own exit hook on the way down, and Claude's
// marks the session terminated and stamps ended_at. Terminated is the archival
// state a person chooses; a session going cold has not ended and must come back
// as itself. Evict records the intent so the hook can tell the two apart. See
// docs/specs/42-cold-sessions.md.
type Evicter interface {
	// Evict stops the terminal, having first recorded that helios did it.
	Evict(sessionID string) error

	// EvictedRecently reports whether this session's terminal was taken by
	// Evict rather than closed by the user, and consumes the mark.
	EvictedRecently(sessionID string) bool
}

// Waker is implemented by backends that can bring a cold session back
// without losing conversation state. The host backend re-runs
// `claude --resume`; tmux cannot do this, so it does not implement it.
type Waker interface {
	// Wake ensures the session has a live terminal, starting one if needed,
	// and reports whether it had to be woken.
	Wake(sessionID, cwd string) (woken bool, err error)
}

// Viewer is implemented by backends that can stream a session's terminal to
// remote viewers. Only the host backend can: tmux has no way to fan a pane
// out to a phone and a desktop at once.
type Viewer interface {
	// Endpoint returns the address a viewer connects to for this session.
	Endpoint(sessionID string) (string, bool)
}
