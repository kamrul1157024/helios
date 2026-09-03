package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// terminalBackend is set by the daemon after shared state is initialized.
var terminalBackend backend.Backend

// SetBackend gives action handlers access to session terminals.
func SetBackend(b backend.Backend) {
	terminalBackend = b
}

// handlePermissionAction turns a remote surface's answer into a decision.
//
// The body is a permissionAnswer plus the action, so a phone can send anything
// the terminal can: the mode to continue a plan in, or the words to send it
// back with.
func handlePermissionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action string `json:"action"`
		permissionAnswer
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	if req.Action == "deny" {
		if text := strings.TrimSpace(req.Feedback); text != "" {
			return deniedWithFeedback(text), nil
		}
		return notifications.Decision{Status: "denied"}, nil
	}

	// Feedback is a refusal's payload; an approval carrying it would send Claude
	// a complaint about work it was just cleared to do.
	answer := req.permissionAnswer
	answer.Feedback = ""
	if answer.empty() {
		return notifications.Decision{Status: "approved"}, nil
	}
	response, err := json.Marshal(answer)
	if err != nil {
		return notifications.Decision{}, fmt.Errorf("encode response: %w", err)
	}
	return notifications.Decision{Status: "approved", Response: response}, nil
}

// handleQuestionAction turns a phone's answer into a decision. It types
// nothing: the hook that raised the question is still blocking, and resolving
// the notification is what hands the answer back to Claude.
func handleQuestionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req questionAnswer
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}
	if req.Action != "answer" && req.Action != "skip" {
		return notifications.Decision{}, fmt.Errorf("action must be answer/skip")
	}
	if req.Action == "skip" {
		return notifications.Decision{Status: "denied", Response: skipResponse()}, nil
	}
	if len(req.Selections) == 0 && req.Text == "" {
		return notifications.Decision{}, fmt.Errorf("missing selections")
	}

	response, err := json.Marshal(questionAnswer{Selections: req.Selections, Text: req.Text})
	if err != nil {
		return notifications.Decision{}, fmt.Errorf("encode response: %w", err)
	}
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

// handleErrorAction retries or dismisses a turn that died on an API error.
//
// Retry sends "continue", which is what a user types in the terminal after an
// API error: the CLI picks the turn up where it stopped rather than starting a
// new one.
func handleErrorAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action string `json:"action"` // "retry" or "dismiss"
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	if req.Action == "dismiss" {
		return notifications.Decision{Status: "dismissed"}, nil
	}
	if req.Action != "retry" {
		return notifications.Decision{}, fmt.Errorf("action must be retry/dismiss")
	}

	sessionID := notif.SourceSession
	if notif.Payload != nil {
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(*notif.Payload), &payload); err == nil && payload.SessionID != "" {
			sessionID = payload.SessionID
		}
	}
	if sessionID == "" {
		return notifications.Decision{}, fmt.Errorf("missing session_id in notification payload")
	}

	// Reject rather than resolve when there is nothing to type into: a
	// notification consumed by a send that went nowhere is unrecoverable. A
	// dead terminal needs the wake path in handleSessionSend, which the
	// composer reaches.
	if terminalBackend == nil || !terminalBackend.Alive(sessionID) {
		return notifications.Decision{}, fmt.Errorf("session %s has no live terminal", sessionID)
	}

	if err := terminalBackend.SendText(sessionID, "continue"); err != nil {
		return notifications.Decision{}, fmt.Errorf("send continue to session %s: %w", sessionID, err)
	}
	log.Printf("error-action: retried session %s", sessionID)
	return notifications.Decision{Status: "approved"}, nil
}

// trustAffirmative is the wording of the "yes" option in Claude's workspace
// trust dialog. Matched case-insensitively as a substring, so the surrounding
// copy can change without breaking the answer.
const trustAffirmative = "trust this folder"

func handleTrustAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action string `json:"action"` // "trust" or "deny"
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	var payload struct {
		SessionID string `json:"session_id"`
	}
	if notif.Payload != nil {
		if err := json.Unmarshal([]byte(*notif.Payload), &payload); err != nil {
			return notifications.Decision{}, fmt.Errorf("invalid payload: %w", err)
		}
	}

	if payload.SessionID == "" {
		return notifications.Decision{}, fmt.Errorf("missing session_id in notification payload")
	}
	if terminalBackend == nil {
		return notifications.Decision{}, fmt.Errorf("terminal backend not available")
	}

	if req.Action == "trust" {
		// The affirmative option is found on screen, not assumed to be the
		// default. It used to be, and a bare Return was sent: Claude has since
		// made "No, exit" the default, so approving trust from a phone quit
		// the agent instead. Measured 2026-08-29 against Claude Code 2.1.x.
		if err := provider.ConfirmChoice(terminalBackend, payload.SessionID, trustAffirmative); err != nil {
			return notifications.Decision{}, fmt.Errorf("approve trust for %s: %w", payload.SessionID, err)
		}
		log.Printf("trust-action: approved trust for session %s", payload.SessionID)
		return notifications.Decision{Status: "approved"}, nil
	}

	// Deny — interrupt the agent rather than answering the dialog.
	if err := terminalBackend.SendKey(payload.SessionID, backend.KeyCtrlC); err != nil {
		log.Printf("trust-action: failed to interrupt session %s: %v", payload.SessionID, err)
	}
	return notifications.Decision{Status: "denied"}, nil
}
