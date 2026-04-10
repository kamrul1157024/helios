package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
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

func (s *Server) handlePermissionHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Track session
	s.db.UpsertHookSession(input.SessionID, input.CWD, "PermissionRequest")

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

	if err := s.mgr.CreateNotification(notif); err != nil {
		http.Error(w, "failed to create notification", http.StatusInternalServerError)
		return
	}

	// Broadcast SSE
	s.sse.Broadcast(SSEEvent{
		Type: "notification",
		Data: notif,
	})

	// Desktop notification
	go sendDesktopNotification(detail)

	// Block and wait for decision (up to 5 minutes)
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()

	decisionCh := make(chan string, 1)
	go func() {
		decision, err := s.mgr.WaitForDecision(notifID)
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
		s.mgr.CancelPending(notifID)
		decision = "denied"
	case <-r.Context().Done():
		s.mgr.CancelPending(notifID)
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

func (s *Server) handleStopHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.db.UpsertHookSession(input.SessionID, input.CWD, "Stop")

	notifID := notifications.GenerateNotificationID()
	notif := &store.Notification{
		ID:              notifID,
		ClaudeSessionID: input.SessionID,
		CWD:             input.CWD,
		Type:            "done",
		Status:          "dismissed",
	}
	s.mgr.CreateNotification(notif)

	s.sse.Broadcast(SSEEvent{
		Type: "session_done",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *Server) handleStopFailureHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.db.UpsertHookSession(input.SessionID, input.CWD, "StopFailure")

	notifID := notifications.GenerateNotificationID()
	notif := &store.Notification{
		ID:              notifID,
		ClaudeSessionID: input.SessionID,
		CWD:             input.CWD,
		Type:            "error",
		Status:          "pending",
	}
	s.mgr.CreateNotification(notif)

	s.sse.Broadcast(SSEEvent{
		Type: "session_error",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *Server) handleNotificationHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.db.UpsertHookSession(input.SessionID, input.CWD, "Notification")

	notifID := notifications.GenerateNotificationID()
	notif := &store.Notification{
		ID:              notifID,
		ClaudeSessionID: input.SessionID,
		CWD:             input.CWD,
		Type:            "idle",
		Status:          "pending",
	}
	s.mgr.CreateNotification(notif)

	s.sse.Broadcast(SSEEvent{
		Type: "notification",
		Data: notif,
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *Server) handleSessionStartHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.db.UpsertHookSession(input.SessionID, input.CWD, "SessionStart")

	s.sse.Broadcast(SSEEvent{
		Type: "session_created",
		Data: map[string]string{"session_id": input.SessionID, "cwd": input.CWD},
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

func (s *Server) handleSessionEndHook(w http.ResponseWriter, r *http.Request) {
	var input hookInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.db.UpsertHookSession(input.SessionID, input.CWD, "SessionEnd")

	s.sse.Broadcast(SSEEvent{
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
