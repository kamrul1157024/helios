package codex

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
	"github.com/kamrul1157024/helios/internal/terminal"
)

// The bug these close: Codex's own approval dialog offers "No, and tell Codex
// what to do differently", and helios painted a bare Deny over it. Every
// refusal reached the model as "Denied via helios", whoever refused and
// whatever they meant.

const codexSession = "01a04dee-183a-7461-9bef-5f05c0aa510a"

// codexOverlays stands in for a session's terminal, recording what helios drew
// over it. Painting happens on the hook's goroutine while the test drives keys
// from its own, so both directions travel by channel.
type codexOverlays struct {
	painted chan terminal.Overlay
	cleared chan string
}

func (p *codexOverlays) SetOverlay(sessionID string, o terminal.Overlay) error {
	p.painted <- o
	return nil
}

func (p *codexOverlays) ClearOverlay(sessionID string) error {
	p.cleared <- sessionID
	return nil
}

// OverlayProtocol reports a host of this build, so the answer field is painted
// rather than dropped and left to the phone.
func (p *codexOverlays) OverlayProtocol(sessionID string) int {
	return terminal.HostProtocol
}

func permCtx(t *testing.T) (*provider.HookContext, *codexOverlays, *hitl.Controller) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	overlays := &codexOverlays{
		painted: make(chan terminal.Overlay, 8),
		cleared: make(chan string, 8),
	}
	ctl := hitl.NewController(overlays)
	ctx := &provider.HookContext{
		DB:             db,
		Mgr:            notifications.NewManager(db),
		HITL:           ctl,
		Notify:         func(string, interface{}) {},
		Report:         func(provider.ReportEvent) {},
		SessionStarted: func(string) {},
	}
	return ctx, overlays, ctl
}

// askPerm runs the permission hook and waits for its overlay to land.
func askPerm(t *testing.T, ctx *provider.HookContext, p *codexOverlays,
	tool string) (<-chan *httptest.ResponseRecorder, terminal.Overlay) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"session_id": codexSession,
		"cwd":        "/tmp/proj",
		"tool_name":  tool,
		"tool_input": map[string]string{"command": "rm -rf build"},
	})
	if err != nil {
		t.Fatalf("encode hook input: %v", err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		handlePermission(ctx, w, httptest.NewRequest("POST", "/hooks/codex/permission", nil), body)
		done <- w
	}()
	return done, awaitPainted(t, p)
}

func awaitPainted(t *testing.T, p *codexOverlays) terminal.Overlay {
	t.Helper()
	select {
	case o := <-p.painted:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was painted on the terminal")
		return terminal.Overlay{}
	}
}

func awaitPermResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) permResponse {
	t.Helper()
	select {
	case w := <-done:
		var resp permResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response %q: %v", w.Body.String(), err)
		}
		return resp
	case <-time.After(5 * time.Second):
		t.Fatal("the hook never answered Codex")
		return permResponse{}
	}
}

func TestPermission_TheOverlayOffersAWayToDisagree(t *testing.T) {
	ctx, overlays, ctl := permCtx(t)

	done, o := askPerm(t, ctx, overlays, "shell")

	if len(o.Options) != 2 {
		t.Errorf("choices = %v, want allow and deny only", o.Options)
	}
	if o.Input == nil || o.Input.Label != feedbackLabel {
		t.Errorf("input row = %v, want one labelled %q", o.Input, feedbackLabel)
	}

	ctl.HandleInput(codexSession, []byte("\x1b"))
	awaitPermResponse(t, done)
}

// The words are the point: Codex reads them and picks a different way rather
// than stopping.
func TestPermission_TypedWordsReachCodex(t *testing.T) {
	ctx, overlays, ctl := permCtx(t)

	done, _ := askPerm(t, ctx, overlays, "shell")

	// Down past both choices onto the feedback row, Enter to open the field.
	ctl.HandleInput(codexSession, []byte("\x1b[B"))
	awaitPainted(t, overlays)
	ctl.HandleInput(codexSession, []byte("\x1b[B"))
	awaitPainted(t, overlays)
	ctl.HandleInput(codexSession, []byte("\r"))
	if o := awaitPainted(t, overlays); o.Input == nil || !o.Input.Active {
		t.Fatalf("input = %v, want the field open after Enter on its row", o.Input)
	}
	ctl.HandleInput(codexSession, []byte("delete the directory, do not rm it"))
	awaitPainted(t, overlays)
	ctl.HandleInput(codexSession, []byte("\r"))

	decision := awaitPermResponse(t, done).HookSpecificOutput.Decision
	if decision.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny — words refuse the tool", decision.Behavior)
	}
	if !strings.Contains(decision.Message, "delete the directory, do not rm it") {
		t.Errorf("message = %q, want the typed words", decision.Message)
	}
	// A refused call comes back as an error, so the words need someone
	// attached to them or they read as a malfunction.
	if !strings.Contains(decision.Message, deniedFeedback) {
		t.Errorf("message = %q, want it to say who is speaking", decision.Message)
	}
}

func TestPermission_EscapeStillDeniesWithoutWords(t *testing.T) {
	ctx, overlays, ctl := permCtx(t)

	done, _ := askPerm(t, ctx, overlays, "shell")
	ctl.HandleInput(codexSession, []byte("\x1b"))

	decision := awaitPermResponse(t, done).HookSpecificOutput.Decision
	if decision.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny", decision.Behavior)
	}
	if decision.Message != deniedReason {
		t.Errorf("message = %q, want %q", decision.Message, deniedReason)
	}
}

func TestPermission_AllowOnceStillAllows(t *testing.T) {
	ctx, overlays, ctl := permCtx(t)

	done, _ := askPerm(t, ctx, overlays, "shell")
	ctl.HandleInput(codexSession, []byte("\r")) // "Allow once" is preselected

	decision := awaitPermResponse(t, done).HookSpecificOutput.Decision
	if decision.Behavior != "allow" {
		t.Errorf("behavior = %q, want allow", decision.Behavior)
	}
	if decision.Message != "" {
		t.Errorf("message = %q, want none on an approval", decision.Message)
	}
}

// The phone sends the same words the overlay's field takes, and the daemon
// cannot tell which surface answered.
func TestPermission_ThePhoneCanSayWhatToDoDifferently(t *testing.T) {
	decision, err := handlePermissionAction(&store.Notification{},
		json.RawMessage(`{"action":"deny","feedback":"use git clean instead"}`))
	if err != nil {
		t.Fatalf("handlePermissionAction: %v", err)
	}
	if decision.Status != "denied" {
		t.Fatalf("status = %q, want denied", decision.Status)
	}
	msg := denyMessage(codexSession, &decision)
	if !strings.Contains(msg, "use git clean instead") {
		t.Errorf("message = %q, want the phone's words", msg)
	}
}

func TestPermission_AnApprovalDropsAnyWordsSentWithIt(t *testing.T) {
	// Feedback is a refusal's payload. Carried onto an approval it would send
	// Codex a complaint about work it was just cleared to do.
	decision, err := handlePermissionAction(&store.Notification{},
		json.RawMessage(`{"action":"approve","feedback":"use git clean instead"}`))
	if err != nil {
		t.Fatalf("handlePermissionAction: %v", err)
	}
	if decision.Status != "approved" {
		t.Fatalf("status = %q, want approved", decision.Status)
	}
	if len(decision.Response) != 0 {
		t.Errorf("response = %s, want nothing carried onto an approval", decision.Response)
	}
}

func TestPermission_ABareNoKeepsItsOldWords(t *testing.T) {
	decision, err := handlePermissionAction(&store.Notification{},
		json.RawMessage(`{"action":"deny"}`))
	if err != nil {
		t.Fatalf("handlePermissionAction: %v", err)
	}
	if got := denyMessage(codexSession, &decision); got != deniedReason {
		t.Errorf("message = %q, want %q", got, deniedReason)
	}
}

// An answer helios cannot read is a no, not a crash and not a yes.
func TestPermission_AnUnreadableAnswerDenies(t *testing.T) {
	choices := []string{allowOnce, denyChoice}
	for name, a := range map[string]hitl.Answer{
		"cancelled":     {Index: -1},
		"row that went": {Index: 7},
	} {
		t.Run(name, func(t *testing.T) {
			if got := terminalDecision(choices, a).Status; got != "denied" {
				t.Errorf("status = %q, want denied", got)
			}
		})
	}

	broken := notifications.Decision{Status: "denied", Response: json.RawMessage(`{`)}
	if got := denyMessage(codexSession, &broken); got != deniedReason {
		t.Errorf("message = %q, want the bare reason for unreadable feedback", got)
	}
}
