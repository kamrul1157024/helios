package claude

import (
	"encoding/json"
	"fmt"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

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
