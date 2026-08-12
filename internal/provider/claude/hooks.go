package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

type hookInput struct {
	SessionID             string          `json:"session_id"`
	CWD                   string          `json:"cwd"`
	TranscriptPath        string          `json:"transcript_path,omitempty"`
	Model                 string          `json:"model,omitempty"`
	ToolName              string          `json:"tool_name,omitempty"`
	ToolInput             json.RawMessage `json:"tool_input,omitempty"`
	PermissionMode        string          `json:"permission_mode,omitempty"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
	HookEventName         string          `json:"hook_event_name,omitempty"`
	MCPServerName         string          `json:"mcp_server_name,omitempty"`
	Message               string          `json:"message,omitempty"`
	Mode                  string          `json:"mode,omitempty"`
	RequestedSchema       json.RawMessage `json:"requested_schema,omitempty"`
	URL                   string          `json:"url,omitempty"`
	ElicitationID         string          `json:"elicitation_id,omitempty"`
	// Reason is populated by StopFailure when the CLI reports why the turn
	// ended. It is absent in Claude Code v2.1.x, hence the transcript fallback
	// in lastAPIError.
	Reason string `json:"reason,omitempty"`
	// Subagent fields
	AgentID     string `json:"agent_id,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	Description string `json:"description,omitempty"`
}

// ==================== Permission Hook ====================

func handlePermission(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "waiting_permission", "PermissionRequest")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "waiting_permission", input.CWD)

	notifID := notifications.GenerateNotificationID()
	detail := fmt.Sprintf("%s: %s", input.ToolName, summarizeToolInput(input.ToolInput))

	payload := map[string]interface{}{
		"tool_name":  input.ToolName,
		"tool_input": json.RawMessage(input.ToolInput),
	}
	if len(input.PermissionSuggestions) > 0 {
		payload["permission_suggestions"] = json.RawMessage(input.PermissionSuggestions)
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)

	notif := &store.Notification{
		ID:            notifID,
		Source:        "claude",
		SourceSession: input.SessionID,
		CWD:           input.CWD,
		Type:          "claude.permission",
		Status:        "pending",
		Title:         &input.ToolName,
		Detail:        &detail,
		Payload:       &payloadStr,
	}

	if err := ctx.Mgr.CreateNotification(notif); err != nil {
		http.Error(w, "failed to create notification", http.StatusInternalServerError)
		return
	}

	// Reserve the decision slot before publishing, so a client that answers
	// immediately cannot beat this handler to WaitForDecision.
	ctx.Mgr.Register(notifID)

	ctx.Notify("notification", notif)

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "permission",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			ToolName:  input.ToolName,
			ToolInput: summarizeToolInput(input.ToolInput),
		})
	}

	decision := waitForDecision(ctx, notifID, r)
	if decision == nil {
		return
	}

	// Permission resolved — set session back to active
	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PermissionResolved")
	renameSessionWindow(ctx, input.SessionID, "active", input.CWD)

	type permResponse struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
			Decision      struct {
				Behavior           string                 `json:"behavior"`
				Message            string                 `json:"message,omitempty"`
				UpdatedInput       map[string]interface{} `json:"updatedInput,omitempty"`
				UpdatedPermissions json.RawMessage        `json:"updatedPermissions,omitempty"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}

	resp := permResponse{}
	resp.HookSpecificOutput.HookEventName = "PermissionRequest"

	if decision.Status == "approved" {
		resp.HookSpecificOutput.Decision.Behavior = "allow"

		if len(decision.Response) > 0 {
			var respData struct {
				UpdatedInput    map[string]interface{} `json:"updated_input,omitempty"`
				ApplyPermission *int                   `json:"apply_permission,omitempty"`
			}
			if json.Unmarshal(decision.Response, &respData) == nil {
				if respData.UpdatedInput != nil {
					resp.HookSpecificOutput.Decision.UpdatedInput = respData.UpdatedInput
				}
				if respData.ApplyPermission != nil {
					var p map[string]json.RawMessage
					if json.Unmarshal(payloadJSON, &p) == nil {
						if sugRaw, ok := p["permission_suggestions"]; ok {
							var suggestions []json.RawMessage
							if json.Unmarshal(sugRaw, &suggestions) == nil {
								idx := *respData.ApplyPermission
								if idx >= 0 && idx < len(suggestions) {
									resp.HookSpecificOutput.Decision.UpdatedPermissions =
										json.RawMessage("[" + string(suggestions[idx]) + "]")
								}
							}
						}
					}
				}
			}
		}
	} else {
		resp.HookSpecificOutput.Decision.Behavior = "deny"
		resp.HookSpecificOutput.Decision.Message = "Denied via helios"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ==================== AskUserQuestion Hook ====================

func handleQuestion(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "waiting_permission", "AskUserQuestion")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "waiting_permission", input.CWD)

	notifID := notifications.GenerateNotificationID()
	title := "Claude has a question"
	detail := "Answer required to continue"

	// input.ToolInput is already {"questions": [...]}; splice session_id into it
	// rather than wrapping, which would nest questions under itself and break
	// the mobile card's payload['questions'] accessor.
	payload := map[string]json.RawMessage{}
	if err := json.Unmarshal(input.ToolInput, &payload); err != nil {
		payload = map[string]json.RawMessage{}
	}
	sessionJSON, err := json.Marshal(input.SessionID)
	if err != nil {
		http.Error(w, "failed to encode session id", http.StatusInternalServerError)
		return
	}
	payload["session_id"] = sessionJSON
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "failed to encode question payload", http.StatusInternalServerError)
		return
	}
	payloadStr := string(payloadJSON)

	notif := &store.Notification{
		ID:            notifID,
		Source:        "claude",
		SourceSession: input.SessionID,
		CWD:           input.CWD,
		Type:          "claude.question",
		Status:        "pending",
		Title:         &title,
		Detail:        &detail,
		Payload:       &payloadStr,
	}

	if err := ctx.Mgr.CreateNotification(notif); err != nil {
		http.Error(w, "failed to create notification", http.StatusInternalServerError)
		return
	}

	// No Register: nothing waits on a decision channel for questions any more.
	// Resolution comes from handleQuestionAction via Mgr.Resolve, or from the
	// PostToolUse / idle_prompt paths, neither of which needs a reserved slot.

	ctx.Notify("notification", notif)

	if ctx.Report != nil {
		questionText := ""
		var qi map[string]interface{}
		if json.Unmarshal(input.ToolInput, &qi) == nil {
			if q, ok := qi["question"].(string); ok {
				questionText = q
			}
		}
		if questionText == "" {
			questionText = "Answer required to continue"
		}
		ctx.Report(provider.ReportEvent{
			Type:      "question",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			Message:   questionText,
		})
	}

	// Deliberately no decision and no block. Returning an empty object lets the
	// CLI run AskUserQuestion and render its own UI, which is the only way the
	// terminal user can answer at all. The phone answers by driving that same
	// UI — see handleQuestionAction.
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// ==================== Elicitation Hook ====================

func handleElicitation(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "waiting_permission", "Elicitation")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "waiting_permission", input.CWD)

	notifID := notifications.GenerateNotificationID()
	title := fmt.Sprintf("%s needs input", input.MCPServerName)
	detail := input.Message

	notifType := "claude.elicitation.form"
	if input.Mode == "url" {
		notifType = "claude.elicitation.url"
	}

	payload := map[string]interface{}{
		"mcp_server_name": input.MCPServerName,
		"message":         input.Message,
		"mode":            input.Mode,
	}
	if len(input.RequestedSchema) > 0 {
		payload["requested_schema"] = json.RawMessage(input.RequestedSchema)
	}
	if input.URL != "" {
		payload["url"] = input.URL
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)

	notif := &store.Notification{
		ID:            notifID,
		Source:        "claude",
		SourceSession: input.SessionID,
		CWD:           input.CWD,
		Type:          notifType,
		Status:        "pending",
		Title:         &title,
		Detail:        &detail,
		Payload:       &payloadStr,
	}

	if err := ctx.Mgr.CreateNotification(notif); err != nil {
		http.Error(w, "failed to create notification", http.StatusInternalServerError)
		return
	}

	// Reserve the decision slot before publishing, so a client that answers
	// immediately cannot beat this handler to WaitForDecision.
	ctx.Mgr.Register(notifID)

	ctx.Notify("notification", notif)

	decision := waitForDecision(ctx, notifID, r)
	if decision == nil {
		return
	}

	type elicitationResponse struct {
		HookSpecificOutput struct {
			HookEventName string                 `json:"hookEventName"`
			Action        string                 `json:"action"`
			Content       map[string]interface{} `json:"content,omitempty"`
		} `json:"hookSpecificOutput"`
	}

	resp := elicitationResponse{}
	resp.HookSpecificOutput.HookEventName = "Elicitation"

	if len(decision.Response) > 0 {
		var respData struct {
			Action  string                 `json:"action"`
			Content map[string]interface{} `json:"content,omitempty"`
		}
		if json.Unmarshal(decision.Response, &respData) == nil {
			resp.HookSpecificOutput.Action = respData.Action
			resp.HookSpecificOutput.Content = respData.Content
		} else {
			resp.HookSpecificOutput.Action = "decline"
		}
	} else {
		resp.HookSpecificOutput.Action = "decline"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ==================== Status Hooks ====================

func handleStop(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "idle", "Stop")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "idle", input.CWD)

	// Resolve any pending notifications for this session (approved from CLI)
	resolvedIDs, _ := ctx.DB.ResolveSessionNotifications(input.SessionID, "resolved", "claude")
	for _, id := range resolvedIDs {
		ctx.Mgr.CancelPendingFromClaude(id)
		ctx.Notify("notification_resolved", map[string]string{"id": id, "action": "resolved", "source": "claude"})
	}

	notifID := notifications.GenerateNotificationID()
	lastDetail := ctx.DB.LastSessionDetail(input.SessionID)
	sess, _ := ctx.DB.GetSession(input.SessionID)
	title := "Session completed"
	detail := sessionContext(input.CWD, sess)
	notif := &store.Notification{
		ID:            notifID,
		Source:        "claude",
		SourceSession: input.SessionID,
		CWD:           input.CWD,
		Type:          "claude.done",
		Status:        "dismissed",
		Title:         &title,
		Detail:        &detail,
	}
	ctx.Mgr.CreateNotification(notif)
	ctx.Notify("notification", notif)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "idle",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "stop",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			Detail:    lastDetail,
		})
	}

	TriggerAutoTitle(ctx, input.SessionID, input.CWD, input.TranscriptPath, ctx.Notify)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleStopFailure(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "error", "StopFailure")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "error", input.CWD)

	notifID := notifications.GenerateNotificationID()
	lastDetail := ctx.DB.LastSessionDetail(input.SessionID)
	sess, _ := ctx.DB.GetSession(input.SessionID)

	errText := lastAPIError(input.TranscriptPath)
	if input.Reason != "" {
		errText = input.Reason
	}
	rate := classifyAPIError(errText)

	title := "Session error"
	if rate.IsRateLimit {
		title = "Rate limit reached"
	}
	// Detail is the error itself. It used to be sessionContext(), which showed
	// the user their own last prompt back and said nothing about what broke.
	detail := errText
	if detail == "" {
		detail = sessionContext(input.CWD, sess)
	}

	payload := map[string]interface{}{
		"session_id":    input.SessionID,
		"error":         errText,
		"is_rate_limit": rate.IsRateLimit,
		"retryable":     true,
	}
	if rate.ResetAt != nil {
		payload["reset_at"] = rate.ResetAt.Format(time.RFC3339)
	}

	notif := &store.Notification{
		ID:            notifID,
		Source:        "claude",
		SourceSession: input.SessionID,
		CWD:           input.CWD,
		Type:          "claude.error",
		Status:        "pending",
		Title:         &title,
		Detail:        &detail,
	}
	if raw, err := json.Marshal(payload); err != nil {
		log.Printf("claude: marshal error payload for %s: %v", input.SessionID, err)
	} else {
		s := string(raw)
		notif.Payload = &s
	}

	if err := ctx.Mgr.CreateNotification(notif); err != nil {
		log.Printf("claude: create error notification for %s: %v", input.SessionID, err)
	} else {
		// Unlike a permission hook there is no CLI request waiting on the
		// answer — the turn is already over. Register only so Resolve has
		// somewhere to deliver, which is what lets the notification clear.
		ctx.Mgr.Register(notifID)
	}
	ctx.Notify("notification", notif)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "error",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "stop_failure",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			Detail:    lastDetail,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleNotification(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updateSessionTranscript(ctx, &input)

	// idle_prompt fires when Claude returns to its input prompt (e.g. after
	// the user interrupts with Escape/Ctrl+C). Claude does not fire a Stop
	// hook for interrupts, so we transition to idle here.
	if input.HookEventName == "idle_prompt" {
		ctx.DB.UpdateSessionStatus(input.SessionID, "idle", "IdlePrompt")
		renameSessionWindow(ctx, input.SessionID, "idle", input.CWD)
		ctx.Notify("session_status", map[string]interface{}{
			"session_id": input.SessionID,
			"status":     "idle",
		})
		// Escaping out of a question returns Claude to its prompt without a
		// PostToolUse, so this is the only signal that the question is gone.
		// The phone must stop offering to answer it.
		resolveSessionQuestions(ctx, input.SessionID)
	}

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "notification",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			Message:   input.HookEventName,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleSessionStart(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var transcriptPath *string
	if input.TranscriptPath != "" {
		transcriptPath = &input.TranscriptPath
	}
	var model *string
	if input.Model != "" {
		model = &input.Model
	}

	// A session helios launched already has a terminal under the same ID; one
	// the user started by hand does not, and stays unmanaged.
	handle := ""
	if ctx.Terminal != nil {
		handle, _ = ctx.Terminal.Handle(input.SessionID)
	}
	// The agent has reported in, so the trust-dialog watcher can stop.
	if ctx.SessionStarted != nil {
		ctx.SessionStarted(input.SessionID)
	}

	sess := &store.Session{
		SessionID:      input.SessionID,
		Source:         "claude",
		CWD:            input.CWD,
		TranscriptPath: transcriptPath,
		Model:          model,
		Status:         "idle",
		LastEvent:      strPtr("SessionStart"),
		Managed:        handle != "",
	}
	ctx.DB.UpsertSession(sess)
	updateSessionPermissionMode(ctx, &input)

	// Relabel the terminal now that the session is registered.
	renameSessionWindow(ctx, input.SessionID, "idle", input.CWD)

	sseData := map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "idle",
		"model":      input.Model,
	}
	if handle != "" {
		sseData["terminal"] = handle
	}
	ctx.Notify("session_status", sseData)

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "session_start",
			SessionID: input.SessionID,
			CWD:       input.CWD,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleSessionEnd(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// SessionEnd fires on process exit AND on /clear. Drop the terminal
	// mapping and mark terminated — the reaper is the safety net for crashes.
	// For /clear, SessionStart fires immediately after and re-registers.
	if ctx.Terminal != nil {
		ctx.Terminal.Forget(input.SessionID)
	}
	ctx.DB.UpdateSessionStatus(input.SessionID, "terminated", "SessionEnd")
	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "terminated",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "session_end",
			SessionID: input.SessionID,
			CWD:       input.CWD,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// ==================== Helpers ====================

func waitForDecision(ctx *provider.HookContext, notifID string, r *http.Request) *notifications.Decision {
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()

	decisionCh := make(chan notifications.Decision, 1)
	go func() {
		decision, err := ctx.Mgr.WaitForDecision(notifID)
		if err != nil {
			decisionCh <- notifications.Decision{Status: "denied"}
			return
		}
		decisionCh <- decision
	}()

	select {
	case decision := <-decisionCh:
		return &decision
	case <-timer.C:
		ctx.Mgr.CancelPending(notifID)
		denied := notifications.Decision{Status: "denied"}
		return &denied
	case <-r.Context().Done():
		ctx.Mgr.CancelPendingFromClaude(notifID)
		ctx.Notify("notification_resolved", map[string]string{"id": notifID, "action": "resolved", "source": "claude"})
		return nil
	}
}

// ==================== Activity Hooks ====================

func handlePromptSubmit(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "UserPromptSubmit")
	updateSessionTranscript(ctx, &input)
	updateSessionPermissionMode(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "active", input.CWD)

	if input.Message != "" {
		ctx.DB.UpdateSessionLastUserMessage(input.SessionID, input.Message)
	}

	ctx.Notify("session_status", map[string]interface{}{
		"session_id":        input.SessionID,
		"status":            "active",
		"last_user_message": input.Message,
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "prompt_submit",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			Message:   input.Message,
		})
	}

	// Last, so a caller woken by this has the status and the message already
	// written rather than racing them.
	if ctx.PromptSubmitted != nil {
		ctx.PromptSubmitted(input.SessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// askUserQuestionTool is the tool whose PreToolUse has a dedicated handler.
const askUserQuestionTool = "AskUserQuestion"

// resolveSessionQuestions clears a session's pending claude.question
// notifications and tells clients to drop them, whichever surface answered.
//
// Narrowed to the one type rather than reusing ResolveSessionNotifications,
// which would also clear a permission request the session is still waiting on.
func resolveSessionQuestions(ctx *provider.HookContext, sessionID string) {
	ids, err := ctx.DB.ResolveSessionNotificationsByType(sessionID, "claude.question", "resolved", "claude")
	if err != nil {
		log.Printf("hook: resolve questions for %s: %v", sessionID, err)
		return
	}
	for _, id := range ids {
		ctx.Notify("notification_resolved", map[string]string{"id": id, "action": "resolved", "source": "claude"})
	}
}

func handleToolPre(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// AskUserQuestion has a dedicated hook that sets waiting_permission. The
	// "*" PreToolUse matcher fires for it too, and writing "active" here is
	// what used to erase that status a millisecond after it was set.
	if input.ToolName == askUserQuestionTool {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PreToolUse:"+input.ToolName)
	updateSessionTranscript(ctx, &input)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "active",
		"last_event": "PreToolUse:" + input.ToolName,
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "tool_pre",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			ToolName:  input.ToolName,
			ToolInput: summarizeToolInput(input.ToolInput),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleToolPost(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PostToolUse:"+input.ToolName)
	updateSessionTranscript(ctx, &input)

	// The tool finished, so the question is answered. Retract the notification
	// on every other surface. Which side answered does not matter.
	if input.ToolName == askUserQuestionTool {
		resolveSessionQuestions(ctx, input.SessionID)
	}

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "tool_post",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			ToolName:  input.ToolName,
			ToolInput: summarizeToolInput(input.ToolInput),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleToolPostFailure(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PostToolUseFailure:"+input.ToolName)
	updateSessionTranscript(ctx, &input)

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "tool_post_failure",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			ToolName:  input.ToolName,
			ToolInput: summarizeToolInput(input.ToolInput),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// ==================== Compaction Hooks ====================

func handlePreCompact(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "compacting", "PreCompact")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "compacting", input.CWD)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "compacting",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "compact_pre",
			SessionID: input.SessionID,
			CWD:       input.CWD,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handlePostCompact(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PostCompact")
	updateSessionTranscript(ctx, &input)
	renameSessionWindow(ctx, input.SessionID, "active", input.CWD)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "active",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "compact_post",
			SessionID: input.SessionID,
			CWD:       input.CWD,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// ==================== Subagent Hooks ====================

func handleSubagentStart(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sub := &store.Subagent{
		AgentID:         input.AgentID,
		ParentSessionID: input.SessionID,
		Status:          "active",
	}
	if input.AgentType != "" {
		sub.AgentType = &input.AgentType
	}
	if input.Description != "" {
		sub.Description = &input.Description
	}
	ctx.DB.CreateSubagent(sub)

	ctx.Notify("subagent_status", map[string]interface{}{
		"agent_id":          input.AgentID,
		"parent_session_id": input.SessionID,
		"agent_type":        input.AgentType,
		"description":       input.Description,
		"status":            "active",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "subagent_start",
			SessionID: input.SessionID,
			CWD:       input.CWD,
			AgentType: input.AgentType,
			Detail:    input.Description,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleSubagentStop(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSubagentStatus(input.AgentID, "completed")

	ctx.Notify("subagent_status", map[string]interface{}{
		"agent_id":          input.AgentID,
		"parent_session_id": input.SessionID,
		"status":            "completed",
	})

	if ctx.Report != nil {
		ctx.Report(provider.ReportEvent{
			Type:      "subagent_stop",
			SessionID: input.SessionID,
			CWD:       input.CWD,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// ==================== Helpers ====================

// updateSessionTranscript updates the transcript_path if provided in the hook input.
func updateSessionTranscript(ctx *provider.HookContext, input *hookInput) {
	if input.TranscriptPath != "" {
		ctx.DB.UpdateSessionTranscriptPath(input.SessionID, input.TranscriptPath)
	}
}

// updateSessionPermissionMode records the mode the agent says it is in.
//
// This is the only authoritative source: the user can change the mode with
// Shift+Tab in the terminal at any time, and nothing else tells us. Waking a
// cold session replays the stored mode, so a stale value here would silently
// undo a switch the user made by hand.
func updateSessionPermissionMode(ctx *provider.HookContext, input *hookInput) {
	if !ValidPermissionMode(input.PermissionMode) {
		return
	}
	if err := ctx.DB.UpdateSessionPermissionMode(input.SessionID, input.PermissionMode); err != nil {
		log.Printf("claude: record permission mode for %s: %v", input.SessionID, err)
	}
}

// renameSessionWindow relabels a session's terminal to match its status.
func renameSessionWindow(ctx *provider.HookContext, sessionID, status, cwd string) {
	if ctx.Terminal == nil || !ctx.Terminal.Alive(sessionID) {
		return
	}
	sess, _ := ctx.DB.GetSession(sessionID)
	label := ""
	if sess != nil {
		label = sess.Label(30)
	}
	if err := ctx.Terminal.Rename(sessionID, backend.DisplayName(status, cwd, label)); err != nil {
		log.Printf("hook: rename terminal for %s: %v", sessionID, err)
	}
}

// killSessionWindow terminates a session's terminal.
func killSessionWindow(ctx *provider.HookContext, sessionID string) {
	if ctx.Terminal == nil {
		return
	}
	if err := ctx.Terminal.Kill(sessionID); err != nil {
		log.Printf("hook: kill terminal for %s: %v", sessionID, err)
	}
}

func strPtr(s string) *string {
	return &s
}

func summarizeToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	if cmd, ok := m["command"].(string); ok {
		if len(cmd) > 100 {
			return cmd[:100] + "..."
		}
		return cmd
	}
	if len(raw) > 100 {
		return string(raw[:100]) + "..."
	}
	return string(raw)
}

// sessionContext builds the notification body from cwd and session info.
// Format: "opal-app: Fix auth bug" (title) or "opal-app: can you fix login" (last user message)
func sessionContext(cwd string, sess *store.Session) string {
	project := filepath.Base(cwd)
	if sess != nil {
		if label := sess.Label(80); label != "" {
			return fmt.Sprintf("%s: %s", project, label)
		}
	}
	return project
}
