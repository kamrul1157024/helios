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

	// Action handlers (type matches notification.type)
	provider.RegisterAction("claude.permission", handlePermissionAction)
	provider.RegisterAction("claude.question", handleQuestionAction)
	provider.RegisterAction("claude.elicitation.form", handleElicitationAction)
	provider.RegisterAction("claude.elicitation.url", handleElicitationAction)
}
