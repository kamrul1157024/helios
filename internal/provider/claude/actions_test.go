package claude

import (
	"encoding/json"
	"strings"
	"sync"
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

// ==================== Question injection ====================

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

// The rendered CLI wraps the question inside a box, so the text never appears
// contiguously — the screen check has to survive that.
const questionScreen = `
╭──────────────────────────────────────╮
│ Approach                             │
│ Which approach should we take for    │
│ the migration?                       │
│                                      │
│ ❯ 1. Rewrite                         │
│   2. Patch                           │
│   3. Leave it                        │
╰──────────────────────────────────────╯
`

func TestQuestionAction_SelectsOptionByIndex(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen(questionScreen)

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
	// Option 2 is two moves down from the highlighted first option.
	want := []string{"sess-1:down", "sess-1:down", "sess-1:enter"}
	if got := fb.sentKeys(); !equalStrings(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func TestQuestionAction_FirstOptionSendsOnlyEnter(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen(questionScreen)

	if _, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":0}]}`),
	); err != nil {
		t.Fatalf("handleQuestionAction: %v", err)
	}
	if got := fb.sentKeys(); !equalStrings(got, []string{"sess-1:enter"}) {
		t.Errorf("keys = %v, want [sess-1:enter]", got)
	}
}

// The single most important safety property: a stray Enter into a session that
// has moved on is a real action the user did not ask for.
func TestQuestionAction_AbortsWhenScreenDoesNotShowQuestion(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen("$ git status\nnothing to commit\n")

	_, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":1}]}`),
	)
	if err == nil {
		t.Fatal("want an error when the question is not on screen")
	}
	if got := fb.sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want none sent", got)
	}
}

func TestQuestionAction_SkipSendsEscape(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen(questionScreen)

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
	if got := fb.sentKeys(); !equalStrings(got, []string{"sess-1:escape"}) {
		t.Errorf("keys = %v, want [sess-1:escape]", got)
	}
}

func TestQuestionAction_DeadTerminalErrors(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.setScreen(questionScreen)

	_, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":0}]}`),
	)
	if err == nil {
		t.Fatal("want an error for a session with no live terminal")
	}
	if !strings.Contains(err.Error(), "no live terminal") {
		t.Errorf("error = %v, want it to mention the missing terminal", err)
	}
}

func TestQuestionAction_OptionIndexOutOfRange(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen(questionScreen)

	if _, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":7}]}`),
	); err == nil {
		t.Fatal("want an error for an out-of-range option")
	}
	if got := fb.sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want none sent", got)
	}
}

func TestQuestionAction_FreeTextIsSubmittedAsAPrompt(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen(questionScreen)

	dec, err := handleQuestionAction(
		questionNotif("sess-1", twoOptions),
		json.RawMessage(`{"action":"answer","text":"something else"}`),
	)
	if err != nil {
		t.Fatalf("handleQuestionAction: %v", err)
	}
	if dec.Status != "answered" {
		t.Errorf("status = %q, want answered", dec.Status)
	}
	if len(fb.texts) != 1 || fb.texts[0] != "sess-1:something else" {
		t.Errorf("texts = %v, want [sess-1:something else]", fb.texts)
	}
}

// Two devices answering at once must not interleave keystrokes into the same
// dialog: each answer's Downs have to stay contiguous with its own Enter.
func TestQuestionAction_ConcurrentAnswersDoNotInterleave(t *testing.T) {
	fb := withBackend(t, newFakeBackend())
	fb.live("sess-1")
	fb.setScreen(questionScreen)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleQuestionAction(
				questionNotif("sess-1", twoOptions),
				json.RawMessage(`{"action":"answer","selections":[{"question_index":0,"option_index":2}]}`),
			)
		}()
	}
	wg.Wait()

	got := fb.sentKeys()
	want := []string{
		"sess-1:down", "sess-1:down", "sess-1:enter",
		"sess-1:down", "sess-1:down", "sess-1:enter",
	}
	if !equalStrings(got, want) {
		t.Errorf("keys = %v, want two uninterleaved runs %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
