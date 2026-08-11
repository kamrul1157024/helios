package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
)

// findClaude locates the claude binary, mirroring the findTmux pattern.
// The daemon runs in a non-interactive context that may not have the user's
// full PATH, so we fall back to a login shell lookup if LookPath fails.
func findClaude() string {
	if p, err := exec.LookPath("claude"); err == nil && p != "" {
		return p
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	out, err := exec.Command(shell, "-l", "-c", "which claude").Output()
	if err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() {
				return p
			}
		}
	}
	return "claude" // fallback: let exec resolve it at call time
}

// DefaultPermissionMode is the permission mode every Helios-started Claude
// session gets unless something asks for another one.
//
// Helios sessions are driven from a phone as often as from a terminal, and a
// prompt that stops on the first permission question is a session the user
// cannot finish from the lock screen. "auto" still asks about the dangerous
// things; it just stops asking about the routine ones.
//
// It is also applied on resume (see cmd/helios/ptyhost.go): the mode is a
// per-invocation flag, not conversation state, so a session that went cold
// would silently come back in the CLI's own default without it.
const DefaultPermissionMode = "auto"

// PermissionModes are the values `claude --permission-mode` accepts, in the
// order they are worth offering: least to most trusting, with the two that
// need a warning last.
//
// Taken from the CLI's own choices list. Note that Helios can set any of them,
// including dontAsk, because it switches by restarting the session — the
// interactive Shift+Tab cycle reaches neither dontAsk nor, in some sessions,
// auto and bypassPermissions.
var PermissionModes = []string{
	"plan",
	"manual",
	"acceptEdits",
	"auto",
	"dontAsk",
	"bypassPermissions",
}

// ValidPermissionMode reports whether mode is one the CLI would accept.
// Passing an unknown one is not a soft failure: claude rejects it and the
// session never starts.
func ValidPermissionMode(mode string) bool {
	return slices.Contains(PermissionModes, mode)
}

// ResumeArgs returns the argv that brings an existing session back.
//
// The permission mode has to be repeated on every resume: it is a
// per-invocation flag, not conversation state, so a woken session would
// otherwise come back in the CLI's default and quietly drop whatever mode the
// user had chosen. An empty or unrecognised mode means DefaultPermissionMode.
func ResumeArgs(sessionID, mode string) []string {
	if !ValidPermissionMode(mode) {
		mode = DefaultPermissionMode
	}
	return []string{findClaude(), "--resume", sessionID, "--permission-mode", mode}
}

// sessionArgs builds the argv for a Claude session. Shared with the resume
// path so a woken session gets the same flags as a fresh one.
func sessionArgs(spec provider.SessionSpec) []string {
	argv := []string{"claude"}
	if spec.SessionID != "" {
		argv = append(argv, "--session-id", spec.SessionID)
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	// --dangerously-skip-permissions is not a permission mode: it bypasses the
	// hook chain the modes run through, so passing both is contradictory.
	switch {
	case spec.SkipPermissions:
		argv = append(argv, "--dangerously-skip-permissions")
	case ValidPermissionMode(spec.PermissionMode):
		argv = append(argv, "--permission-mode", spec.PermissionMode)
	default:
		argv = append(argv, "--permission-mode", DefaultPermissionMode)
	}
	// Last, and positional: anything after it would be read as more prompt.
	if spec.Prompt != "" {
		argv = append(argv, spec.Prompt)
	}
	return argv
}

// Register registers all Claude hook and action handlers.
func Register() {
	// Provider registration
	provider.RegisterProvider(
		provider.ProviderInfo{
			ID:   "claude",
			Name: "Claude Code",
			Icon: "terminal",
			Capabilities: provider.ProviderCapabilities{
				PromptQueue: true,
			},
			PermissionModes: PermissionModes,
		},
		// SessionBuilder
		sessionArgs,
		// ModelsFetcher
		func() ([]provider.ModelInfo, error) {
			return []provider.ModelInfo{
				{ID: "opus", Name: "Opus", Description: "Most capable model"},
				{ID: "sonnet", Name: "Sonnet", Description: "Fast and capable"},
				{ID: "haiku", Name: "Haiku", Description: "Fastest and most affordable"},
				{ID: "opus[1m]", Name: "Opus 1M", Description: "Opus with 1M context window", ContextWindow: "1M"},
				{ID: "sonnet[1m]", Name: "Sonnet 1M", Description: "Sonnet with 1M context window", ContextWindow: "1M"},
				{ID: "opusplan", Name: "Opus Plan", Description: "Opus plans, Sonnet executes"},
			}, nil
		},
	)

	// Hook handlers (type matches URL path: /hooks/claude/permission → "claude.permission")
	provider.RegisterHook("claude.permission", handlePermission)
	provider.RegisterHook("claude.question", handleQuestion)
	provider.RegisterHook("claude.elicitation", handleElicitation)
	provider.RegisterHook("claude.stop", handleStop)
	provider.RegisterHook("claude.stop.failure", handleStopFailure)
	provider.RegisterHook("claude.notification", handleNotification)
	provider.RegisterHook("claude.session.start", handleSessionStart)
	provider.RegisterHook("claude.session.end", handleSessionEnd)
	provider.RegisterHook("claude.prompt.submit", handlePromptSubmit)
	provider.RegisterHook("claude.tool.pre", handleToolPre)
	provider.RegisterHook("claude.tool.post", handleToolPost)
	provider.RegisterHook("claude.tool.post.failure", handleToolPostFailure)
	provider.RegisterHook("claude.compact.pre", handlePreCompact)
	provider.RegisterHook("claude.compact.post", handlePostCompact)
	provider.RegisterHook("claude.subagent.start", handleSubagentStart)
	provider.RegisterHook("claude.subagent.stop", handleSubagentStop)

	// Action handlers (type matches notification.type)
	provider.RegisterAction("claude.permission", handlePermissionAction)
	provider.RegisterAction("claude.question", handleQuestionAction)
	provider.RegisterAction("claude.elicitation.form", handleElicitationAction)
	provider.RegisterAction("claude.elicitation.url", handleElicitationAction)
	provider.RegisterAction("claude.trust", handleTrustAction)

	// Small model caller — runs claude CLI with haiku for lightweight text generation
	claudeBin := findClaude()
	provider.RegisterSmallModelCaller("claude", func(ctx context.Context, system, prompt string) (string, error) {
		cmd := exec.CommandContext(ctx, claudeBin,
			"--bare",
			"-p",
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
	})

	// Event types for reporter filtering
	provider.RegisterEventTypes("claude", []provider.EventTypeInfo{
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
	})

	// Slash commands available in the Claude CLI
	provider.RegisterCommands([]provider.Command{
		{Name: "/compact", Description: "Compact conversation context", Icon: "compress"},
		{Name: "/review", Description: "Review code changes", Icon: "rate_review"},
		{Name: "/cost", Description: "Show token usage & cost", Icon: "payments"},
		{Name: "/status", Description: "Show session status", Icon: "info"},
		{Name: "/doctor", Description: "Run health check", Icon: "health_and_safety"},
		{Name: "/memory", Description: "View & manage memory", Icon: "memory"},
		{Name: "/clear", Description: "Clear conversation history", Icon: "clear_all"},
		{Name: "/model", Description: "Switch model", Icon: "swap_horiz"},
	})
}
