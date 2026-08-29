package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/discovery"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// Provider drives Claude Code.
//
// The hook and action handlers stay free functions in this package rather than
// methods. They take everything they need from the HookContext, so a receiver
// would add nothing but churn — including to their tests, which pass them as
// function values.
type Provider struct {
	bin string
	// hookPort is where the agent's hooks reach the daemon. Always the real
	// internal port: hooks are how helios sees a session at all.
	hookPort int
}

// New returns the Claude provider.
//
// Two ports, because they answer different questions and only one is optional.
// hookPort is where the agent's hooks call back, and must always be the
// daemon's real internal port. mcpPort is where a session reaches the Helios
// MCP server, and is zero when that feature is off — which is how the provider
// is told to leave --mcp-config off the argv.
//
// They were briefly one parameter. With MCP disabled that wrote every hook URL
// as http://localhost:0 and the daemon heard nothing.
func New(hookPort, mcpPort int) *Provider {
	setMCPPort(mcpPort)
	return &Provider{bin: findClaude(), hookPort: hookPort}
}

// SetBackend gives the action handlers access to session terminals. Called by
// the daemon once shared state exists, which is after registration.
func (p *Provider) SetBackend(b backend.Backend) { SetBackend(b) }

func (p *Provider) Info() provider.Info {
	return provider.Info{
		ID:   "claude",
		Name: "Claude Code",
		Icon: "terminal",
		Kind: provider.KindNative,
	}
}

func (p *Provider) Launch(spec provider.SessionSpec) (provider.Launch, error) {
	return provider.Launch{
		Argv: sessionArgs(spec),
		Mode: LaunchPermissionMode(spec),
	}, nil
}

// Resume ignores resumeID: Claude accepts the session id Helios minted, so the
// two are always equal. A provider whose agent mints its own would not.
func (p *Provider) Resume(sessionID, resumeID, mode string) (provider.Launch, error) {
	return provider.Launch{Argv: ResumeArgs(sessionID, mode), Mode: mode}, nil
}

func (p *Provider) PermissionModes() []string { return PermissionModes }

func (p *Provider) ValidMode(mode string) bool { return ValidPermissionMode(mode) }

func (p *Provider) Models() ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{
		{ID: "opus", Name: "Opus", Description: "Most capable model"},
		{ID: "sonnet", Name: "Sonnet", Description: "Fast and capable"},
		{ID: "haiku", Name: "Haiku", Description: "Fastest and most affordable"},
		{ID: "opus[1m]", Name: "Opus 1M", Description: "Opus with 1M context window", ContextWindow: "1M"},
		{ID: "sonnet[1m]", Name: "Sonnet 1M", Description: "Sonnet with 1M context window", ContextWindow: "1M"},
		{ID: "opusplan", Name: "Opus Plan", Description: "Opus plans, Sonnet executes"},
	}, nil
}

// HookRoutes maps a URL suffix to its handler. The daemon serves each at
// POST /hooks/claude/<suffix>.
func (p *Provider) HookRoutes() map[string]provider.HookHandler {
	return map[string]provider.HookHandler{
		"permission":        handlePermission,
		"question":          handleQuestion,
		"elicitation":       handleElicitation,
		"stop":              handleStop,
		"stop/failure":      handleStopFailure,
		"notification":      handleNotification,
		"session/start":     handleSessionStart,
		"session/end":       handleSessionEnd,
		"prompt/submit":     handlePromptSubmit,
		"tool/pre":          handleToolPre,
		"tool/post":         handleToolPost,
		"tool/post/failure": handleToolPostFailure,
		"compact/pre":       handlePreCompact,
		"compact/post":      handlePostCompact,
		"subagent/start":    handleSubagentStart,
		"subagent/stop":     handleSubagentStop,
	}
}

// ActionRoutes carries the clients' labels alongside the handlers, so the
// notification catalogue is served rather than hardcoded once per client.
func (p *Provider) ActionRoutes() map[string]provider.ActionRoute {
	return map[string]provider.ActionRoute{
		"claude.permission": {
			Handler:  handlePermissionAction,
			Label:    "Permission requests",
			Detail:   "Claude is asking to use a tool that needs your approval.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"claude.question": {
			Handler:  handleQuestionAction,
			Label:    "Questions",
			Detail:   "Claude needs your input to continue.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"claude.elicitation.form": {
			Handler:  handleElicitationAction,
			Label:    "Elicitation — form input",
			Detail:   "An MCP server is requesting structured input.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"claude.elicitation.url": {
			Handler:  handleElicitationAction,
			Label:    "Elicitation — authentication",
			Detail:   "An MCP server needs you to authenticate via a URL.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"claude.trust": {
			Handler:  handleTrustAction,
			Label:    "Workspace trust",
			Detail:   "Claude is asking to trust this workspace.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"claude.error": {
			Handler: handleErrorAction,
			Label:   "Session error",
			Detail:  "Claude stopped due to an error.",
			// Not blocking: the turn is already over. It is answerable —
			// retry or dismiss — which is a different thing.
			Blocking: false, Group: "info", DefaultAlert: true,
		},
	}
}

func (p *Provider) EventTypes() []provider.EventTypeInfo {
	return []provider.EventTypeInfo{
		{Type: "tool_pre", Label: "Tool Started", Description: "A tool is about to run", Category: "tools"},
		{Type: "tool_post", Label: "Tool Completed", Description: "A tool finished successfully", Category: "tools"},
		{Type: "tool_post_failure", Label: "Tool Failed", Description: "A tool finished with an error", Category: "tools"},
		{Type: "prompt_submit", Label: "Prompt Submitted", Description: "User sent a new prompt", Category: "actions"},
		{Type: "permission", Label: "Permission Needed", Description: "Waiting for user to approve an action", Category: "actions"},
		{Type: "question", Label: "Question Asked", Description: "Claude is asking a question", Category: "actions"},
		{Type: "stop", Label: "Session Stopped", Description: "Session finished normally", Category: "lifecycle"},
		{Type: "stop_failure", Label: "Session Error", Description: "Session stopped due to an error", Category: "lifecycle"},
		{Type: "session_start", Label: "Session Started", Description: "A new session began", Category: "lifecycle"},
		{Type: "session_end", Label: "Session Ended", Description: "Session was closed", Category: "lifecycle"},
		{Type: "compact_pre", Label: "Compacting", Description: "Context compaction is starting", Category: "context"},
		{Type: "compact_post", Label: "Compacted", Description: "Context compaction finished", Category: "context"},
		{Type: "subagent_start", Label: "Subagent Started", Description: "A subagent was spawned", Category: "subagents"},
		{Type: "subagent_stop", Label: "Subagent Stopped", Description: "A subagent finished", Category: "subagents"},
		{Type: "notification", Label: "Notification", Description: "A general notification from Claude", Category: "other"},
	}
}

func (p *Provider) Commands() []provider.Command {
	return []provider.Command{
		{Name: "/compact", Description: "Compact conversation context", Icon: "compress"},
		{Name: "/review", Description: "Review code changes", Icon: "rate_review"},
		{Name: "/cost", Description: "Show token usage & cost", Icon: "payments"},
		{Name: "/status", Description: "Show session status", Icon: "info"},
		{Name: "/doctor", Description: "Run health check", Icon: "health_and_safety"},
		{Name: "/memory", Description: "View & manage memory", Icon: "memory"},
		{Name: "/clear", Description: "Clear conversation history", Icon: "clear_all"},
		{Name: "/model", Description: "Switch model", Icon: "swap_horiz"},
	}
}

// LocateTranscript finds a transcript whose recorded path has gone stale.
// Claude keeps it in a directory named after the session's cwd, so entering a
// worktree moves the file out from under whatever was recorded at launch.
func (p *Provider) LocateTranscript(sessionID string) string {
	return discovery.FindClaudeTranscript(sessionID)
}

func (p *Provider) ParseLine(line []byte, seq int) []transcript.Message {
	return transcript.ParseClaudeLine(line, seq)
}

func (p *Provider) Discover(db *store.Store) { discovery.DiscoverClaudeSessions(db) }

// Title and AutoTitle delegate to the shared implementation, which reads the
// transcript through this provider's own parser and narrates with its own
// small model. Nothing about naming a session is Claude-specific.
func (p *Provider) Title(db *store.Store, sessionID, cwd, transcriptPath string, notify provider.Notify) string {
	return provider.RegenerateTitle(db, sessionID, cwd, transcriptPath, notify)
}

func (p *Provider) AutoTitle(ctx *provider.HookContext, sessionID, cwd, transcriptPath string, notify provider.Notify) {
	provider.TriggerAutoTitle(ctx, sessionID, cwd, transcriptPath, notify)
}

// Complete runs the CLI with the cheapest model, which respects whatever auth
// the user already has rather than asking for a key.
func (p *Provider) Complete(ctx context.Context, system, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, p.bin,
		"--bare", "-p",
		"--model", "haiku",
		"--output-format", "json",
		"--system-prompt", system,
	)
	cmd.Stdin = strings.NewReader(prompt)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude cli: %w", err)
	}

	var result struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("claude cli error: %s", result.Result)
	}
	return result.Result, nil
}

// MatchScreen recognises the workspace-trust dialog, which no hook reports:
// the agent is blocked before it has said anything to the daemon.
func (p *Provider) MatchScreen(screen string) *provider.ScreenPrompt {
	lower := strings.ToLower(screen)
	for _, pattern := range trustPromptPatterns {
		if strings.Contains(lower, pattern) {
			return &provider.ScreenPrompt{
				Type:   "claude.trust",
				Title:  "Workspace trust required",
				Detail: "Claude is asking to trust this folder before it can run.",
			}
		}
	}
	return nil
}

// trustPromptPatterns are phrases from Claude's workspace trust dialog.
//
// Matched against emulator output, never the raw PTY stream: Claude Code
// positions text with cursor-column jumps, so these phrases never appear
// contiguously in the bytes it writes.
var trustPromptPatterns = []string{
	"yes, i trust this folder",
	"quick safety check",
	"one you trust",
	"trust the files in this",
}

// QueuePrompt types the prompt into the session's terminal, which Claude holds
// until the current turn ends.
//
// Typing rather than `claude --resume -p`: a one-shot spawn costs a fresh
// process per message and cannot be handed off between the phone and the
// terminal.
func (p *Provider) QueuePrompt(sessionID, resumeID, text string) error {
	if terminalBackend == nil {
		return errNoTerminal
	}
	return terminalBackend.SendText(sessionID, text)
}

// Available reports whether the claude CLI is on this machine. Resolved on
// every call, so installing the agent takes effect without a daemon restart.
func (p *Provider) Available() bool {
	_, found := provider.LookAgent("claude")
	return found
}
