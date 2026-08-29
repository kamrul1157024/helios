// Package provider is the seam between the daemon and the agent harnesses it
// drives.
//
// Every provider implements Provider. Everything beyond starting a session is
// an optional capability interface, declared here and discovered at
// registration rather than at the call site — see registry.go for why that
// distinction matters.
//
// The idiom is the one internal/tunnel already uses: a small core interface
// plus optional ones found by assertion. See docs/specs/47-provider-interface.md.
package provider

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// Kind is how a provider reached the registry. Clients show it; the daemon
// does not branch on it.
//
// It has one value today. It exists because a provider whose argv comes from
// outside the binary needs a trust state, and this is where that will hang.
type Kind string

const KindNative Kind = "native"

// Info identifies a provider.
//
// It carries no capability flags. What a provider supports is derived from the
// interfaces it implements, so a provider cannot claim something it has not
// got.
type Info struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
	Kind Kind   `json:"kind"`
}

// SessionSpec is everything a provider needs to start a session. It is a
// struct rather than a parameter list because it grows: permission mode
// arrived after model, and whatever comes next should not break every caller.
type SessionSpec struct {
	SessionID string
	Prompt    string
	Model     string
	CWD       string

	// PermissionMode is the agent's permission mode. Empty means the
	// provider's own default, which is not necessarily the agent's.
	PermissionMode string

	// SkipPermissions requests the agent's escape hatch for permission checks
	// entirely, and overrides PermissionMode. Separate from the mode because
	// the flag that spells it is provider-specific and, for Claude, is not
	// simply a synonym for one of the modes.
	SkipPermissions bool
}

// Launch is everything needed to start or wake a session.
type Launch struct {
	// Argv is executed directly, never through a shell and never joined into
	// a string, so a prompt full of quotes reaches the agent as typed.
	Argv []string

	// Env is merged over the daemon's own agent environment. A provider whose
	// hooks must identify the session they belong to sets it here.
	Env map[string]string

	// Mode is the permission mode the session will actually run under, or ""
	// when the provider has no such concept. Recorded at launch so a wake can
	// replay it rather than guess.
	Mode string
}

// Provider is an agent harness Helios can drive. Two methods; everything else
// is optional.
type Provider interface {
	Info() Info
	Launch(SessionSpec) (Launch, error)
}

// ==================== Capability interfaces ====================

// Resumer brings a cold session back.
//
// resumeID is whatever the provider stored on the session row, and is opaque
// to the daemon. For an agent that accepts Helios's own id it equals
// sessionID; for one that mints its own it does not.
type Resumer interface {
	Resume(sessionID, resumeID, mode string) (Launch, error)
}

// HookHandler processes an incoming hook request and writes the response.
type HookHandler func(ctx *HookContext, w http.ResponseWriter, r *http.Request, input json.RawMessage)

// Hooker serves the agent's callbacks.
type Hooker interface {
	// HookRoutes maps a path suffix to its handler. "session/start" serves
	// POST /hooks/<provider-id>/session/start.
	HookRoutes() map[string]HookHandler
}

// Scope is where a hook table is written.
type Scope int

const (
	ScopeUser Scope = iota
	ScopeProject
)

// HookHealth is what the daemon can tell a user about a hook install.
//
// Effective is separate from Installed because an agent may read a hook table
// and then decline to run it. Codex does exactly that for untrusted hooks, in
// silence, and a daemon that checked only its own last write would report
// healthy while receiving nothing. See docs/specs/46-codex-provider.md.
type HookHealth struct {
	Installed bool   `json:"installed"`
	Current   bool   `json:"current"`
	Effective bool   `json:"effective"`
	Detail    string `json:"detail,omitempty"`
}

// HookInstaller writes the agent's hook configuration.
type HookInstaller interface {
	InstallHooks(Scope) error
	HookHealth() HookHealth
	RemoveHooks() error
}

// ActionHandler turns a client's answer into a decision.
type ActionHandler func(notif *store.Notification, body json.RawMessage) (notifications.Decision, error)

// ActionRoute is a handler plus what the clients need to render and rank the
// notification it answers.
//
// The metadata travels with the handler because the clients used to hardcode
// it, once per client per surface, and adding a provider meant editing all of
// them. Served, it is edited nowhere.
type ActionRoute struct {
	Handler ActionHandler
	// Label and Detail are user-facing, in the client's settings list.
	Label  string
	Detail string
	// Blocking marks a request that holds the agent until it is answered.
	// Those get a card that can answer them; the rest are news.
	Blocking bool
	// Group buckets the settings list: "action_required" or "info".
	Group string
	// DefaultAlert is whether a fresh install alerts on this type.
	DefaultAlert bool
}

// Actor answers notifications this provider raises.
type Actor interface {
	// ActionRoutes maps a notification type to the route that answers it. The
	// type must be prefixed with the provider's ID.
	ActionRoutes() map[string]ActionRoute
}

// Moder exposes the agent's permission modes.
type Moder interface {
	// PermissionModes lists the modes, most restrictive first.
	PermissionModes() []string
	ValidMode(mode string) bool
}

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ContextWindow string `json:"context_window,omitempty"`
}

// ModelLister returns the models a provider can run.
type ModelLister interface {
	Models() ([]ModelInfo, error)
}

// Transcriber reads the agent's own conversation log.
type Transcriber interface {
	// LocateTranscript finds a session's transcript when the recorded path has
	// gone stale, or returns "".
	LocateTranscript(sessionID string) string
	ParseTranscript(path string, limit, offset int) (*transcript.TranscriptResult, error)
}

// Discoverer finds sessions the user started outside Helios.
type Discoverer interface {
	Discover(db *store.Store)
}

// Notify publishes an SSE event.
type Notify func(eventType string, data interface{})

// Titler names a session from its transcript.
type Titler interface {
	Title(db *store.Store, sessionID, cwd, transcriptPath string, notify Notify) string
	AutoTitle(ctx *HookContext, sessionID, cwd, transcriptPath string, notify Notify)
}

// SmallModel runs the provider's cheapest model for short text generation.
// Implementations should use the provider's CLI, to respect the user's
// existing auth rather than asking for a key.
type SmallModel interface {
	Complete(ctx context.Context, system, prompt string) (string, error)
}

// EventTypeInfo describes a reportable event from a provider.
type EventTypeInfo struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"` // tools, actions, lifecycle, context, subagents, other
}

// Narrator declares the events the reporter can filter on.
type Narrator interface {
	EventTypes() []EventTypeInfo
}

// Command is a slash command available in a provider's CLI.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

// Commander lists the provider's slash commands.
type Commander interface {
	Commands() []Command
}

// Queuer hands a prompt to a session whose agent is mid-turn.
//
// The provider owns how a prompt reaches its agent. For both agents today that
// is typing into the PTY, which they queue and pick up at the end of the turn;
// a provider whose agent needs an out-of-band call does it here instead
// without the daemon knowing.
//
// Not implementing this means a busy session rejects the prompt rather than
// holding it, which is the safe answer for an agent that would drop it.
type Queuer interface {
	QueuePrompt(sessionID, resumeID, text string) error
}

// ScreenPrompt is a modal found on a session's screen that no hook reports.
type ScreenPrompt struct {
	// Type is the notification type to raise, prefixed with the provider ID.
	Type string
	// Title and Detail are user-facing.
	Title  string
	Detail string
}

// ScreenWatcher recognises a modal the agent is blocked on and that no hook
// reports.
//
// The daemon owns the watching — the polling, the TTL, the screen source — and
// asks the provider what a screen means. It exists because the workspace-trust
// dialog is per-agent: Claude and Codex both have one, worded differently, and
// a daemon matching one set of phrases misses the other in silence.
type ScreenWatcher interface {
	MatchScreen(screen string) *ScreenPrompt
}

// ==================== Hook plumbing ====================

// ReportEvent is a narration event passed to the Reporter. Defined here to
// avoid a circular import: reporter imports provider.
type ReportEvent struct {
	Type      string
	SessionID string
	CWD       string
	ToolName  string
	ToolInput string // summarized tool input
	Message   string
	Status    string
	AgentType string
	Detail    string
}

// HookContext provides everything a hook handler needs without importing
// server.
type HookContext struct {
	DB       *store.Store
	Mgr      *notifications.Manager
	Terminal backend.Backend
	// HITL paints helios's own prompt over a session's terminal so the person
	// sitting at it can answer. Nil in contexts built for a single
	// non-blocking call, and on backends that cannot draw over a session.
	HITL *hitl.Controller
	// Notify broadcasts an SSE event.
	Notify Notify
	// SessionStarted marks a session as having reported in, which stops the
	// trust-dialog watcher for it and releases anything waiting for the agent
	// to finish booting.
	SessionStarted func(sessionID string)
	// PromptSubmitted marks a prompt as accepted by the agent. It is the only
	// proof a prompt typed into a terminal actually landed.
	PromptSubmitted func(sessionID string)
	// Report pushes an event to the Reporter for narration.
	Report func(event ReportEvent)
}
