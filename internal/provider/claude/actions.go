package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// terminalBackend is set by the daemon after shared state is initialized.
var terminalBackend backend.Backend

// SetBackend gives action handlers access to session terminals.
func SetBackend(b backend.Backend) {
	terminalBackend = b
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

// keyDelay spaces out injected keystrokes. The CLI redraws between them and a
// burst can be coalesced into a single highlight move.
const keyDelay = 40 * time.Millisecond

// screenSettleTimeout bounds the wait for a question to render before its
// keystrokes are sent.
const screenSettleTimeout = 3 * time.Second

// answerLocks serialises injection per session. Two devices answering at once
// must not interleave keystrokes into the same dialog.
var answerLocks sync.Map // sessionID -> *sync.Mutex

func sessionAnswerLock(sessionID string) *sync.Mutex {
	lock, _ := answerLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// questionSpec is one entry of the AskUserQuestion tool input.
type questionSpec struct {
	Question string            `json:"question"`
	Header   string            `json:"header"`
	Options  []json.RawMessage `json:"options"`
}

type questionPayload struct {
	SessionID string         `json:"session_id"`
	Questions []questionSpec `json:"questions"`
}

// handleQuestionAction answers Claude's question by driving the CLI's own
// question UI.
//
// The alternative — returning the answer from a blocking PreToolUse hook — is
// what this replaces: it prevented the CLI from ever rendering the UI, so the
// terminal user could not answer at all.
func handleQuestionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action     string `json:"action"`
		Selections []struct {
			QuestionIndex int `json:"question_index"`
			OptionIndex   int `json:"option_index"`
		} `json:"selections"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
	}
	if req.Action != "answer" && req.Action != "skip" {
		return notifications.Decision{}, fmt.Errorf("action must be answer/skip")
	}

	var payload questionPayload
	if notif.Payload != nil {
		if err := json.Unmarshal([]byte(*notif.Payload), &payload); err != nil {
			return notifications.Decision{}, fmt.Errorf("invalid payload: %w", err)
		}
	}
	sessionID := payload.SessionID
	if sessionID == "" {
		sessionID = notif.SourceSession
	}
	if sessionID == "" {
		return notifications.Decision{}, fmt.Errorf("missing session_id in notification payload")
	}

	// Reject rather than resolve when there is nothing to type into. Consuming
	// the notification here would leave the CLI sitting at a question no
	// surface can answer any more.
	if terminalBackend == nil || !terminalBackend.Alive(sessionID) {
		return notifications.Decision{}, fmt.Errorf("session %s has no live terminal", sessionID)
	}

	lock := sessionAnswerLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	if req.Action == "skip" {
		if err := terminalBackend.SendKey(sessionID, backend.KeyEscape); err != nil {
			return notifications.Decision{}, fmt.Errorf("send Escape to session %s: %w", sessionID, err)
		}
		log.Printf("question-action: skipped question for session %s", sessionID)
		return notifications.Decision{Status: "denied"}, nil
	}

	if req.Text != "" {
		if len(payload.Questions) > 0 {
			if err := awaitQuestionOnScreen(sessionID, payload.Questions[0]); err != nil {
				return notifications.Decision{}, err
			}
		}
		if err := terminalBackend.SendText(sessionID, req.Text); err != nil {
			return notifications.Decision{}, fmt.Errorf("send answer to session %s: %w", sessionID, err)
		}
		response, err := json.Marshal(map[string]interface{}{"text": req.Text})
		if err != nil {
			return notifications.Decision{}, fmt.Errorf("encode response: %w", err)
		}
		return notifications.Decision{Status: "answered", Response: response}, nil
	}

	if len(req.Selections) == 0 {
		return notifications.Decision{}, fmt.Errorf("missing selections")
	}

	for _, sel := range req.Selections {
		if sel.QuestionIndex < 0 || sel.QuestionIndex >= len(payload.Questions) {
			return notifications.Decision{}, fmt.Errorf("question index %d out of range", sel.QuestionIndex)
		}
		q := payload.Questions[sel.QuestionIndex]
		if sel.OptionIndex < 0 || sel.OptionIndex >= len(q.Options) {
			return notifications.Decision{}, fmt.Errorf("option index %d out of range for question %d", sel.OptionIndex, sel.QuestionIndex)
		}
		if err := answerQuestion(sessionID, q, sel.OptionIndex); err != nil {
			return notifications.Decision{}, err
		}
	}

	response, err := json.Marshal(map[string]interface{}{"selections": req.Selections})
	if err != nil {
		return notifications.Decision{}, fmt.Errorf("encode response: %w", err)
	}
	log.Printf("question-action: answered %d question(s) for session %s", len(req.Selections), sessionID)
	return notifications.Decision{Status: "answered", Response: response}, nil
}

// answerQuestion moves the CLI's highlight to optionIndex and confirms it.
//
// The screen check runs first and is mandatory: a stray Enter into a session
// that has moved on is a real action the user did not ask for.
func answerQuestion(sessionID string, q questionSpec, optionIndex int) error {
	if err := awaitQuestionOnScreen(sessionID, q); err != nil {
		return err
	}
	for i := 0; i < optionIndex; i++ {
		if err := terminalBackend.SendKey(sessionID, backend.KeyDown); err != nil {
			return fmt.Errorf("send Down to session %s: %w", sessionID, err)
		}
		time.Sleep(keyDelay)
	}
	if err := terminalBackend.SendKey(sessionID, backend.KeyEnter); err != nil {
		return fmt.Errorf("send Enter to session %s: %w", sessionID, err)
	}
	return nil
}

// awaitQuestionOnScreen polls the rendered screen until it shows q, and fails
// closed when it never does. The poll covers the gap between one question
// being confirmed and the next being drawn.
func awaitQuestionOnScreen(sessionID string, q questionSpec) error {
	needles := condenseAll(q.Question, q.Header)
	if len(needles) == 0 {
		return fmt.Errorf("question carries no text to match against the screen")
	}

	deadline := time.Now().Add(screenSettleTimeout)
	var lastErr error
	for {
		screen, err := terminalBackend.Capture(sessionID)
		if err != nil {
			lastErr = fmt.Errorf("capture session %s: %w", sessionID, err)
		} else {
			lastErr = nil
			haystack := condense(screen)
			for _, needle := range needles {
				if strings.Contains(haystack, needle) {
					return nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(keyDelay)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("session %s is not showing the question any more", sessionID)
}

// condense reduces text to lowercase letters and digits.
//
// The screen is emulator output, so a question is wrapped across lines and
// boxed in border characters; nothing matches contiguously until the padding,
// newlines and borders are gone.
func condense(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func condenseAll(texts ...string) []string {
	var out []string
	for _, t := range texts {
		if c := condense(t); c != "" {
			out = append(out, c)
		}
	}
	return out
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
		// The trust dialog opens with "Yes, proceed" selected, so Enter accepts.
		if err := terminalBackend.SendKey(payload.SessionID, backend.KeyEnter); err != nil {
			return notifications.Decision{}, fmt.Errorf("send Enter to session %s: %w", payload.SessionID, err)
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
