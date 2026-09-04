package claude

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// samplePlan is what the CLI puts in tool_input.plan: markdown, several lines.
const samplePlan = "# Plan: give plan approval its own rows\n\n" +
	"## Context\nThe terminal only offers Allow once and Deny.\n\n" +
	"## Steps\n1. Name the rows.\n2. Collect the feedback.\n"

func planInput(plan, path string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"plan": plan, "planFilePath": path})
	if err != nil {
		panic(err)
	}
	return raw
}

func planHook(plan string) hookInput {
	return hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: exitPlanModeTool,
		PermissionMode: "plan",
		ToolInput:      planInput(plan, "~/.claude/plans/rows.md"),
	}
}

// cliPlanDialog is the dialog Claude Code 2.1.259 draws for a plan, which
// helios answers with a keystroke because the CLI ignores an allow for
// ExitPlanMode. Copied from a real session.
//
// The composer sits under it, carrying a cursor mark of its own. That mark is
// why the row is pressed by number: a highlight walk reads the lowest "❯" on
// the screen as the dialog's selection and marches away from the rows.
const cliPlanDialog = `Claude has written up a plan and is ready to execute. Would you like to proceed?
❯ 1. Yes, and use auto mode
  2. Yes, manually approve edits
  3. Tell Claude what to change
      shift+tab to approve with this feedback
  ctrl+g to edit in Vim · ~/.claude/plans/rows.md
────────────────────────────────────────────────
❯ what shall we do next
────────────────────────────────────────────────`

// The same dialog on Claude Code 2.1.126, which words the first row
// differently. Approving was a no-op against this build: the match looked for
// "auto mode" and the row says "auto-accept edits".
const cliPlanDialogAutoAccept = `Claude has written up a plan and is ready to execute. Would you like to proceed?
❯ 1. Yes, auto-accept edits
  2. Yes, manually approve edits
  3. Tell Claude what to change
      shift+tab to approve with this feedback`

// awaitKeys waits for the handoff goroutine to finish pressing rows.
func awaitKeys(t *testing.T, f *fakeBackend, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.sentKeys(); len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("only %v reached the CLI's dialog, want %d keystrokes", f.sentKeys(), want)
	return nil
}

// sessionMode reads back the mode helios would resume the session with.
func sessionMode(t *testing.T, db *store.Store, sessionID string) string {
	t.Helper()
	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil {
		t.Fatalf("GetSession(%q): %v", sessionID, err)
	}
	if sess.PermissionMode == nil {
		return ""
	}
	return *sess.PermissionMode
}

// answerFromPhone runs the permission hook and resolves its notification the
// way a remote surface does. A plan has no terminal box to answer any more, so
// this is the only way in besides the CLI's own dialog.
func answerFromPhone(t *testing.T, ctx *provider.HookContext, input hookInput,
	decision notifications.Decision) permResponse {
	t.Helper()
	ids := captureNotifIDs(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callHook(handlePermission, ctx, input) }()

	if err := ctx.Mgr.Resolve(awaitNotifID(t, ids), decision, "mobile"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return permDecision(t, awaitResponse(t, done))
}

// The bug this closes: helios drew its own approval box over the dialog the CLI
// draws for a plan. The two composite into one unreadable screen — which is
// also the screen helios reads back when it presses a row for the phone.
func TestPlan_PaintsNoBoxOverTheCLIsOwnDialog(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, _ := withTerminal(ctx)
	terminalOf(ctx).setScreen(cliPlanDialog)

	answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "denied"})

	select {
	case o := <-overlays.painted:
		t.Errorf("helios painted %q over the CLI's own plan dialog", o.Title)
	default:
	}
}

// An ordinary tool keeps its box: only a plan has a dialog of the CLI's own to
// leave alone.
func TestPlan_OrdinaryToolsKeepTheirBox(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, o := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
	})
	if o.Title != "Bash" {
		t.Errorf("title = %q, want the tool name", o.Title)
	}
	hitlCtl.HandleInput("sess-1", []byte("\x1b"))
	awaitResponse(t, done)
}

// The CLI will not let a hook approve a plan: an allow for ExitPlanMode is
// ignored and its own dialog is shown regardless. So helios answers "ask" and
// presses the row the phone picked.
func TestPlan_AutoModePressesTheFirstRow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)
	terminalOf(ctx).setScreen(cliPlanDialog)

	resp := answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "approved",
			Response: json.RawMessage(`{"plan_choice":"auto"}`)})

	// Not "allow": the CLI ignores that here, and claiming it would hide the
	// keystroke that does the work.
	if got := resp.HookSpecificOutput.Decision.Behavior; got != "ask" {
		t.Fatalf("behavior = %q, want ask", got)
	}
	if got := awaitKeys(t, terminalOf(ctx), 1); got[0] != "sess-1:1" {
		t.Errorf("keys = %v, want the first row pressed by number", got)
	}
	// The CLI applies the mode to itself. Helios repeats --permission-mode from
	// the record on resume, so it has to know too, or the session wakes back up
	// in plan mode.
	if got := sessionMode(t, db, "sess-1"); got != "auto" {
		t.Errorf("recorded mode = %q, want auto", got)
	}
}

// The row's copy belongs to the CLI and has already moved once.
func TestPlan_AutoModeIsPressedWhateverTheCLICallsIt(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)
	terminalOf(ctx).setScreen(cliPlanDialogAutoAccept)

	answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "approved",
			Response: json.RawMessage(`{"plan_choice":"auto"}`)})

	if got := awaitKeys(t, terminalOf(ctx), 1); got[0] != "sess-1:1" {
		t.Errorf("keys = %v, want the first row pressed by number", got)
	}
	if got := sessionMode(t, db, "sess-1"); got != "auto" {
		t.Errorf("recorded mode = %q, want auto", got)
	}
}

func TestPlan_ManualApprovalPressesTheSecondRow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)
	terminalOf(ctx).setScreen(cliPlanDialog)

	answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "approved",
			Response: json.RawMessage(`{"plan_choice":"manual"}`)})

	// Two, not "one Down then Enter": the highlight is not walked, so the
	// composer's own cursor mark cannot be mistaken for the dialog's.
	if got := awaitKeys(t, terminalOf(ctx), 1); got[0] != "sess-1:2" {
		t.Errorf("keys = %v, want the second row pressed by number", got)
	}
	if got := sessionMode(t, db, "sess-1"); got != "manual" {
		t.Errorf("recorded mode = %q, want manual", got)
	}
}

// A dialog that never appears, or a row the CLI has renamed, must not hang the
// session or fake an answer: the CLI's dialog is on screen and answerable by
// hand.
func TestPlan_AnUnreachableDialogLeavesTheSessionAnswerable(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)
	terminalOf(ctx).setScreen("a screen with no plan dialog on it")

	resp := answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "approved",
			Response: json.RawMessage(`{"plan_choice":"auto"}`)})

	if got := resp.HookSpecificOutput.Decision.Behavior; got != "ask" {
		t.Errorf("behavior = %q, want ask so the CLI keeps its own dialog", got)
	}
	if got := terminalOf(ctx).sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want nothing pressed into a screen we did not recognise", got)
	}
}

// The rows alone are not enough to press one: a numbered list is an ordinary
// shape, and a digit typed into the composer is a digit in the next prompt.
func TestPlan_RowsWithoutTheCLIsQuestionArePressedNothing(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)
	terminalOf(ctx).setScreen("1. Yes, and use auto mode\n2. Yes, manually approve edits")

	answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "approved",
			Response: json.RawMessage(`{"plan_choice":"auto"}`)})

	if got := terminalOf(ctx).sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want nothing pressed without the CLI's own question", got)
	}
}

// Disagreeing with a plan is the point of the feedback field: Claude re-plans
// from the words rather than stopping.
func TestPlan_TypedFeedbackReachesClaude(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)

	resp := answerFromPhone(t, ctx, planHook(samplePlan),
		deniedWithFeedback("split the plan on newlines"))

	decision := resp.HookSpecificOutput.Decision
	if decision.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny — feedback sends the plan back", decision.Behavior)
	}
	if !strings.Contains(decision.Message, "split the plan on newlines") {
		t.Errorf("message = %q, want the typed words", decision.Message)
	}
	// A denied tool reaches the model as "Error: <message>", so the words need
	// someone attached to them or they read as a malfunction.
	if !strings.Contains(decision.Message, planFeedbacked) {
		t.Errorf("message = %q, want it to say who is speaking", decision.Message)
	}
	if got := sessionMode(t, db, "sess-1"); got != "" {
		t.Errorf("recorded mode = %q, want plan mode left alone on a refusal", got)
	}
}

// A refusal without a word. A bare "Denied via helios" reads as stop; the plan
// is how Claude asks to start.
func TestPlan_ABareRefusalAsksClaudeWhatToChange(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	withTerminal(ctx)

	resp := answerFromPhone(t, ctx, planHook(samplePlan),
		notifications.Decision{Status: "denied"})

	decision := resp.HookSpecificOutput.Decision
	if decision.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny", decision.Behavior)
	}
	if !strings.Contains(decision.Message, exitPlanModeTool) {
		t.Errorf("message = %q, want it to name the way back", decision.Message)
	}
}

// The push notification a phone shows used to be raw JSON cut at 100
// characters.
func TestPlan_NotificationDetailReadsAsThePlansTitle(t *testing.T) {
	got := summarizeToolInput(planInput(samplePlan, ""))
	if got != "Plan: give plan approval its own rows" {
		t.Errorf("summary = %q, want the plan's heading without the marks", got)
	}
}

// A typed answer carries Index -1, so indexing the choices with it would read
// off the front of the list.
func TestPlan_ATypedAnswerDoesNotIndexTheChoices(t *testing.T) {
	got := terminalDecision([]string{allowOnce, denyChoice},
		hitl.Answer{Index: -1, Text: "  change it  "})

	if got.Status != "denied" {
		t.Fatalf("status = %q, want denied", got.Status)
	}
	var answer permissionAnswer
	if err := json.Unmarshal(got.Response, &answer); err != nil {
		t.Fatalf("decode %s: %v", got.Response, err)
	}
	if answer.Feedback != "change it" {
		t.Errorf("feedback = %q, want the trimmed text", answer.Feedback)
	}
}

func TestPlan_AnAnswerOffTheEndDenies(t *testing.T) {
	for _, a := range []hitl.Answer{{Index: 9}, {Index: -1}, {Index: -3, Text: "   "}} {
		if got := terminalDecision([]string{allowOnce, denyChoice}, a); got.Status != "denied" {
			t.Errorf("terminalDecision(%+v) = %q, want denied", a, got.Status)
		}
	}
}

// The phone answers over the same contract as before, so it can pick a mode or
// send the plan back in words.
func TestPlan_ThePhoneCanAnswerAPlan(t *testing.T) {
	approve, err := handlePermissionAction(nil, json.RawMessage(
		`{"action":"approve","plan_choice":"auto"}`))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approve.Status != "approved" || !strings.Contains(string(approve.Response), `"plan_choice":"auto"`) {
		t.Errorf("approve = %+v, want the chosen mode carried through", approve)
	}

	deny, err := handlePermissionAction(nil, json.RawMessage(
		`{"action":"deny","feedback":"use a queue instead"}`))
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	if deny.Status != "denied" || !strings.Contains(string(deny.Response), "use a queue instead") {
		t.Errorf("deny = %+v, want the feedback carried through", deny)
	}
}

// An approval says yes. Feedback riding along with one would reach Claude as a
// complaint about work it was just cleared to do.
func TestPlan_AnApprovalDropsAnyFeedback(t *testing.T) {
	got, err := handlePermissionAction(nil, json.RawMessage(
		`{"action":"approve","feedback":"ignore me"}`))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(got.Response) != 0 {
		t.Errorf("response = %s, want nothing beyond the yes", got.Response)
	}
}
