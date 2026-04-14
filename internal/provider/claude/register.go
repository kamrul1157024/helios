package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
)

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
		},
		// SessionBuilder
		func(prompt, model, cwd, sessionID string) string {
			cmd := "claude"
			if sessionID != "" {
				cmd += fmt.Sprintf(" --session-id %s", sessionID)
			}
			if model != "" {
				cmd += fmt.Sprintf(" --model %s", model)
			}
			cmd += fmt.Sprintf(" %q", prompt)
			return cmd
		},
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
	provider.RegisterSmallModelCaller("claude", func(ctx context.Context, system, prompt string) (string, error) {
		cmd := exec.CommandContext(ctx, "claude",
			"-p",
			"--bare",
			"--model", "haiku",
			"--tools", "",
			"--no-session-persistence",
			"--output-format", "json",
			"--system-prompt", system,
		)
		cmd.Stdin = strings.NewReader(prompt)

		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("claude cli: %w", err)
		}

		var result struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return "", fmt.Errorf("parse response: %w", err)
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
