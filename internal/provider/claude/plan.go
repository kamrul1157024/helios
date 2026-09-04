package claude

import (
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
)

// exitPlanModeTool is the tool Claude calls to leave plan mode.
//
// On the wire its approval is a permission like any other, so helios used to
// paint "Allow once" and "Deny" over it. It is not a yes-or-no question: the
// CLI's own dialog offers the mode to continue in, and a way to send the plan
// back with a reason. Helios leaves that dialog alone and answers it for a
// remote surface. See docs/specs/57-plan-approval.md.
const exitPlanModeTool = "ExitPlanMode"

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
// meaning. There is more than one per row because the copy is the CLI's to
// change and it has: 2.1.259 offers "Yes, and use auto mode", 2.1.126 offers
// "Yes, auto-accept edits". Helios has to answer whichever is installed, so it
// tries each in turn.
var planChoices = map[string]struct {
	dialogRows []string
	mode       string
}{
	planChoiceAuto:   {dialogRows: []string{"auto-accept", "auto mode"}, mode: "auto"},
	planChoiceManual: {dialogRows: []string{"manually approve"}, mode: "manual"},
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

// planDialogSentinel is the CLI's own question, and the proof that its dialog
// is the thing on screen. Nothing is pressed before it appears: a digit sent at
// any other moment is a digit typed into the composer.
const planDialogSentinel = "would you like to proceed"

// planRowNumber reads the number off a numbered dialog row: "❯ 1. Yes, …" is
// answered by pressing 1.
var planRowNumber = regexp.MustCompile(`^[^0-9]*([1-9])\.`)

// planDigits maps a row's number to the key that presses it. The CLI has never
// offered a fourth row, and a plan whose rows ran past three would need reading
// before it was answered anyway.
var planDigits = map[string]backend.Key{"1": backend.Key1, "2": backend.Key2, "3": backend.Key3}

// answerPlanDialog picks a row on the CLI's own plan dialog.
//
// A plan is the one permission the CLI will not let a hook decide: an "allow"
// for ExitPlanMode is ignored and the dialog is shown regardless. Measured
// against Claude Code 2.1.259 — an allow, with and without a setMode permission
// update, left the dialog up and the plan unstarted. So helios replies "ask" to
// get out of the CLI's way and presses the row the user chose.
//
// The row is pressed by its number rather than by walking the highlight onto
// it. The composer's own "❯" sits below the dialog, and a highlight walk reads
// that as the cursor: it then presses Up until it gives up, having moved the
// selection somewhere nobody asked for.
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
	for time.Now().Before(deadline) {
		if key, found := planRowKey(b, sessionID, row.dialogRows); found {
			if err := b.SendKey(sessionID, key); err != nil {
				log.Printf("hook: press %q on the plan dialog for %s: %v", key, sessionID, err)
			}
			return
		}
		time.Sleep(planDialogPoll)
	}
	// The screen goes in the log because without it a miss says only that the
	// rows were not found, and every cause looks the same from there: a renamed
	// row, a dialog that never came, a capture that read something else.
	log.Printf("hook: answer the plan dialog for %s with %v: no row matched\nlast screen:\n%s",
		sessionID, row.dialogRows, lastScreen(b, sessionID))
}

// planRowKey finds the digit that answers the wanted row, once the CLI's dialog
// is the thing on screen.
func planRowKey(b backend.Backend, sessionID string, want []string) (backend.Key, bool) {
	screen, err := b.Capture(sessionID)
	if err != nil {
		return "", false
	}
	lower := strings.ToLower(screen)
	if !strings.Contains(lower, planDialogSentinel) {
		return "", false
	}
	for _, line := range strings.Split(lower, "\n") {
		number := planRowNumber.FindStringSubmatch(strings.TrimSpace(line))
		if number == nil {
			continue
		}
		for _, w := range want {
			if strings.Contains(line, w) {
				key, ok := planDigits[number[1]]
				return key, ok
			}
		}
	}
	return "", false
}

// planScreenLines is how much of the screen a failure is worth. The rows sit at
// the bottom, so the tail is the part that would have carried them.
const planScreenLines = 15

func lastScreen(b backend.Backend, sessionID string) string {
	screen, err := b.Capture(sessionID)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	var kept []string
	for _, line := range strings.Split(screen, "\n") {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimRight(line, " "))
		}
	}
	if len(kept) > planScreenLines {
		kept = kept[len(kept)-planScreenLines:]
	}
	return strings.Join(kept, "\n")
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
