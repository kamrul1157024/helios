package codex

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

// ActionRoutes carries the clients' labels alongside the handlers, so the
// notification catalogue is served rather than hardcoded once per client.
//
// Four of Claude's six types have no Codex equivalent. There is no question
// card because Codex has no AskUserQuestion tool, no elicitation card because
// it has no Elicitation hook event, and no error-retry card because it reports
// no StopFailure. Absent is the honest representation; a card that cannot be
// answered would be worse.
func (p *Provider) ActionRoutes() map[string]provider.ActionRoute {
	return map[string]provider.ActionRoute{
		"codex.permission": {
			Handler:  handlePermissionAction,
			Label:    "Permission requests",
			Detail:   "Codex is asking to run something that needs your approval.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"codex.trust": {
			Handler:  handleTrustAction,
			Label:    "Directory trust",
			Detail:   "Codex is asking to trust this directory.",
			Blocking: true, Group: "action_required", DefaultAlert: true,
		},
		"codex.done": {
			Handler:  handleDoneAction,
			Label:    "Turn completed",
			Detail:   "Codex finished a turn.",
			Blocking: false, Group: "info", DefaultAlert: true,
		},
	}
}

// handlePermissionAction turns a remote surface's answer into a decision.
//
// A refusal may carry words, the way the terminal overlay's text field does.
// An approval that carried them would send Codex a complaint about work it was
// just cleared to do, so they are read on the deny path only.
func handlePermissionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action string `json:"action"`
		permissionAnswer
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}
	switch req.Action {
	case "approve":
		return notifications.Decision{Status: "approved"}, nil
	case "deny":
		if text := strings.TrimSpace(req.Feedback); text != "" {
			return deniedWithFeedback(text), nil
		}
		return notifications.Decision{Status: "denied"}, nil
	default:
		return notifications.Decision{}, fmt.Errorf("action must be approve/deny")
	}
}

// handleDoneAction dismisses a completion notice. It exists so the type has a
// route and therefore a catalogue entry; there is nothing to decide.
func handleDoneAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	return notifications.Decision{Status: "dismissed"}, nil
}

// handleTrustAction answers Codex's directory-trust dialog by keystroke,
// because no hook reports it and none can answer it.
//
// The delay is not defensive padding. Codex paints the dialog before its input
// loop consumes keys, and a Return sent in that window is swallowed with no
// feedback: the dialog stays up and the notification is already resolved, so
// nothing will ever answer it. Measured at three seconds against 0.150.1; see
// docs/specs/46-codex-provider.md.
// trustAffirmative is the wording of the "yes" option in Codex's directory
// trust dialog.
// Codex asks two different questions with one card behind them: the directory
// dialog and, on a fresh install, the hook one. Tried in order, because a
// screen shows only one of them.
var trustAffirmatives = []string{"yes, continue", "trust all and continue"}

func handleTrustAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}

	sessionID := notif.SourceSession
	if sessionID == "" {
		return notifications.Decision{}, fmt.Errorf("missing session for trust action")
	}
	if terminalBackend == nil {
		return notifications.Decision{}, fmt.Errorf("terminal backend not available")
	}

	if req.Action == "trust" {
		// The affirmative option is found on screen rather than assumed to be
		// the default, for the same reason as Claude: a default is the agent's
		// to change, and answering the wrong row here quits the session.
		if err := settleThen(sessionID, func() error {
			var last error
			for _, want := range trustAffirmatives {
				if last = provider.ConfirmChoice(terminalBackend, sessionID, want); last == nil {
					return nil
				}
			}
			return last
		}); err != nil {
			return notifications.Decision{}, fmt.Errorf("approve trust for %s: %w", sessionID, err)
		}
		log.Printf("codex trust-action: approved trust for session %s", sessionID)
		return notifications.Decision{Status: "approved"}, nil
	}

	// Deny — interrupt the agent rather than answering the dialog.
	if err := terminalBackend.SendKey(sessionID, backend.KeyCtrlC); err != nil {
		log.Printf("codex trust-action: failed to interrupt session %s: %v", sessionID, err)
	}
	return notifications.Decision{Status: "denied"}, nil
}
