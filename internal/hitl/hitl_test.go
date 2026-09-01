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
	mu       sync.Mutex
	painted  []terminal.Overlay
	cleared  []string
	setErr   error
	protocol int
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

// OverlayProtocol reports a current host unless a test says otherwise, so the
// zero value of fakeOverlays is not an ancient terminal.
func (f *fakeOverlays) OverlayProtocol(sessionID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.protocol == 0 {
		return terminal.HostProtocol
	}
	return f.protocol
}

func (f *fakeOverlays) last() (terminal.Overlay, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.painted) == 0 {
		return terminal.Overlay{}, false
	}
	return f.painted[len(f.painted)-1], true
}

func (f *fakeOverlays) paints() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.painted)
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
	return askPrompt(t, testPrompt)
}

func askPrompt(t *testing.T, p Prompt) (*Controller, *fakeOverlays, chan Answer, func()) {
	t.Helper()
	terms := &fakeOverlays{}
	c := NewController(terms)
	answers := make(chan Answer, 4)
	release, err := c.Ask("s1", p, func(a Answer) { answers <- a })
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

var multiPrompt = Prompt{
	Title:   "Which checks to run",
	Choices: []string{"Unit tests", "Race detector", "Flutter analyze"},
	Multi:   true,
}

var typedPrompt = Prompt{
	Title:     "Next step",
	Choices:   []string{"Live repro", "Code review"},
	AllowText: true,
}

func TestDescriptionsReachTheOverlay(t *testing.T) {
	_, terms, _, _ := askPrompt(t, Prompt{
		Title:   "Next step",
		Choices: []string{"Live repro", "Code review"},
		Details: []string{"Drive the real TUI.", "Read the diff."},
	})

	o, _ := terms.last()
	if len(o.Details) != 2 || o.Details[0] != "Drive the real TUI." {
		t.Errorf("details = %v, want both descriptions", o.Details)
	}
}

// A permission prompt asks for none of this, and has to paint exactly what it
// painted before the fields existed — an older host is reading the same bytes.
func TestAPlainPromptCarriesNoneOfTheNewFields(t *testing.T) {
	_, terms, _, _ := ask(t)

	o, _ := terms.last()
	if o.Details != nil || o.Checked != nil || o.Input != nil {
		t.Errorf("overlay = %+v, want no details, checkboxes or field", o)
	}
}

func TestSpaceTicksAChoice(t *testing.T) {
	c, terms, _, _ := askPrompt(t, multiPrompt)

	c.HandleInput("s1", []byte(" "))
	o, _ := terms.last()
	if len(o.Checked) != 3 || !o.Checked[0] {
		t.Errorf("checked = %v, want the first choice ticked", o.Checked)
	}

	c.HandleInput("s1", []byte(" "))
	o, _ = terms.last()
	if o.Checked[0] {
		t.Error("a second space did not untick the choice")
	}
}

func TestSpaceDoesNothingOnASingleChoicePrompt(t *testing.T) {
	c, terms, answers, _ := ask(t)

	c.HandleInput("s1", []byte(" "))
	o, _ := terms.last()
	if o.Checked != nil {
		t.Errorf("checked = %v, want none on a single-choice prompt", o.Checked)
	}
	select {
	case a := <-answers:
		t.Fatalf("space answered the prompt: %+v", a)
	default:
	}
}

func TestEnterAnswersWithEveryTickedChoice(t *testing.T) {
	c, _, answers, _ := askPrompt(t, multiPrompt)

	c.HandleInput("s1", []byte(" "))      // tick the first
	c.HandleInput("s1", []byte("\x1b[B")) // move down
	c.HandleInput("s1", []byte(" "))      // tick the second
	c.HandleInput("s1", []byte("\r"))

	a := awaitAnswer(t, answers)
	if a.Cancelled() {
		t.Fatalf("answer = %+v, want two choices", a)
	}
	if len(a.Indexes) != 2 || a.Indexes[0] != 0 || a.Indexes[1] != 1 {
		t.Errorf("indexes = %v, want [0 1]", a.Indexes)
	}
}

// Answering a multi-select question with nothing ticked is not an answer.
func TestEnterWithNothingTickedIsACancel(t *testing.T) {
	c, _, answers, _ := askPrompt(t, multiPrompt)

	c.HandleInput("s1", []byte("\r"))
	if a := awaitAnswer(t, answers); !a.Cancelled() {
		t.Errorf("answer = %+v, want a cancel", a)
	}
}

func TestTypingAnAnswer(t *testing.T) {
	c, terms, answers, _ := askPrompt(t, typedPrompt)

	// Down twice lands on the row past the two choices: the answer field.
	c.HandleInput("s1", []byte("\x1b[B\x1b[B"))
	c.HandleInput("s1", []byte("\r"))
	o, _ := terms.last()
	if o.Input == nil || !o.Input.Active {
		t.Fatalf("input = %+v, want an open field", o.Input)
	}
	if o.Footer != footerTyping {
		t.Errorf("footer = %q, want the typing hint", o.Footer)
	}

	c.HandleInput("s1", []byte("rebase first"))
	o, _ = terms.last()
	if o.Input.Value != "rebase first" {
		t.Errorf("value = %q, want what was typed", o.Input.Value)
	}

	c.HandleInput("s1", []byte("\r"))
	a := awaitAnswer(t, answers)
	if a.Text != "rebase first" || a.Cancelled() {
		t.Errorf("answer = %+v, want the typed text", a)
	}
}

func TestBackspaceEditsTheAnswer(t *testing.T) {
	c, terms, _, _ := askPrompt(t, typedPrompt)

	c.HandleInput("s1", []byte("\x1b[B\x1b[B\r"))
	c.HandleInput("s1", []byte("one two"))
	c.HandleInput("s1", []byte("\x7f"))
	if o, _ := terms.last(); o.Input.Value != "one tw" {
		t.Errorf("value = %q, want one rune gone", o.Input.Value)
	}
	c.HandleInput("s1", []byte("\x17"))
	if o, _ := terms.last(); o.Input.Value != "one " {
		t.Errorf("value = %q, want the word gone", o.Input.Value)
	}
	c.HandleInput("s1", []byte("\x15"))
	if o, _ := terms.last(); o.Input.Value != "" {
		t.Errorf("value = %q, want the line cleared", o.Input.Value)
	}
}

// The first Escape closes the field and leaves the question standing. Only the
// second one throws it away.
func TestEscapeLeavesTheFieldBeforeItCancels(t *testing.T) {
	c, terms, answers, _ := askPrompt(t, typedPrompt)

	c.HandleInput("s1", []byte("\x1b[B\x1b[B\r"))
	c.HandleInput("s1", []byte("draft"))
	c.HandleInput("s1", []byte("\x1b"))

	o, _ := terms.last()
	if o.Input.Active {
		t.Error("the field is still open after Escape")
	}
	if o.Input.Value != "draft" {
		t.Errorf("value = %q, want what was typed to survive", o.Input.Value)
	}
	select {
	case a := <-answers:
		t.Fatalf("the first Escape answered the prompt: %+v", a)
	default:
	}

	c.HandleInput("s1", []byte("\x1b"))
	if a := awaitAnswer(t, answers); !a.Cancelled() {
		t.Errorf("answer = %+v, want a cancel", a)
	}
}

func TestAnEmptyFieldIsNotAnAnswer(t *testing.T) {
	c, terms, answers, _ := askPrompt(t, typedPrompt)

	c.HandleInput("s1", []byte("\x1b[B\x1b[B\r"))
	c.HandleInput("s1", []byte("\r"))

	if o, _ := terms.last(); o.Input.Active {
		t.Error("Enter on an empty field left it open")
	}
	select {
	case a := <-answers:
		t.Fatalf("an empty field answered the prompt: %+v", a)
	default:
	}
}

// An older host would draw a multi-select question as a single-select list,
// which is an answer the user did not give. That one goes to the phone whole.
func TestAnOldHostIsNotOfferedCheckboxes(t *testing.T) {
	terms := &fakeOverlays{protocol: 1}
	c := NewController(terms)

	if _, err := c.Ask("s1", multiPrompt, nil); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("err = %v, want ErrNoTerminal", err)
	}
	if terms.paints() != 0 {
		t.Error("painted a multi-select prompt on a host that cannot draw one")
	}
}

// The answer field is the one that degrades: an older host still gets the
// choices, and what the user wanted to write is still answerable on the phone.
// Suppressing the whole prompt instead would leave that terminal with nothing.
func TestAnOldHostStillGetsTheChoices(t *testing.T) {
	terms := &fakeOverlays{protocol: 1}
	c := NewController(terms)

	if _, err := c.Ask("s1", typedPrompt, nil); err != nil {
		t.Fatalf("err = %v, want the choices painted", err)
	}
	o, ok := terms.last()
	if !ok {
		t.Fatal("nothing was painted")
	}
	if o.Input != nil {
		t.Errorf("input = %+v, want no field on a host that cannot draw one", o.Input)
	}
	if len(o.Options) != len(typedPrompt.Choices) {
		t.Errorf("options = %v, want all the choices", o.Options)
	}
}

// A question with no choices is nothing but the field, so there is no reduced
// version of it to draw.
func TestAnOldHostGetsNoFieldOnlyPrompt(t *testing.T) {
	terms := &fakeOverlays{protocol: 1}
	c := NewController(terms)

	_, err := c.Ask("s1", Prompt{Title: "Name", AllowText: true}, nil)
	if !errors.Is(err, ErrNoTerminal) {
		t.Errorf("err = %v, want ErrNoTerminal", err)
	}
}

// A prompt that asks for none of it is painted on any host.
func TestAnOldHostStillGetsAPlainPrompt(t *testing.T) {
	c := NewController(&fakeOverlays{protocol: 1})
	if _, err := c.Ask("s1", testPrompt, nil); err != nil {
		t.Errorf("err = %v, want it painted", err)
	}
}
