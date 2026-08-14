package hitl

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/terminal"
)

// fakeOverlays records what a controller painted, standing in for the terminal
// backend.
type fakeOverlays struct {
	mu      sync.Mutex
	painted []terminal.Overlay
	cleared []string
	setErr  error
}

func (f *fakeOverlays) SetOverlay(sessionID string, o terminal.Overlay) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.painted = append(f.painted, o)
	return nil
}

func (f *fakeOverlays) ClearOverlay(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, sessionID)
	return nil
}

func (f *fakeOverlays) last() (terminal.Overlay, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.painted) == 0 {
		return terminal.Overlay{}, false
	}
	return f.painted[len(f.painted)-1], true
}

func (f *fakeOverlays) clears() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cleared)
}

var testPrompt = Prompt{
	Title:   "Bash",
	Body:    []string{"rm -rf build/"},
	Choices: []string{"Allow once", "Allow, and don't ask again", "Deny"},
}

// ask wires a controller to a fake terminal and a channel of answers.
func ask(t *testing.T) (*Controller, *fakeOverlays, chan Answer, func()) {
	t.Helper()
	terms := &fakeOverlays{}
	c := NewController(terms)
	answers := make(chan Answer, 4)
	release, err := c.Ask("s1", testPrompt, func(a Answer) { answers <- a })
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return c, terms, answers, release
}

// awaitAnswer waits for the callback, which runs on its own goroutine so that
// resolving a notification cannot block the mirror's reader.
func awaitAnswer(t *testing.T, answers chan Answer) Answer {
	t.Helper()
	select {
	case a := <-answers:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("no answer")
		return Answer{}
	}
}

func TestAskPaintsThePrompt(t *testing.T) {
	_, terms, _, _ := ask(t)

	o, ok := terms.last()
	if !ok {
		t.Fatal("nothing was painted")
	}
	if o.Title != "Bash" || o.Selected != 0 {
		t.Errorf("overlay = %+v, want the first choice selected", o)
	}
	if len(o.Options) != len(testPrompt.Choices) {
		t.Errorf("options = %v, want %v", o.Options, testPrompt.Choices)
	}
	if o.Footer == "" {
		t.Error("no key hint on the prompt")
	}
}

func TestArrowKeysMoveTheHighlight(t *testing.T) {
	c, terms, _, _ := ask(t)

	c.HandleInput("s1", []byte("\x1b[B\x1b[B"))
	if o, _ := terms.last(); o.Selected != 2 {
		t.Errorf("selected = %d after two downs, want 2", o.Selected)
	}
	c.HandleInput("s1", []byte("\x1b[A"))
	if o, _ := terms.last(); o.Selected != 1 {
		t.Errorf("selected = %d after an up, want 1", o.Selected)
	}
}

// Holding an arrow key must not roll "Deny" around onto "Allow once".
func TestTheHighlightStopsAtTheEnds(t *testing.T) {
	c, terms, _, _ := ask(t)

	c.HandleInput("s1", []byte("\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B"))
	if o, _ := terms.last(); o.Selected != 2 {
		t.Errorf("selected = %d at the bottom, want 2", o.Selected)
	}
	c.HandleInput("s1", []byte("\x1b[A\x1b[A\x1b[A\x1b[A"))
	if o, _ := terms.last(); o.Selected != 0 {
		t.Errorf("selected = %d at the top, want 0", o.Selected)
	}
}

func TestEnterAnswersWithTheHighlightedChoice(t *testing.T) {
	c, _, answers, _ := ask(t)

	c.HandleInput("s1", []byte("\x1b[B\r"))
	if a := awaitAnswer(t, answers); a.Index != 1 {
		t.Errorf("answer = %+v, want index 1", a)
	}
}

func TestADigitSelectsAndConfirms(t *testing.T) {
	c, _, answers, _ := ask(t)

	c.HandleInput("s1", []byte("3\r"))
	if a := awaitAnswer(t, answers); a.Index != 2 {
		t.Errorf("answer = %+v, want index 2", a)
	}
}

// A digit past the end of the list is a typo, not a choice.
func TestAnOutOfRangeDigitIsIgnored(t *testing.T) {
	c, _, answers, _ := ask(t)

	c.HandleInput("s1", []byte("9\r"))
	if a := awaitAnswer(t, answers); a.Index != 0 {
		t.Errorf("answer = %+v, want the highlight left at 0", a)
	}
}

func TestEscapeCancels(t *testing.T) {
	c, _, answers, _ := ask(t)

	c.HandleInput("s1", []byte("\x1b"))
	a := awaitAnswer(t, answers)
	if !a.Cancelled() {
		t.Errorf("answer = %+v, want a cancellation", a)
	}
}

// Two Enters are one answer. Anything else would resolve a decision twice.
func TestAPromptAnswersOnlyOnce(t *testing.T) {
	c, _, answers, _ := ask(t)

	c.HandleInput("s1", []byte("\r"))
	awaitAnswer(t, answers)
	c.HandleInput("s1", []byte("\r\x1b"))

	select {
	case a := <-answers:
		t.Errorf("answered twice: %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestReleaseClearsAndStopsListening(t *testing.T) {
	c, terms, answers, release := ask(t)

	release()
	if terms.clears() != 1 {
		t.Errorf("cleared %d times, want 1", terms.clears())
	}
	if c.Pending("s1") {
		t.Error("the prompt is still pending after release")
	}

	// Keys now belong to the agent again, so nothing here may answer.
	c.HandleInput("s1", []byte("\r"))
	select {
	case a := <-answers:
		t.Errorf("a released prompt answered: %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
}

// The hook calls release once the decision settles, whichever surface settled
// it, so a phone answer and a timeout both land here.
func TestReleaseIsIdempotent(t *testing.T) {
	_, terms, _, release := ask(t)

	release()
	release()
	if terms.clears() != 1 {
		t.Errorf("cleared %d times, want 1", terms.clears())
	}
}

func TestAskWithoutATerminalReportsIt(t *testing.T) {
	c := NewController(nil)

	release, err := c.Ask("s1", testPrompt, func(Answer) { t.Error("answered") })
	if !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err = %v, want ErrNoTerminal", err)
	}
	// The caller defers release unconditionally, so it has to be safe.
	release()
	if c.Pending("s1") {
		t.Error("a prompt that was never painted is pending")
	}
}

// A cold session cannot be painted on. That is the phone-only fallback, and it
// must not leave the controller thinking a prompt is on screen.
func TestAFailedPaintLeavesNothingPending(t *testing.T) {
	terms := &fakeOverlays{setErr: errors.New("no terminal")}
	c := NewController(terms)

	if _, err := c.Ask("s1", testPrompt, nil); err == nil {
		t.Fatal("expected an error when the overlay cannot be set")
	}
	if c.Pending("s1") {
		t.Error("a prompt that failed to paint is pending")
	}
}

func TestInputForAnUnpromptedSessionIsIgnored(t *testing.T) {
	c, _, _, _ := ask(t)
	c.HandleInput("other", []byte("\r")) // must not panic or answer
}

func TestAPromptWithNoChoicesIsRejected(t *testing.T) {
	c := NewController(&fakeOverlays{})
	if _, err := c.Ask("s1", Prompt{Title: "empty"}, nil); err == nil {
		t.Error("expected an error for a prompt with nothing to choose")
	}
}
