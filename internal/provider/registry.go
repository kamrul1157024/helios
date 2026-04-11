package provider

import (
	"encoding/json"
	"net/http"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// HookContext provides everything a hook handler needs without importing server.
type HookContext struct {
	DB     *store.Store
	Mgr    *notifications.Manager
	Notify func(eventType string, data interface{})  // SSE broadcast
	Push   func(notifType, id, title, body string)    // push notification
}

// HookHandler processes an incoming hook request and writes the response.
type HookHandler func(ctx *HookContext, w http.ResponseWriter, r *http.Request, input json.RawMessage)

// ActionHandler processes a user action for a specific notification type.
type ActionHandler func(notif *store.Notification, body json.RawMessage) (notifications.Decision, error)

var hookHandlers = map[string]HookHandler{}
var actionHandlers = map[string]ActionHandler{}

// RegisterHook registers a hook handler for a given type (e.g. "claude.permission").
func RegisterHook(hookType string, handler HookHandler) {
	hookHandlers[hookType] = handler
}

// RegisterAction registers an action handler for a given notification type.
func RegisterAction(notifType string, handler ActionHandler) {
	actionHandlers[notifType] = handler
}

// GetHookHandler returns the hook handler for a type, or nil.
func GetHookHandler(hookType string) HookHandler {
	return hookHandlers[hookType]
}

// GetActionHandler returns the action handler for a type, or nil.
func GetActionHandler(notifType string) ActionHandler {
	return actionHandlers[notifType]
}

// Command represents a slash command available in a provider's CLI.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

var commands []Command

// RegisterCommands registers slash commands for a provider.
func RegisterCommands(cmds []Command) {
	commands = append(commands, cmds...)
}

// GetCommands returns all registered slash commands.
func GetCommands() []Command {
	return commands
}
