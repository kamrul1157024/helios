package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/push"
	"github.com/kamrul1157024/helios/internal/store"
)

type hookInput struct {
	SessionID string          `json:"session_id"`
	CWD       string          `json:"cwd"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
}

type permissionResponse struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message,omitempty"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

func (s *InternalServer) handlePermissionHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Track session
	s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "PermissionRequest")

	// Create notification
	notifID := notifications.GenerateNotificationID()
	toolInput := string(input.ToolInput)
	detail := fmt.Sprintf("%s: %s", input.ToolName, summarizeToolInput(input.ToolInput))

	notif := &store.Notification{
		ID:              notifID,
		ClaudeSessionID: input.SessionID,
		CWD:             input.CWD,
		Type:            "permission",
		Status:          "pending",
		ToolName:        &input.ToolName,
		ToolInput:       &toolInput,
		Detail:          &detail,
	}

	if err := s.shared.Mgr.CreateNotification(notif); err != nil {
		http.Error(w, "failed to create notification", http.StatusInternalServerError)
		return
	}

	// Broadcast SSE
	s.shared.SSE.Broadcast(SSEEvent{
		Type: "notification",
		Data: notif,
	})

	// Desktop notification
	go sendDesktopNotification(detail)

	// Web Push notification
	if s.shared.Pusher != nil {
		go s.shared.Pusher.SendToAll(push.PushPayload{
			Type:  "permission",
			ID:    notifID,
			Title: "Claude needs permission",
			Body:  detail,
			Actions: []push.PushAction{
				{Action: "approve", Title: "Approve"},
				{Action: "deny", Title: "Deny"},
			},
		})
	}

	// Block and wait for decision (up to 5 minutes)
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()

	decisionCh := make(chan string, 1)
	go func() {
		decision, err := s.shared.Mgr.WaitForDecision(notifID)
		if err != nil {
			decisionCh <- "denied"
			return
		}
		decisionCh <- decision
	}()

	var decision string
	select {
	case decision = <-decisionCh:
	case <-timer.C:
		s.shared.Mgr.CancelPending(notifID)
		decision = "denied"
	case <-r.Context().Done():
		s.shared.Mgr.CancelPendingFromClaude(notifID)
		s.shared.SSE.Broadcast(SSEEvent{
			Type: "notification_resolved",
			Data: map[string]string{"id": notifID, "action": "dismissed", "source": "claude"},
		})
		return
	}

	// Build response
	resp := permissionResponse{}
	resp.HookSpecificOutput.HookEventName = "PermissionRequest"
	if decision == "approved" {
		resp.HookSpecificOutput.Decision.Behavior = "allow"
	} else {
		resp.HookSpecificOutput.Decision.Behavior = "deny"
		resp.HookSpecificOutput.Decision.Message = "Denied via helios"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *InternalServer) handleStopHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "Stop")

	notifID := notifications.GenerateNotificationID()
	lastDetail := s.shared.DB.LastSessionDetail(input.SessionID)
	detail := "Claude session completed"
	if lastDetail != "" {
		detail = "Session completed — last action: " + lastDetail
	}
	notif := &store.Notification{
		ID:              notifID,
		ClaudeSessionID: input.SessionID,
		CWD:             input.CWD,
		Type:            "done",
		Status:          "dismissed",
		Detail:          &detail,
	}
	s.shared.Mgr.CreateNotification(notif)

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_done",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *InternalServer) handleStopFailureHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "StopFailure")

	notifID := notifications.GenerateNotificationID()
	lastDetail := s.shared.DB.LastSessionDetail(input.SessionID)
	detail := "Claude session stopped with an error"
	if lastDetail != "" {
		detail = "Session error — last action: " + lastDetail
	}
	notif := &store.Notification{
		ID:              notifID,
		ClaudeSessionID: input.SessionID,
		CWD:             input.CWD,
		Type:            "error",
		Status:          "pending",
		Detail:          &detail,
	}
	s.shared.Mgr.CreateNotification(notif)

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_error",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *InternalServer) handleNotificationHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Just track session activity, don't create a notification — idle prompts are noise
	s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "Notification")

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *InternalServer) handleSessionStartHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "SessionStart")

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_created",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *InternalServer) handleSessionEndHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "SessionEnd")

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_done",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
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

func sendDesktopNotification(detail string) {
	if runtime.GOOS != "darwin" {
		return
	}
	script := fmt.Sprintf(`display notification "%s" with title "helios" subtitle "Claude needs permission"`, detail)
	exec.Command("osascript", "-e", script).Run()
}
