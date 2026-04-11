package claude

import (
	"github.com/kamrul1157024/helios/internal/provider"
)

// Register registers all Claude hook and action handlers.
func Register() {
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
