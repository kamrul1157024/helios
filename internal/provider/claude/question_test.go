package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/notifications"
)

var threeQuestions = []questionSpec{
	{
		Question: "Which hosts should show the banner?",
		Header:   "Banner scope",
		Options:  []questionOption{{Label: "Every host"}, {Label: "Only the active host"}},
	},
	{
		Question: "How should a sleeping phone reconnect?",
		Header:   "Wake strategy",
		Options:  []questionOption{{Label: "Poll on resume"}, {Label: "Heartbeat watchdog"}},
	},
	{
		Question: "How should this ship?",
		Header:   "Rollout",
		Options:  []questionOption{{Label: "Straight to main"}, {Label: "Behind a flag"}},
	},
}

func answered(t *testing.T, v questionAnswer) *notifications.Decision {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	return &notifications.Decision{Status: "answered", Response: raw}
}

// This string is what Claude actually reads in place of the tool's result, so
// it is pinned rather than probed. The measured wording is in
// docs/specs/36-helios-owned-hitl.md.
func TestQuestionReason_Golden(t *testing.T) {
	got := questionReason(threeQuestions, answered(t, questionAnswer{Selections: []selection{
		{QuestionIndex: 0, OptionIndex: 1},
		{QuestionIndex: 1, OptionIndex: 1},
		{QuestionIndex: 2, OptionIndex: 1},
	}}))

	want := strings.Join([]string{
		"Answered by the user in helios. These are the user's answers — use them and do not ask again.",
		`1. Banner scope -> "Only the active host"`,
		`2. Wake strategy -> "Heartbeat watchdog"`,
		`3. Rollout -> "Behind a flag"`,
	}, "\n")

	if got != want {
		t.Errorf("reason =\n%s\n\nwant\n%s", got, want)
	}
}

func TestQuestionReason_FreeText(t *testing.T) {
	got := questionReason(threeQuestions, answered(t, questionAnswer{Text: "None of these — use zrok"}))

	if !strings.HasPrefix(got, answerPreamble) {
		t.Errorf("reason = %q, want it to open with the preamble", got)
	}
	if !strings.Contains(got, "None of these — use zrok") {
		t.Errorf("reason = %q, want the typed answer in it", got)
	}
}

// A partly answered set still has to name the right questions, so the reader on
// the other end is not left matching numbers to nothing.
func TestQuestionReason_OnlySomeQuestionsAnswered(t *testing.T) {
	got := questionReason(threeQuestions, answered(t, questionAnswer{Selections: []selection{
		{QuestionIndex: 2, OptionIndex: 0},
	}}))

	if !strings.Contains(got, `3. Rollout -> "Straight to main"`) {
		t.Errorf("reason = %q, want the third question numbered as the third", got)
	}
	if strings.Contains(got, "Banner scope") {
		t.Errorf("reason = %q, want nothing about a question that was not answered", got)
	}
}

func TestQuestionReason_Skipped(t *testing.T) {
	d := &notifications.Decision{Status: "denied", Response: skipResponse()}
	if got := questionReason(threeQuestions, d); got != skippedReason {
		t.Errorf("reason = %q, want the skipped wording", got)
	}
}

// Timeouts and cancellations arrive as a decision with nothing in it.
func TestQuestionReason_Unanswered(t *testing.T) {
	for name, d := range map[string]*notifications.Decision{
		"nil":           nil,
		"empty":         {Status: "denied"},
		"unreadable":    {Status: "answered", Response: json.RawMessage(`not json`)},
		"no selections": {Status: "answered", Response: json.RawMessage(`{"selections":[]}`)},
	} {
		t.Run(name, func(t *testing.T) {
			got := questionReason(threeQuestions, d)
			if got != unansweredReason && got != skippedReason {
				t.Errorf("reason = %q, want it to say nothing was chosen", got)
			}
		})
	}
}

// An index helios cannot resolve must still produce a readable line rather than
// panicking or naming the wrong option.
func TestQuestionReason_IndexOutOfRange(t *testing.T) {
	got := questionReason(threeQuestions, answered(t, questionAnswer{Selections: []selection{
		{QuestionIndex: 9, OptionIndex: 4},
	}}))

	if !strings.Contains(got, "10. Question 10 -> \"option 5\"") {
		t.Errorf("reason = %q, want a line that stands on its own", got)
	}
}

func TestOptionLabels_FillsInABlankLabel(t *testing.T) {
	labels := optionLabels(questionSpec{Options: []questionOption{{Label: "Yes"}, {}}})
	if len(labels) != 2 || labels[0] != "Yes" || labels[1] != "Option 2" {
		t.Errorf("labels = %v, want the blank one numbered", labels)
	}
}

func TestParseQuestions(t *testing.T) {
	qs := parseQuestions(json.RawMessage(
		`{"questions":[{"question":"Proceed?","header":"Plan","options":[{"label":"Yes"}]}]}`))
	if len(qs) != 1 || qs[0].Header != "Plan" || len(qs[0].Options) != 1 {
		t.Fatalf("questions = %+v, want one question with one option", qs)
	}
	if got := parseQuestions(json.RawMessage(`not json`)); got != nil {
		t.Errorf("questions = %+v, want nil for input that cannot be read", got)
	}
}
