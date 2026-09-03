package claude

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/terminal"
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
const cliPlanDialog = `Claude has written up a plan and is ready to execute. Would you like to proceed?
❯ 1. Yes, and use auto mode
  2. Yes, manually approve edits
  3. Tell Claude what to change
      shift+tab to approve with this feedback
  ctrl+g to edit in Vim · ~/.claude/plans/rows.md`

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

// The bug this closes: a plan is not a yes-or-no question, but helios painted
// Allow once / Deny over it, so neither mode could be picked and disagreeing
// meant a bare no.
func TestPlan_PaintsTheModeRowsAndTheFeedbackRow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, o := askPermission(t, ctx, overlays, planHook(samplePlan))

	if o.Title != planTitle {
		t.Errorf("title = %q, want %q — the tool name names the mechanism, not the decision", o.Title, planTitle)
	}
	want := []string{planAutoMode, planManualEdits}
	if len(o.Options) != len(want) || o.Options[0] != want[0] || o.Options[1] != want[1] {
		t.Errorf("options = %v, want %v", o.Options, want)
	}
	if len(o.Details) != 2 || o.Details[0] == "" || o.Details[1] == "" {
		t.Errorf("details = %v, want a line under each mode", o.Details)
	}
	if o.Input == nil || o.Input.Label != planFeedback {
		t.Errorf("input row = %v, want one labelled %q", o.Input, planFeedback)
	}

	// The plan is one body entry per line. Passed whole it would be reflowed
	// into a single paragraph and the headings would vanish.
	if len(o.Body) < 5 {
		t.Errorf("body = %v, want the plan split across lines", o.Body)
	}
	if o.Body[0] != "# Plan: give plan approval its own rows" {
		t.Errorf("body[0] = %q, want the plan's first line", o.Body[0])
	}

	hitlCtl.HandleInput("sess-1", []byte("\x1b"))
	awaitResponse(t, done)
}

// The CLI will not let a hook approve a plan: an allow for ExitPlanMode is
// ignored and its own dialog is shown regardless. So helios answers "ask" and
// presses the row the user picked.
func TestPlan_AutoModeIsPressedOnTheCLIsOwnDialog(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)
	terminalOf(ctx).setScreen(cliPlanDialog)

	done, _ := askPermission(t, ctx, overlays, planHook(samplePlan))
	hitlCtl.HandleInput("sess-1", []byte("\r")) // the first row is preselected

	decision := permDecision(t, awaitResponse(t, done)).HookSpecificOutput.Decision
	// Not "allow": the CLI ignores that here, and claiming it would hide the
	// keystroke that does the work.
	if decision.Behavior != "ask" {
		t.Fatalf("behavior = %q, want ask", decision.Behavior)
	}
	// Row one is already highlighted, so Enter is the whole answer.
	if got := awaitKeys(t, terminalOf(ctx), 1); got[0] != "sess-1:enter" {
		t.Errorf("keys = %v, want Enter on the highlighted row", got)
	}
	// The CLI applies the mode to itself. Helios repeats --permission-mode from
	// the record on resume, so it has to know too, or the session wakes back up
	// in plan mode.
	if got := sessionMode(t, db, "sess-1"); got != "auto" {
		t.Errorf("recorded mode = %q, want auto", got)
	}
}

func TestPlan_ManualApprovalWalksToTheSecondRow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)
	terminalOf(ctx).setScreen(cliPlanDialog)

	done, _ := askPermission(t, ctx, overlays, planHook(samplePlan))
	hitlCtl.HandleInput("sess-1", []byte("\x1b[B"))
	awaitOverlay(t, overlays)
	hitlCtl.HandleInput("sess-1", []byte("\r"))

	if got := permDecision(t, awaitResponse(t, done)).HookSpecificOutput.Decision.Behavior; got != "ask" {
		t.Fatalf("behavior = %q, want ask", got)
	}
	// The highlight starts on row one, so row two is a Down and then Enter.
	got := awaitKeys(t, terminalOf(ctx), 2)
	if got[0] != "sess-1:down" || got[1] != "sess-1:enter" {
		t.Errorf("keys = %v, want the highlight walked down, then Enter", got)
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
	overlays, hitlCtl := withTerminal(ctx)
	terminalOf(ctx).setScreen("a screen with no plan dialog on it")

	done, _ := askPermission(t, ctx, overlays, planHook(samplePlan))
	hitlCtl.HandleInput("sess-1", []byte("\r"))

	if got := permDecision(t, awaitResponse(t, done)).HookSpecificOutput.Decision.Behavior; got != "ask" {
		t.Errorf("behavior = %q, want ask so the CLI keeps its own dialog", got)
	}
	if got := terminalOf(ctx).sentKeys(); len(got) != 0 {
		t.Errorf("keys = %v, want nothing pressed into a screen we did not recognise", got)
	}
}

// Disagreeing with a plan is the point of the feedback row: Claude re-plans
// from the words rather than stopping.
func TestPlan_TypedFeedbackReachesClaude(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, _ := askPermission(t, ctx, overlays, planHook(samplePlan))

	// Down past both modes onto the feedback row, Enter to open the field.
	hitlCtl.HandleInput("sess-1", []byte("\x1b[B"))
	awaitOverlay(t, overlays)
	hitlCtl.HandleInput("sess-1", []byte("\x1b[B"))
	awaitOverlay(t, overlays)
	hitlCtl.HandleInput("sess-1", []byte("\r"))
	if o := awaitOverlay(t, overlays); o.Input == nil || !o.Input.Active {
		t.Fatalf("input = %v, want the field open after Enter on its row", o.Input)
	}
	hitlCtl.HandleInput("sess-1", []byte("split the plan on newlines"))
	awaitOverlay(t, overlays)
	hitlCtl.HandleInput("sess-1", []byte("\r"))

	decision := permDecision(t, awaitResponse(t, done)).HookSpecificOutput.Decision
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

// Escape on a plan is a refusal without a word. A bare "Denied via helios"
// reads as stop; the plan is how Claude asks to start.
func TestPlan_EscapeAsksClaudeWhatToChange(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, _ := askPermission(t, ctx, overlays, planHook(samplePlan))
	hitlCtl.HandleInput("sess-1", []byte("\x1b"))

	decision := permDecision(t, awaitResponse(t, done)).HookSpecificOutput.Decision
	if decision.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny", decision.Behavior)
	}
	if !strings.Contains(decision.Message, exitPlanModeTool) {
		t.Errorf("message = %q, want it to name the way back", decision.Message)
	}
}

// An ordinary tool keeps the rows it had; only ExitPlanMode changes.
func TestPlan_OrdinaryToolsKeepAllowAndDeny(t *testing.T) {
	p := permissionPrompt(&hookInput{
		ToolName:  "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
	})
	if p.Title != "Bash" || p.AllowText {
		t.Errorf("prompt = %+v, want the tool name and no answer field", p)
	}
	if len(p.Choices) != 2 || p.Choices[0] != allowOnce || p.Choices[1] != denyChoice {
		t.Errorf("choices = %v, want allow/deny", p.Choices)
	}
}

func TestPlan_LongPlanIsCappedAndPointsAtTheFile(t *testing.T) {
	long := strings.Repeat("a line of the plan\n", planBodyMaxLines+6)
	body := planBody(planInput(long, "~/.claude/plans/rows.md"))

	// One line over the cap: the cap line itself.
	if len(body) != planBodyMaxLines+1 {
		t.Fatalf("body has %d lines, want %d plus the cap line", len(body), planBodyMaxLines)
	}
	tail := body[len(body)-1]
	if !strings.Contains(tail, "6 more lines") {
		t.Errorf("tail = %q, want the count of what was cut", tail)
	}
	// The overlay clips from the bottom of the viewport upwards, so a capped
	// plan has to say where the whole one lives.
	if !strings.Contains(tail, "~/.claude/plans/rows.md") {
		t.Errorf("tail = %q, want the plan's path", tail)
	}
}

func TestPlan_ShortPlanShowsThePathAlone(t *testing.T) {
	body := planBody(planInput("# Small\n", "~/.claude/plans/small.md"))
	if got := body[len(body)-1]; got != "~/.claude/plans/small.md" {
		t.Errorf("tail = %q, want the path with no cut-count", got)
	}
}

// The cap exists for the screen, so it is checked on the screen: the box is
// anchored to the bottom of the viewport and clipped from the top, and a plan
// long enough to push the rows off would leave nothing to answer with.
func TestPlan_TheRowsSurviveALongPlanOnASmallScreen(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	long := "# Plan\n" + strings.Repeat("a line of the plan\n", 60)
	done, o := askPermission(t, ctx, overlays, planHook(long))

	// 80x24 is the small end of a real terminal, not a corner case.
	painted := string(terminal.RenderOverlay(o, 80, 24))
	for _, want := range []string{planAutoMode, planManualEdits, planFeedback} {
		if !strings.Contains(painted, want) {
			t.Errorf("the row %q was clipped off an 80x24 screen:\n%s", want, painted)
		}
	}

	hitlCtl.HandleInput("sess-1", []byte("\x1b"))
	awaitResponse(t, done)
}

// A plan with no text at all still has to paint something.
func TestPlan_WithoutAPlanFallsBackToTheToolInput(t *testing.T) {
	body := planBody(json.RawMessage(`{"other":"field"}`))
	if len(body) != 1 || body[0] == "" {
		t.Errorf("body = %v, want a single summary line", body)
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
// off the front of the list. Nothing set AllowText on a permission before, so
// this was latent until the feedback row.
func TestPlan_ATypedAnswerDoesNotIndexTheChoices(t *testing.T) {
	choices := []string{planAutoMode, planManualEdits}
	got := terminalDecision(choices, hitl.Answer{Index: -1, Text: "  change it  "})

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

// A row's label is the CLI's copy and changes with its releases, so what
// travels between surfaces is the short name.
func TestPlan_PlanChoiceNames(t *testing.T) {
	cases := map[string]string{
		planAutoMode:    planChoiceAuto,
		planManualEdits: planChoiceManual,
		planFeedback:    "",
		allowOnce:       "",
	}
	for row, want := range cases {
		if got := planChoiceOf(row); got != want {
			t.Errorf("planChoiceOf(%q) = %q, want %q", row, got, want)
		}
	}
}

// The phone answers over the same contract as the terminal, so it can pick a
// mode or send the plan back in words.
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
