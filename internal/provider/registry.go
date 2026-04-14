package provider

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

// ReportEvent is a narration event passed to the Reporter.
// Defined here to avoid circular imports (reporter imports provider).
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

// HookContext provides everything a hook handler needs without importing server.
type HookContext struct {
	DB               *store.Store
	Mgr              *notifications.Manager
	Tmux             *tmux.Client
	Notify           func(eventType string, data interface{}) // SSE broadcast
	RemovePendingPane func(cwd string) string                 // remove pane from pending map by CWD, returns pane ID
	Report           func(event ReportEvent)                     // push event to Reporter for narration
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

// ==================== Provider Registry ====================

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ContextWindow string `json:"context_window,omitempty"`
}

// ProviderCapabilities describes what a provider supports.
type ProviderCapabilities struct {
	PromptQueue bool `json:"prompt_queue"` // supports queuing prompts while active via tmux send-keys
}

// ProviderInfo describes a registered session provider.
type ProviderInfo struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Icon         string               `json:"icon"`
	Capabilities ProviderCapabilities `json:"capabilities"`
}

// SessionBuilder builds the shell command to start a new session.
type SessionBuilder func(prompt, model, cwd, sessionID string) string

// ModelsFetcher returns available models for the provider.
type ModelsFetcher func() ([]ModelInfo, error)

var providers = map[string]ProviderInfo{}
var sessionBuilders = map[string]SessionBuilder{}
var modelsFetchers = map[string]ModelsFetcher{}

// RegisterProvider registers a provider with its session builder and models fetcher.
func RegisterProvider(info ProviderInfo, builder SessionBuilder, fetcher ModelsFetcher) {
	providers[info.ID] = info
	sessionBuilders[info.ID] = builder
	modelsFetchers[info.ID] = fetcher
}

// GetProviders returns all registered providers.
func GetProviders() []ProviderInfo {
	result := make([]ProviderInfo, 0, len(providers))
	for _, p := range providers {
		result = append(result, p)
	}
	return result
}

// GetSessionBuilder returns the session builder for a provider, or nil.
func GetSessionBuilder(providerID string) SessionBuilder {
	return sessionBuilders[providerID]
}

// GetModelsFetcher returns the models fetcher for a provider, or nil.
func GetModelsFetcher(providerID string) ModelsFetcher {
	return modelsFetchers[providerID]
}

// GetCapabilities returns the capabilities for a provider.
func GetCapabilities(providerID string) ProviderCapabilities {
	if p, ok := providers[providerID]; ok {
		return p.Capabilities
	}
	return ProviderCapabilities{}
}

// ==================== Small Model Caller ====================

// SmallModelCaller runs a provider's cheapest model for short text generation.
// Used for narration, summarization, and other lightweight AI calls.
// Implementations should use the provider's CLI to respect the user's existing auth.
type SmallModelCaller func(ctx context.Context, system, prompt string) (string, error)

var smallModelCallers = map[string]SmallModelCaller{}

// RegisterSmallModelCaller registers a small model caller for a provider.
func RegisterSmallModelCaller(providerID string, caller SmallModelCaller) {
	smallModelCallers[providerID] = caller
}

// GetSmallModelCaller returns the small model caller for a provider, or nil.
func GetSmallModelCaller(providerID string) SmallModelCaller {
	return smallModelCallers[providerID]
}

// ==================== Event Types ====================

// EventTypeInfo describes a reportable event type from a provider.
type EventTypeInfo struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"` // "tools", "actions", "lifecycle", "context", "subagents", "other"
}

var eventTypes = map[string][]EventTypeInfo{}

// RegisterEventTypes registers event types for a provider.
func RegisterEventTypes(providerID string, types []EventTypeInfo) {
	eventTypes[providerID] = types
}

// GetAllEventTypes returns all registered event types grouped by provider.
func GetAllEventTypes() map[string][]EventTypeInfo {
	return eventTypes
}
