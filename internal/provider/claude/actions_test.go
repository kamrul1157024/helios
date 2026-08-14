package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/store"
)

// withBackend installs a fake terminal backend for the duration of a test.
func withBackend(t *testing.T, f *fakeBackend) *fakeBackend {
	t.Helper()
	prev := terminalBackend
	terminalBackend = f
	t.Cleanup(func() { terminalBackend = prev })
	return f
}

func errorNotif(sessionID string) *store.Notification {
	payload := `{"session_id":"` + sessionID + `","error":"API Error: Response stalled mid-stream.","is_rate_limit":false,"retryable":true}`
	return &store.Notification{
		ID:            "notif-1",
		Source:        "claude",
		SourceSession: sessionID,
		Type:          "claude.error",
		Status:        "pending",
		Payload:       &payload,
	}
}

func TestErrorAction_RetrySendsContinue(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	dec, err := handleErrorAction(errorNotif("sess-1"), json.RawMessage(`{"action":"retry"}`))
	if err != nil {
		t.Fatalf("handleErrorAction: %v", err)
	}
	if dec.Status != "approved" {
		t.Errorf("status = %q, want approved", dec.Status)
	}
	// "continue" is what the user types in the terminal: the CLI resumes the
	// interrupted turn rather than starting a new one.
	if len(fb.texts) != 1 || fb.texts[0] != "sess-1:continue" {
		t.Errorf("texts = %v, want [sess-1:continue]", fb.texts)
	}
}

// A notification consumed by a send that went nowhere is unrecoverable, so a
// dead terminal must error rather than resolve.
func TestErrorAction_RetryWithDeadTerminal(t *testing.T) {
	fb := withBackend(t, newFakeBackend())

	_, err := handleErrorAction(errorNotif("sess-1"), json.RawMessage(`{"action":"retry"}`))
	if err == nil {
		t.Fatal("want an error for a session with no live terminal")
	}
	if !strings.Contains(err.Error(), "no live terminal") {
		t.Errorf("error = %v, want it to mention the missing terminal", err)
	}
	if len(fb.texts) != 0 {
		t.Errorf("texts = %v, want nothing sent", fb.texts)
	}
}

func TestErrorAction_RetryWithNoBackend(t *testing.T) {
	prev := terminalBackend
	terminalBackend = nil
	t.Cleanup(func() { terminalBackend = prev })

	if _, err := handleErrorAction(errorNotif("sess-1"), json.RawMessage(`{"action":"retry"}`)); err == nil {
		t.Fatal("want an error when no backend is wired")
	}
}

func TestErrorAction_Dismiss(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	dec, err := handleErrorAction(errorNotif("sess-1"), json.RawMessage(`{"action":"dismiss"}`))
	if err != nil {
		t.Fatalf("handleErrorAction: %v", err)
	}
	if dec.Status != "dismissed" {
		t.Errorf("status = %q, want dismissed", dec.Status)
	}
	if len(fb.texts) != 0 {
		t.Errorf("texts = %v, want nothing sent on dismiss", fb.texts)
	}
}

func TestErrorAction_UnknownAction(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	if _, err := handleErrorAction(errorNotif("sess-1"), json.RawMessage(`{"action":"explode"}`)); err == nil {
		t.Fatal("want an error for an unknown action")
	}
}

// The payload is the primary source, but a row written before the payload
// existed still names its session, so fall back rather than refusing.
func TestErrorAction_FallsBackToSourceSession(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	notif := errorNotif("sess-1")
	notif.Payload = nil

	if _, err := handleErrorAction(notif, json.RawMessage(`{"action":"retry"}`)); err != nil {
		t.Fatalf("handleErrorAction: %v", err)
	}
	if len(fb.texts) != 1 || fb.texts[0] != "sess-1:continue" {
		t.Errorf("texts = %v, want [sess-1:continue]", fb.texts)
	}
}

func TestErrorAction_MissingSessionID(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	notif := errorNotif("sess-1")
	notif.SourceSession = ""
	empty := `{"error":"boom"}`
	notif.Payload = &empty

	if _, err := handleErrorAction(notif, json.RawMessage(`{"action":"retry"}`)); err == nil {
		t.Fatal("want an error when no session can be identified")
	}
}

// ==================== Answering a question from the phone ====================

// questionNotif builds the notification handleQuestion stores: the tool input
// with session_id spliced in at the top level.
func questionNotif(sessionID string, questions string) *store.Notification {
	payload := `{"questions":` + questions + `,"session_id":"` + sessionID + `"}`
	return &store.Notification{
		ID:            "notif-q",
		Source:        "claude",
		SourceSession: sessionID,
		Type:          "claude.question",
		Status:        "pending",
		Payload:       &payload,
	}
}

const twoOptions = `[{"question":"Which approach?","header":"Approach","options":[{"label":"Rewrite"},{"label":"Patch"},{"label":"Leave it"}]}]`

// The phone answers by resolving the notification the blocked hook is waiting
// on. It types nothing into the terminal: that is what this replaced.
func TestQuestionAction_CarriesTheSelections(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	dec, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":2}]}`),
	)
	if err != nil {
		t.Fatalf("handleQuestionAction: %v", err)
	}
	if dec.Status != "answered" {
		t.Errorf("status = %q, want answered", dec.Status)
	}
	if !strings.Contains(string(dec.Response), `"option_index":2`) {
		t.Errorf("response = %s, want the chosen option in it", dec.Response)
	}
	if got := fb.sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want nothing typed into the session", got)
	}
}

func TestQuestionAction_Skip(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")

	dec, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"skip"}`),
	)
	if err != nil {
		t.Fatalf("handleQuestionAction: %v", err)
	}
	if dec.Status != "denied" {
		t.Errorf("status = %q, want denied", dec.Status)
	}
	if got := fb.sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want nothing typed into the session", got)
	}
}

func TestQuestionAction_FreeText(t *testing.T) {
	dec, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","text":"something else"}`),
	)
	if err != nil {
		t.Fatalf("handleQuestionAction: %v", err)
	}
	if !strings.Contains(string(dec.Response), "something else") {
		t.Errorf("response = %s, want the typed answer in it", dec.Response)
	}
}

// A dead terminal used to be fatal, because answering meant typing. It is not
// any more: the hook holding the question is in the daemon, not the session.
func TestQuestionAction_NeedsNoTerminal(t *testing.T) {
	withBackend(t, newFakeBackend())

	if _, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":0}]}`),
	); err != nil {
		t.Errorf("handleQuestionAction with no live terminal: %v", err)
	}
}

func TestQuestionAction_RejectsAnEmptyAnswer(t *testing.T) {
	if _, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer"}`),
	); err == nil {
		t.Fatal("want an error for an answer with nothing in it")
	}
}

func TestQuestionAction_RejectsAnUnknownAction(t *testing.T) {
	if _, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"maybe"}`),
	); err == nil {
		t.Fatal("want an error for an unknown action")
	}
}
