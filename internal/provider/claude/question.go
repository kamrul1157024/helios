package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// questionOption is one answer the CLI offered.
type questionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// questionSpec is one entry of the AskUserQuestion tool input.
type questionSpec struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	Options     []questionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect"`
}

// questionPayload is the tool input as the notification stores it: the CLI's
// own {"questions": [...]} with session_id spliced in at the top level.
type questionPayload struct {
	SessionID string         `json:"session_id"`
	Questions []questionSpec `json:"questions"`
}

// selection is one answered question, by index into the payload.
type selection struct {
	QuestionIndex int `json:"question_index"`
	OptionIndex   int `json:"option_index"`
}

// questionAnswer is what a surface sends back, whichever surface it was.
type questionAnswer struct {
	Action     string      `json:"action,omitempty"`
	Selections []selection `json:"selections,omitempty"`
	Text       string      `json:"text,omitempty"`
}

func parseQuestions(toolInput json.RawMessage) []questionSpec {
	if len(toolInput) == 0 {
		return nil
	}
	var payload questionPayload
	if err := json.Unmarshal(toolInput, &payload); err != nil {
		log.Printf("hook: parse question payload: %v", err)
		return nil
	}
	return payload.Questions
}

// showQuestionPrompt paints Claude's question over the session's terminal and
// returns the function that takes it down.
func showQuestionPrompt(ctx *provider.HookContext, sessionID, notifID string, questions []questionSpec) func() {
	if len(questions) == 0 {
		return func() {}
	}

	a := &questionAsker{ctx: ctx, sessionID: sessionID, notifID: notifID, questions: questions}
	a.ask(0)
	return a.stop
}

// questionAsker walks the terminal through one question at a time: an overlay
// is a single list of choices, and AskUserQuestion carries up to four questions.
type questionAsker struct {
	ctx       *provider.HookContext
	sessionID string
	notifID   string
	questions []questionSpec

	// mu guards the answers gathered so far and the overlay currently on
	// screen. Answers arrive on their own goroutine while the hook that started
	// this may be taking the prompt down.
	mu      sync.Mutex
	chosen  []selection
	typed   map[int]string
	release func()
	stopped bool
}

// ask paints question i. Painting replaces whatever the previous question left
// on screen, so there is nothing to clear in between.
func (a *questionAsker) ask(i int) {
	q := a.questions[i]
	a.setRelease(showPrompt(a.ctx, a.sessionID, hitl.Prompt{
		Title:   a.title(i),
		Body:    nonEmpty([]string{q.Question}),
		Choices: optionLabels(q),
		Details: optionDescriptions(q),
		Multi:   q.MultiSelect,
		// The CLI always takes an answer none of its options carry, so the row
		// that collects one is not conditional.
		AllowText: true,
	}, func(ans hitl.Answer) { a.answered(i, ans) }))
}

// title labels the box with the question's header, and says where in the set
// this one falls when there is more than one to get through.
func (a *questionAsker) title(i int) string {
	header := a.questions[i].Header
	if strings.TrimSpace(header) == "" {
		header = "Claude has a question"
	}
	if len(a.questions) == 1 {
		return header
	}
	return fmt.Sprintf("%s (%d/%d)", header, i+1, len(a.questions))
}

// answered records one choice and either moves on to the next question or
// resolves the notification, which is what wakes the blocked hook.
func (a *questionAsker) answered(i int, ans hitl.Answer) {
	if ans.Cancelled() {
		a.resolve(notifications.Decision{Status: "denied", Response: skipResponse()})
		return
	}

	a.mu.Lock()
	switch {
	case ans.Text != "":
		if a.typed == nil {
			a.typed = make(map[int]string)
		}
		a.typed[i] = ans.Text
	case len(ans.Indexes) > 0:
		for _, idx := range ans.Indexes {
			a.chosen = append(a.chosen, selection{QuestionIndex: i, OptionIndex: idx})
		}
	default:
		a.chosen = append(a.chosen, selection{QuestionIndex: i, OptionIndex: ans.Index})
	}
	chosen := append([]selection(nil), a.chosen...)
	text := a.typedText()
	a.mu.Unlock()

	if i+1 < len(a.questions) {
		a.ask(i + 1)
		return
	}

	response, err := json.Marshal(questionAnswer{Selections: chosen, Text: text})
	if err != nil {
		log.Printf("hook: encode question answer for %s: %v", a.notifID, err)
		return
	}
	a.resolve(notifications.Decision{Status: "answered", Response: response})
}

// typedText renders the answers the user wrote rather than picked. A set of
// questions shares one text field on the wire, so each answer past the first
// says which question it belongs to. The caller holds mu.
func (a *questionAsker) typedText() string {
	if len(a.typed) == 0 {
		return ""
	}
	if len(a.questions) == 1 {
		return a.typed[0]
	}
	lines := make([]string, 0, len(a.typed))
	for i := range a.questions {
		if text, ok := a.typed[i]; ok {
			lines = append(lines, fmt.Sprintf("%s: %s", a.title(i), text))
		}
	}
	return strings.Join(lines, "\n")
}

// resolve settles the one notification both surfaces share, so the first answer
// wins and the loser is told the question is gone.
func (a *questionAsker) resolve(d notifications.Decision) {
	if err := a.ctx.Mgr.Resolve(a.notifID, d, "terminal"); err != nil &&
		!errors.Is(err, store.ErrAlreadyResolved) {
		log.Printf("hook: resolve question %s from the terminal: %v", a.notifID, err)
	}
}

// setRelease remembers how to take the current question down. A question
// painted after the hook already finished is taken down immediately: otherwise
// the last one in a set could outlive the answer that resolved it.
func (a *questionAsker) setRelease(fn func()) {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		fn()
		return
	}
	a.release = fn
	a.mu.Unlock()
}

func (a *questionAsker) stop() {
	a.mu.Lock()
	fn := a.release
	a.release = nil
	a.stopped = true
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func optionLabels(q questionSpec) []string {
	labels := make([]string, 0, len(q.Options))
	for i, o := range q.Options {
		if strings.TrimSpace(o.Label) == "" {
			labels = append(labels, fmt.Sprintf("Option %d", i+1))
			continue
		}
		labels = append(labels, o.Label)
	}
	return labels
}

// optionDescriptions returns the reasoning under each label, or nil when the
// question carries none — the overlay leaves the field out entirely then, and
// an older host sees the JSON it has always seen.
func optionDescriptions(q questionSpec) []string {
	found := false
	details := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		d := strings.TrimSpace(o.Description)
		found = found || d != ""
		details = append(details, d)
	}
	if !found {
		return nil
	}
	return details
}

func skipResponse() json.RawMessage {
	return json.RawMessage(`{"action":"skip"}`)
}

// The reason string is what Claude actually reads as the answer, so it is
// built in one place and has its own test.
const (
	answerPreamble = "Answered by the user in helios. " +
		"These are the user's answers — use them and do not ask again."
	skippedReason = "The user skipped this question in helios. " +
		"Continue without an answer and do not ask it again."
	unansweredReason = "Nobody answered this question in helios in time. " +
		"Continue with your best judgement rather than asking again."
)

// questionReason renders the decision as the text Claude receives in place of
// the tool's result. See docs/specs/36-helios-owned-hitl.md for the measurement
// behind this shape.
func questionReason(questions []questionSpec, d *notifications.Decision) string {
	if d == nil || len(d.Response) == 0 {
		return unansweredReason
	}
	var ans questionAnswer
	if err := json.Unmarshal(d.Response, &ans); err != nil {
		log.Printf("hook: read question answer: %v", err)
		return unansweredReason
	}
	if len(ans.Selections) == 0 && strings.TrimSpace(ans.Text) == "" {
		return skippedReason
	}

	lines := []string{answerPreamble}
	for _, sel := range ans.Selections {
		lines = append(lines, selectionLine(questions, sel))
	}
	if text := strings.TrimSpace(ans.Text); text != "" {
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

// selectionLine names the question and the option chosen, both by their own
// words: an index would mean nothing to the reader on the other end.
func selectionLine(questions []questionSpec, sel selection) string {
	header := fmt.Sprintf("Question %d", sel.QuestionIndex+1)
	label := fmt.Sprintf("option %d", sel.OptionIndex+1)

	if sel.QuestionIndex >= 0 && sel.QuestionIndex < len(questions) {
		q := questions[sel.QuestionIndex]
		if h := firstNonEmpty(q.Header, q.Question); h != "" {
			header = h
		}
		if sel.OptionIndex >= 0 && sel.OptionIndex < len(q.Options) {
			if l := strings.TrimSpace(q.Options[sel.OptionIndex].Label); l != "" {
				label = l
			}
		}
	}
	return fmt.Sprintf("%d. %s -> %q", sel.QuestionIndex+1, header, label)
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if s := strings.TrimSpace(c); s != "" {
			return s
		}
	}
	return ""
}
