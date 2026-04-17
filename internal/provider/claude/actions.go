package claude

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

// tmuxClient is set by the daemon after shared state is initialized.
var tmuxClient tmux.TmuxClient

// SetTmux sets the tmux client for action handlers that need pane access.
func SetTmux(c tmux.TmuxClient) {
	tmuxClient = c
}

func handlePermissionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action          string                 `json:"action"`
		UpdatedInput    map[string]interface{} `json:"updated_input,omitempty"`
		ApplyPermission *int                   `json:"apply_permission,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	if req.Action == "deny" {
		return notifications.Decision{Status: "denied"}, nil
	}

	respData := map[string]interface{}{}
	if req.UpdatedInput != nil {
		respData["updated_input"] = req.UpdatedInput
	}
	if req.ApplyPermission != nil {
		respData["apply_permission"] = *req.ApplyPermission
	}
	var response json.RawMessage
	if len(respData) > 0 {
		response, _ = json.Marshal(respData)
	}

	return notifications.Decision{Status: "approved", Response: response}, nil
}

func handleQuestionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action  string            `json:"action"`
		Answers map[string]string `json:"answers"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	if req.Action == "skip" {
		return notifications.Decision{Status: "denied"}, nil
	}

	if len(req.Answers) == 0 {
		return notifications.Decision{}, fmt.Errorf("missing answers")
	}

	response, _ := json.Marshal(map[string]interface{}{"answers": req.Answers})
	return notifications.Decision{Status: "answered", Response: response}, nil
}

func handleElicitationAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action  string                 `json:"action"`
		Content map[string]interface{} `json:"content,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	if req.Action != "accept" && req.Action != "decline" && req.Action != "cancel" {
		return notifications.Decision{}, fmt.Errorf("action must be accept/decline/cancel")
	}

	status := "answered"
	if req.Action == "decline" || req.Action == "cancel" {
		status = "denied"
	}

	response, _ := json.Marshal(map[string]interface{}{
		"action":  req.Action,
		"content": req.Content,
	})
	return notifications.Decision{Status: status, Response: response}, nil
}

func handleTrustAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action string `json:"action"` // "trust" or "deny"
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	// Extract pane_id from the notification payload
	var payload struct {
		PaneID string `json:"pane_id"`
	}
	if notif.Payload != nil {
		json.Unmarshal([]byte(*notif.Payload), &payload)
	}

	if payload.PaneID == "" {
		return notifications.Decision{}, fmt.Errorf("missing pane_id in notification payload")
	}

	if tmuxClient == nil {
		return notifications.Decision{}, fmt.Errorf("tmux client not available")
	}

	if req.Action == "trust" {
		// Send "Yes, proceed" by pressing Enter (the trust dialog has Yes selected by default)
		if err := tmuxClient.SendKeysRaw(payload.PaneID, "Enter"); err != nil {
			log.Printf("trust-action: failed to send Enter to pane %s: %v", payload.PaneID, err)
			return notifications.Decision{}, fmt.Errorf("failed to send keys: %w", err)
		}
		log.Printf("trust-action: sent Enter to pane %s (trust approved)", payload.PaneID)
		return notifications.Decision{Status: "approved"}, nil
	}

	// Deny — send Ctrl+C to abort Claude
	if err := tmuxClient.SendKeysRaw(payload.PaneID, "C-c"); err != nil {
		log.Printf("trust-action: failed to send C-c to pane %s: %v", payload.PaneID, err)
	}
	return notifications.Decision{Status: "denied"}, nil
}
