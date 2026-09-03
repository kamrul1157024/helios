package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

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

// The rows helios draws for a plan, in the CLI's own words. The first two
// differ only in the mode they leave the session in. The third is not a choice
// at all: it labels the row that opens the answer field.
const (
	planAcceptEdits = "Yes, auto-accept edits"
	planManualEdits = "Yes, manually approve edits"
	planFeedback    = "Tell Claude what to change"
)

const (
	planAcceptEditsDetail = "Claude edits files without asking for the rest of this session"
	planManualEditsDetail = "Claude asks before each edit, as it does now"
)

// The permission modes the two "Yes" rows switch a session to, spelled the way
// the CLI's setMode update spells them. Note that these are not the names
// helios stores; see heliosMode.
const (
	modeAcceptEdits = "acceptEdits"
	modeDefault     = "default"
)

// planTitle names the box after the decision rather than after the tool. A
// tool name is the right label for Bash. Here it would name the mechanism.
const planTitle = "Ready to code?"

// planPrompt is the approval a plan gets.
func planPrompt(input *hookInput) hitl.Prompt {
	return hitl.Prompt{
		Title:     planTitle,
		Body:      planBody(input.ToolInput),
		Choices:   []string{planAcceptEdits, planManualEdits},
		Details:   []string{planAcceptEditsDetail, planManualEditsDetail},
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

// setModeUpdate is the permission update that leaves the session in mode.
//
// The shape is the CLI's own: its plan dialog sends exactly this through
// permissionUpdates when the user picks one of the "Yes" rows. Measured against
// Claude Code 2.1.259.
func setModeUpdate(mode string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`[{"type":"setMode","destination":"session","mode":%q}]`, mode))
}

// approvedWithMode is a "Yes" row: the plan is approved, and the session
// continues in mode rather than in the mode plan mode was entered from.
func approvedWithMode(mode string) notifications.Decision {
	return notifications.Decision{
		Status:   "approved",
		Response: answerJSON(permissionAnswer{PermissionUpdates: setModeUpdate(mode)}),
	}
}

// deniedWithFeedback sends the plan back with the user's own words.
func deniedWithFeedback(text string) notifications.Decision {
	return notifications.Decision{
		Status:   "denied",
		Response: answerJSON(permissionAnswer{Feedback: text}),
	}
}

// recordSessionMode writes a mode switch back to the session record.
//
// The CLI applies a setMode update to the running process only. Helios repeats
// --permission-mode from the record on every resume (see ResumeArgs), so
// without this a session that left plan mode would wake back up inside it.
func recordSessionMode(ctx *provider.HookContext, sessionID string, updates json.RawMessage) {
	mode := heliosMode(setModeOf(updates))
	if mode == "" {
		return
	}
	if err := ctx.DB.UpdateSessionPermissionMode(sessionID, mode); err != nil {
		log.Printf("hook: record permission mode %q for %s: %v", mode, sessionID, err)
	}
}

// setModeOf returns the mode a set of permission updates switches to, or ""
// when none of them does. Rules and directories are not a mode change.
func setModeOf(updates json.RawMessage) string {
	if len(updates) == 0 {
		return ""
	}
	var list []struct {
		Type string `json:"type"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(updates, &list); err != nil {
		return ""
	}
	for _, u := range list {
		if u.Type == "setMode" && u.Mode != "" {
			return u.Mode
		}
	}
	return ""
}

// heliosMode maps the CLI's name for a permission mode onto the one helios
// stores, and returns "" for a mode helios cannot launch with.
//
// The two vocabularies agree on all but one name: setMode calls the
// ask-each-time mode "default", and --permission-mode calls it "manual".
func heliosMode(cliMode string) string {
	if cliMode == modeDefault {
		return "manual"
	}
	if ValidPermissionMode(cliMode) {
		return cliMode
	}
	return ""
}
