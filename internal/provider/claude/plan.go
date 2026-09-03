package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
)

// exitPlanModeTool is the tool Claude calls to leave plan mode.
//
// On the wire its approval is a permission like any other, so helios used to
// paint "Allow once" and "Deny" over it. It is not a yes-or-no question: the
// CLI's own dialog offers the mode to continue in, and a way to send the plan
// back with a reason. See docs/specs/57-plan-approval.md.
const exitPlanModeTool = "ExitPlanMode"

// The rows helios draws for a plan, worded as the CLI words them. The first two
// differ only in the mode they leave the session in. The third is not a choice
// at all: it labels the row that opens the answer field.
const (
	planAutoMode    = "Yes, and use auto mode"
	planManualEdits = "Yes, manually approve edits"
	planFeedback    = "Tell Claude what to change"
)

const (
	planAutoModeDetail    = "Claude edits and runs commands without asking, for the rest of this session"
	planManualEditsDetail = "Claude asks before each edit, as it does now"
)

// planTitle names the box after the decision rather than after the tool. A tool
// name is the right label for Bash. Here it would name the mechanism.
const planTitle = "Ready to code?"

// planPrompt is the approval a plan gets.
func planPrompt(input *hookInput) hitl.Prompt {
	return hitl.Prompt{
		Title:     planTitle,
		Body:      planBody(input.ToolInput),
		Choices:   []string{planAutoMode, planManualEdits},
		Details:   []string{planAutoModeDetail, planManualEditsDetail},
		AllowText: true,
		TextLabel: planFeedback,
	}
}

// planBodyMaxLines caps how much of the plan the box shows. The overlay is
// anchored to the bottom of the viewport and clips from the top, so an uncapped
// plan pushes the rows themselves off the screen.
const planBodyMaxLines = 14

// planBody renders the plan for the overlay, one entry per line of markdown.
//
// The line-per-entry part is not cosmetic: the box wraps on words and would
// reflow a whole plan passed as one entry into a single paragraph, taking the
// headings with it.
func planBody(toolInput json.RawMessage) []string {
	var in struct {
		Plan     string `json:"plan"`
		FilePath string `json:"planFilePath"`
	}
	if len(toolInput) > 0 {
		if err := json.Unmarshal(toolInput, &in); err != nil {
			log.Printf("hook: parse plan: %v", err)
		}
	}
	if strings.TrimSpace(in.Plan) == "" {
		return []string{summarizeToolInput(toolInput)}
	}

	lines := strings.Split(strings.TrimRight(in.Plan, "\n"), "\n")
	hidden := 0
	if len(lines) > planBodyMaxLines {
		hidden = len(lines) - planBodyMaxLines
		lines = lines[:planBodyMaxLines]
	}
	if tail := planTail(hidden, in.FilePath); tail != "" {
		lines = append(lines, tail)
	}
	return lines
}

// planTail says what was cut and where the whole plan lives, so a box that
// shows part of a plan is not the only copy the reader can reach.
func planTail(hidden int, path string) string {
	switch {
	case hidden > 0 && path != "":
		return fmt.Sprintf("…%d more lines · %s", hidden, path)
	case hidden > 0:
		return fmt.Sprintf("…%d more lines", hidden)
	default:
		return path
	}
}

// planHeadlineMax keeps the one-line form short enough for a phone's push
// notification, which shows a couple of lines at most.
const planHeadlineMax = 100

// planHeadline reduces a plan to one line: its first non-blank line, with the
// markdown heading marks taken off.
func planHeadline(plan string) string {
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" {
			continue
		}
		if len(line) > planHeadlineMax {
			return line[:planHeadlineMax] + "..."
		}
		return line
	}
	return ""
}

// The two ways to say yes to a plan, as they travel between surfaces. Short
// names rather than the row labels, because the labels are the CLI's copy and
// change with its releases.
const (
	planChoiceAuto   = "auto"
	planChoiceManual = "manual"
)

// planChoiceOf names the answer a row stands for, or "" for a row that is not
// one of the two ways to say yes.
func planChoiceOf(choice string) string {
	switch choice {
	case planAutoMode:
		return planChoiceAuto
	case planManualEdits:
		return planChoiceManual
	}
	return ""
}

// approvedWithPlanChoice is a "Yes" row. The mode rides along because the CLI
// does not take it from the hook; see answerPlanDialog.
func approvedWithPlanChoice(choice string) notifications.Decision {
	return notifications.Decision{
		Status:   "approved",
		Response: answerJSON(permissionAnswer{PlanChoice: choice}),
	}
}

// deniedWithFeedback sends the plan back with the user's own words.
func deniedWithFeedback(text string) notifications.Decision {
	return notifications.Decision{
		Status:   "denied",
		Response: answerJSON(permissionAnswer{Feedback: text}),
	}
}

// What each plan choice matches on the CLI's own dialog, and the permission
// mode helios records for it.
//
// The match is a substring of the CLI's row, kept to the words that carry the
// meaning. Its full copy is "Yes, and use auto mode", and that wording is the
// CLI's to change.
var planChoices = map[string]struct {
	dialogRow string
	mode      string
}{
	planChoiceAuto:   {dialogRow: "auto mode", mode: "auto"},
	planChoiceManual: {dialogRow: "manually approve", mode: "manual"},
}

// planDialogWait is how long helios keeps looking for the CLI's dialog.
//
// The hook has to answer before the dialog is drawn — the CLI holds it back
// until the hook replies — so the reply and the keystroke cannot be sent
// together. Measured at well under a second; the rest is slack.
const (
	planDialogWait = 15 * time.Second
	planDialogPoll = 150 * time.Millisecond
)

// answerPlanDialog picks a row on the CLI's own plan dialog.
//
// A plan is the one permission the CLI will not let a hook decide: an "allow"
// for ExitPlanMode is ignored and the dialog is shown regardless. Measured
// against Claude Code 2.1.259 — an allow, with and without a setMode permission
// update, left the dialog up and the plan unstarted. So helios collects the
// answer on its own overlay, replies "ask" to get out of the CLI's way, and
// presses the row the user chose.
//
// Failing here is survivable and deliberately quiet: the CLI's dialog is on
// screen, fully usable, and the person at the terminal can answer it. That is
// also the behaviour if the CLI renames a row out from under the match.
func answerPlanDialog(b backend.Backend, sessionID, choice string) {
	row, ok := planChoices[choice]
	if !ok {
		return
	}

	deadline := time.Now().Add(planDialogWait)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = provider.ConfirmChoice(b, sessionID, row.dialogRow); lastErr == nil {
			return
		}
		time.Sleep(planDialogPoll)
	}
	log.Printf("hook: answer the plan dialog for %s with %q: %v", sessionID, row.dialogRow, lastErr)
}

// recordPlanMode writes an approved plan's mode to the session record.
//
// The CLI applies the mode to the running process only. Helios repeats
// --permission-mode from the record on every resume (see ResumeArgs), so
// without this a session that left plan mode would wake back up inside it.
func recordPlanMode(ctx *provider.HookContext, sessionID, choice string) {
	row, ok := planChoices[choice]
	if !ok {
		return
	}
	if err := ctx.DB.UpdateSessionPermissionMode(sessionID, row.mode); err != nil {
		log.Printf("hook: record permission mode %q for %s: %v", row.mode, sessionID, err)
	}
}
