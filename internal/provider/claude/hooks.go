package claude

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
	HookEventName         string          `json:"hook_event_name,omitempty"`
	MCPServerName         string          `json:"mcp_server_name,omitempty"`
	Message               string          `json:"message,omitempty"`
	Mode                  string          `json:"mode,omitempty"`
	RequestedSchema       json.RawMessage `json:"requested_schema,omitempty"`
	URL                   string          `json:"url,omitempty"`
	ElicitationID         string          `json:"elicitation_id,omitempty"`
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

	ctx.Notify("notification", notif)
	ctx.Push("claude.permission", notifID, "Claude needs permission", detail)

	decision := waitForDecision(ctx, notifID, r)
	if decision == nil {
		return
	}

	// Permission resolved — set session back to active
	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PermissionResolved")

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

	notifID := notifications.GenerateNotificationID()
	title := "Claude has a question"
	detail := "Answer required to continue"
	payloadStr := string(input.ToolInput)

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

	ctx.Notify("notification", notif)
	ctx.Push("claude.question", notifID, title, detail)

	decision := waitForDecision(ctx, notifID, r)
	if decision == nil {
		return
	}

	type preToolUseResponse struct {
		HookSpecificOutput struct {
			HookEventName      string                 `json:"hookEventName"`
			PermissionDecision string                 `json:"permissionDecision"`
			UpdatedInput       map[string]interface{} `json:"updatedInput,omitempty"`
		} `json:"hookSpecificOutput"`
	}

	resp := preToolUseResponse{}
	resp.HookSpecificOutput.HookEventName = "PreToolUse"

	if decision.Status == "answered" && len(decision.Response) > 0 {
		resp.HookSpecificOutput.PermissionDecision = "allow"
		var respData map[string]interface{}
		json.Unmarshal(decision.Response, &respData)

		var toolInput map[string]interface{}
		json.Unmarshal(input.ToolInput, &toolInput)
		for k, v := range respData {
			toolInput[k] = v
		}
		resp.HookSpecificOutput.UpdatedInput = toolInput
	} else {
		resp.HookSpecificOutput.PermissionDecision = "deny"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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

	ctx.Notify("notification", notif)
	ctx.Push(notifType, notifID, title, detail)

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

	// Resolve any pending notifications for this session (approved from CLI)
	resolvedIDs, _ := ctx.DB.ResolveSessionNotifications(input.SessionID, "resolved", "claude")
	for _, id := range resolvedIDs {
		ctx.Mgr.CancelPendingFromClaude(id)
		ctx.Notify("notification_resolved", map[string]string{"id": id, "action": "resolved", "source": "claude"})
	}

	notifID := notifications.GenerateNotificationID()
	lastDetail := ctx.DB.LastSessionDetail(input.SessionID)
	title := "Session completed"
	detail := "Claude session completed"
	if lastDetail != "" {
		detail = "Session completed — last action: " + lastDetail
	}
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

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "idle",
	})

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

	notifID := notifications.GenerateNotificationID()
	lastDetail := ctx.DB.LastSessionDetail(input.SessionID)
	title := "Session error"
	detail := "Claude session stopped with an error"
	if lastDetail != "" {
		detail = "Session error — last action: " + lastDetail
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
	ctx.Mgr.CreateNotification(notif)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "error",
	})

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
		ctx.Notify("session_status", map[string]interface{}{
			"session_id": input.SessionID,
			"status":     "idle",
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

	sess := &store.Session{
		SessionID:      input.SessionID,
		Source:         "claude",
		CWD:            input.CWD,
		TranscriptPath: transcriptPath,
		Model:          model,
		Status:         "idle",
		LastEvent:      strPtr("SessionStart"),
	}
	ctx.DB.UpsertSession(sess)

	// Remove from pending panes and associate the tmux pane with this session.
	var paneID string
	if ctx.RemovePendingPane != nil {
		paneID = ctx.RemovePendingPane(input.CWD)
	}
	if paneID != "" {
		ctx.DB.UpdateSessionTmuxPane(input.SessionID, paneID, 0)
	}

	sseData := map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "idle",
		"model":      input.Model,
	}
	if paneID != "" {
		sseData["tmux_pane"] = paneID
	}
	ctx.Notify("session_status", sseData)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleSessionEnd(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "ended", "SessionEnd")

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"cwd":        input.CWD,
		"status":     "ended",
	})

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

	if input.Message != "" {
		ctx.DB.UpdateSessionLastUserMessage(input.SessionID, input.Message)
	}

	ctx.Notify("session_status", map[string]interface{}{
		"session_id":        input.SessionID,
		"status":            "active",
		"last_user_message": input.Message,
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func handleToolPre(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx.DB.UpdateSessionStatus(input.SessionID, "active", "PreToolUse:"+input.ToolName)
	updateSessionTranscript(ctx, &input)

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "active",
		"last_event": "PreToolUse:" + input.ToolName,
	})

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

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "compacting",
	})

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

	ctx.Notify("session_status", map[string]interface{}{
		"session_id": input.SessionID,
		"status":     "active",
	})

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
