package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

// ==================== Test infrastructure ====================

func openMemoryStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// fakeTmux satisfies tmux.TmuxClient. Tracks calls for assertions.
type fakeTmux struct {
	renames    []string // paneID+name concatenated
	kills      []string
	sentRaw    []string
	createPane string
	createErr  error
}

func (f *fakeTmux) Available() bool                                         { return true }
func (f *fakeTmux) RenameWindow(paneID, name string) error                  { f.renames = append(f.renames, paneID+":"+name); return nil }
func (f *fakeTmux) KillPane(paneID string) error                            { f.kills = append(f.kills, paneID); return nil }
func (f *fakeTmux) CreateWindow(cwd, command string) (string, error)        { return f.createPane, f.createErr }
func (f *fakeTmux) SetPaneSessionID(paneID, sessionID string) error         { return nil }
func (f *fakeTmux) SweepDeadPanes(m *tmux.PaneMap) []string                 { return nil }
func (f *fakeTmux) SendKeysRaw(paneID, keys string) error                   { f.sentRaw = append(f.sentRaw, paneID+":"+keys); return nil }

// setupCtx builds a HookContext wired to an in-memory store.
// SSE events are collected into sseEvents.
func setupCtx(t *testing.T) (*provider.HookContext, *store.Store, *[]string) {
	t.Helper()
	db := openMemoryStore(t)
	mgr := notifications.NewManager(db)
	pm := tmux.NewPaneMap()
	var sseEvents []string

	ctx := &provider.HookContext{
		DB:      db,
		Mgr:     mgr,
		PaneMap: pm,
		Tmux:    nil, // safe — renameSessionWindow/killSessionWindow guard nil
		Notify: func(eventType string, _ interface{}) {
			sseEvents = append(sseEvents, eventType)
		},
		Report:            func(provider.ReportEvent) {},
		RemovePendingPane: func(string) string { return "" },
	}
	return ctx, db, &sseEvents
}

// seedSession inserts a session into the store with the given status.
func seedSession(t *testing.T, db *store.Store, sessionID, cwd, status string) {
	t.Helper()
	sess := &store.Session{
		SessionID: sessionID,
		Source:    "claude",
		CWD:       cwd,
		Status:    status,
		LastEvent: strPtr("seed"),
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// callHook fires a hook handler with the given JSON body and returns the response.
func callHook(handler func(*provider.HookContext, http.ResponseWriter, *http.Request, json.RawMessage),
	ctx *provider.HookContext, body interface{}) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handler(ctx, w, req, json.RawMessage(raw))
	return w
}

// assertStatus reads the DB session and checks the status field.
func assertStatus(t *testing.T, db *store.Store, sessionID, want string) {
	t.Helper()
	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil {
		t.Fatalf("GetSession(%q): %v", sessionID, err)
	}
	if sess.Status != want {
		t.Errorf("status = %q, want %q", sess.Status, want)
	}
}

// ==================== SessionStart ====================

func TestSessionStart_CreatesSession_IdleStatus(t *testing.T) {
	ctx, db, _ := setupCtx(t)

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		Model:     "opus",
	})

	assertStatus(t, db, "sess-1", "idle")
	sess, _ := db.GetSession("sess-1")
	if sess.Model == nil || *sess.Model != "opus" {
		t.Errorf("model = %v, want opus", sess.Model)
	}
}

func TestSessionStart_ManagedFalse_WhenNoPaneKnown(t *testing.T) {
	ctx, db, _ := setupCtx(t)

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-unmanaged",
		CWD:       "/tmp/proj",
	})

	sess, _ := db.GetSession("sess-unmanaged")
	if sess.Managed {
		t.Error("managed = true, want false when no pane is known")
	}
}

func TestSessionStart_ManagedTrue_WhenPaneInPaneMap(t *testing.T) {
	ctx, db, sseEvents := setupCtx(t)
	ctx.PaneMap.Set("sess-managed", "%5")

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-managed",
		CWD:       "/tmp/proj",
	})

	sess, _ := db.GetSession("sess-managed")
	if !sess.Managed {
		t.Error("managed = false, want true when pane is in PaneMap")
	}

	found := false
	for _, e := range *sseEvents {
		if e == "session_status" {
			found = true
		}
	}
	if !found {
		t.Error("expected session_status SSE event")
	}
}

func TestSessionStart_ManagedTrue_WhenPaneFromPendingPanes(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	ctx.RemovePendingPane = func(cwd string) string {
		if cwd == "/tmp/proj" {
			return "%9"
		}
		return ""
	}

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-pending",
		CWD:       "/tmp/proj",
	})

	sess, _ := db.GetSession("sess-pending")
	if !sess.Managed {
		t.Error("managed = false, want true when pane comes from PendingPanes")
	}
	paneID, ok := ctx.PaneMap.Get("sess-pending")
	if !ok || paneID != "%9" {
		t.Errorf("PaneMap entry = %q %v, want %%9 true", paneID, ok)
	}
}

func TestSessionStart_SetsTranscriptPath(t *testing.T) {
	ctx, db, _ := setupCtx(t)

	callHook(handleSessionStart, ctx, hookInput{
		SessionID:      "sess-tp",
		CWD:            "/tmp/proj",
		TranscriptPath: "/tmp/transcript.jsonl",
	})

	sess, _ := db.GetSession("sess-tp")
	if sess.TranscriptPath == nil || *sess.TranscriptPath != "/tmp/transcript.jsonl" {
		t.Errorf("transcript_path = %v", sess.TranscriptPath)
	}
}

// ==================== PromptSubmit ====================

func TestPromptSubmit_TransitionsToActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handlePromptSubmit, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		Message:   "fix the auth bug",
	})

	assertStatus(t, db, "sess-1", "active")
	sess, _ := db.GetSession("sess-1")
	if sess.LastUserMessage == nil || *sess.LastUserMessage != "fix the auth bug" {
		t.Errorf("last_user_message = %v", sess.LastUserMessage)
	}
}

func TestPromptSubmit_EmptyMessage_DoesNotClearLastUserMessage(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-2", "/tmp/proj", "idle")
	db.UpdateSessionLastUserMessage("sess-2", "original message")

	callHook(handlePromptSubmit, ctx, hookInput{
		SessionID: "sess-2",
		CWD:       "/tmp/proj",
		Message:   "",
	})

	sess, _ := db.GetSession("sess-2")
	if sess.LastUserMessage == nil || *sess.LastUserMessage != "original message" {
		t.Errorf("last_user_message = %v, want original", sess.LastUserMessage)
	}
}

// ==================== Tool hooks ====================

func TestToolPre_SetsActiveWithToolName(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handleToolPre, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Bash",
	})

	assertStatus(t, db, "sess-1", "active")
	sess, _ := db.GetSession("sess-1")
	if sess.LastEvent == nil || *sess.LastEvent != "PreToolUse:Bash" {
		t.Errorf("last_event = %v, want PreToolUse:Bash", sess.LastEvent)
	}
}

func TestToolPost_StaysActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleToolPost, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Read",
	})

	assertStatus(t, db, "sess-1", "active")
	sess, _ := db.GetSession("sess-1")
	if sess.LastEvent == nil || *sess.LastEvent != "PostToolUse:Read" {
		t.Errorf("last_event = %v, want PostToolUse:Read", sess.LastEvent)
	}
}

func TestToolPostFailure_StaysActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleToolPostFailure, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Bash",
	})

	assertStatus(t, db, "sess-1", "active")
	sess, _ := db.GetSession("sess-1")
	if sess.LastEvent == nil || *sess.LastEvent != "PostToolUseFailure:Bash" {
		t.Errorf("last_event = %v, want PostToolUseFailure:Bash", sess.LastEvent)
	}
}

// ==================== Compaction ====================

func TestPreCompact_TransitionsToCompacting(t *testing.T) {
	ctx, db, sseEvents := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handlePreCompact, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	assertStatus(t, db, "sess-1", "compacting")
	found := false
	for _, e := range *sseEvents {
		if e == "session_status" {
			found = true
		}
	}
	if !found {
		t.Error("expected session_status SSE event on compact_pre")
	}
}

func TestPostCompact_TransitionsBackToActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "compacting")

	callHook(handlePostCompact, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	assertStatus(t, db, "sess-1", "active")
}

func TestCompactionCycle_PreThenPost(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handlePreCompact, ctx, hookInput{SessionID: "sess-1", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-1", "compacting")

	callHook(handlePostCompact, ctx, hookInput{SessionID: "sess-1", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-1", "active")
}

// ==================== Stop ====================

func TestStop_TransitionsToIdle(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStop, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	assertStatus(t, db, "sess-1", "idle")
}

func TestStop_CreatesDoneNotification(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStop, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	notifs, err := db.ListNotifications("claude", "", "claude.done")
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("want 1 claude.done notification, got %d", len(notifs))
	}
	if notifs[0].Status != "dismissed" {
		t.Errorf("notification status = %q, want dismissed", notifs[0].Status)
	}
}

func TestStop_ResolvesPendingSessionNotifications(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "waiting_permission")

	// Create a pending notification for this session.
	notifID := "notif-test-pending"
	title := "Approve?"
	detail := "tool: Bash"
	payload := `{"tool_name":"Bash"}`
	notif := &store.Notification{
		ID:            notifID,
		Source:        "claude",
		SourceSession: "sess-1",
		CWD:           "/tmp/proj",
		Type:          "claude.permission",
		Status:        "pending",
		Title:         &title,
		Detail:        &detail,
		Payload:       &payload,
	}
	if err := ctx.Mgr.CreateNotification(notif); err != nil {
		t.Fatalf("create notification: %v", err)
	}

	callHook(handleStop, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	assertStatus(t, db, "sess-1", "idle")
	got, _ := db.GetNotification(notifID)
	if got == nil || got.Status != "resolved" {
		t.Errorf("pending notification status = %v, want resolved", got)
	}
}

// ==================== StopFailure ====================

func TestStopFailure_TransitionsToError(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	assertStatus(t, db, "sess-1", "error")
}

func TestStopFailure_CreatesErrorNotification(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	notifs, err := db.ListNotifications("claude", "", "claude.error")
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("want 1 claude.error notification, got %d", len(notifs))
	}
	if notifs[0].Status != "pending" {
		t.Errorf("notification status = %q, want pending", notifs[0].Status)
	}
}

// ==================== SessionEnd ====================

func TestSessionEnd_TransitionsToTerminated(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handleSessionEnd, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	assertStatus(t, db, "sess-1", "terminated")
}

func TestSessionEnd_SetsEndedAt(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handleSessionEnd, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	sess, _ := db.GetSession("sess-1")
	if sess.EndedAt == nil || *sess.EndedAt == "" {
		t.Error("ended_at not set after SessionEnd")
	}
}

func TestSessionEnd_RemovesFromPaneMap(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	ctx.PaneMap.Set("sess-1", "%7")

	callHook(handleSessionEnd, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	if _, ok := ctx.PaneMap.Get("sess-1"); ok {
		t.Error("PaneMap still has entry after SessionEnd")
	}
}

func TestSessionEnd_BroadcastsSSEEvent(t *testing.T) {
	ctx, db, sseEvents := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleSessionEnd, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	found := false
	for _, e := range *sseEvents {
		if e == "session_status" {
			found = true
		}
	}
	if !found {
		t.Error("expected session_status SSE event on SessionEnd")
	}
}

// ==================== Notification hook ====================

func TestNotification_IdlePrompt_TransitionsToIdle(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleNotification, ctx, hookInput{
		SessionID:     "sess-1",
		CWD:           "/tmp/proj",
		HookEventName: "idle_prompt",
	})

	assertStatus(t, db, "sess-1", "idle")
}

func TestNotification_OtherEvent_DoesNotChangeStatus(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleNotification, ctx, hookInput{
		SessionID:     "sess-1",
		CWD:           "/tmp/proj",
		HookEventName: "some_other_event",
	})

	assertStatus(t, db, "sess-1", "active")
}

// ==================== Subagent lifecycle ====================

func TestSubagentStart_CreatesRecord(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleSubagentStart, ctx, hookInput{
		SessionID:   "sess-1",
		CWD:         "/tmp/proj",
		AgentID:     "agent-abc",
		AgentType:   "general-purpose",
		Description: "Exploring the codebase",
	})

	sub, err := db.GetSubagent("agent-abc")
	if err != nil || sub == nil {
		t.Fatalf("GetSubagent: %v", err)
	}
	if sub.Status != "active" {
		t.Errorf("subagent status = %q, want active", sub.Status)
	}
	if sub.AgentType == nil || *sub.AgentType != "general-purpose" {
		t.Errorf("agent_type = %v, want general-purpose", sub.AgentType)
	}
	if sub.Description == nil || *sub.Description != "Exploring the codebase" {
		t.Errorf("description = %v", sub.Description)
	}
}

func TestSubagentStop_CompletesRecord(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleSubagentStart, ctx, hookInput{
		SessionID: "sess-1",
		AgentID:   "agent-xyz",
	})
	callHook(handleSubagentStop, ctx, hookInput{
		SessionID: "sess-1",
		AgentID:   "agent-xyz",
	})

	sub, _ := db.GetSubagent("agent-xyz")
	if sub.Status != "completed" {
		t.Errorf("subagent status = %q, want completed", sub.Status)
	}
}

// ==================== Permission flow (blocking) ====================

// resolveAfter resolves a notification via the manager after a short delay,
// simulating an async mobile approval.
func resolveAfter(mgr *notifications.Manager, notifID, status string, delay time.Duration) {
	time.AfterFunc(delay, func() {
		mgr.Resolve(notifID, notifications.Decision{Status: status}, "mobile")
	})
}

func TestPermission_TransitionsToWaitingPermission(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	// Capture the notif ID by intercepting Notify.
	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolName:  "Bash",
		})
	}()

	// Wait until the handler registers the pending notification then check status.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess, _ := db.GetSession("sess-1")
		if sess != nil && sess.Status == "waiting_permission" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertStatus(t, db, "sess-1", "waiting_permission")

	// Unblock the handler.
	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "approved"}, "mobile")
	<-done
}

func TestPermission_Approve_ResumesActive_AndReturnsAllow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolName:  "Bash",
		})
	}()

	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "approved"}, "mobile")

	w := <-resultCh
	assertStatus(t, db, "sess-1", "active")

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	output := resp["hookSpecificOutput"].(map[string]interface{})
	decision := output["decision"].(map[string]interface{})
	if decision["behavior"] != "allow" {
		t.Errorf("behavior = %v, want allow", decision["behavior"])
	}
}

func TestPermission_Deny_ResumesActive_AndReturnsDeny(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolName:  "Bash",
		})
	}()

	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "denied"}, "mobile")

	w := <-resultCh
	assertStatus(t, db, "sess-1", "active")

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	output := resp["hookSpecificOutput"].(map[string]interface{})
	decision := output["decision"].(map[string]interface{})
	if decision["behavior"] != "deny" {
		t.Errorf("behavior = %v, want deny", decision["behavior"])
	}
}

func TestPermission_ClientDisconnect_CancelsNotification(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	// Request with a cancellable context.
	raw, _ := json.Marshal(hookInput{SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash"})
	req, cancel := makeRequestWithCancel(raw)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handlePermission(ctx, w, req, json.RawMessage(raw))
	}()

	// Wait until the notification is registered, then cancel the request context.
	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	// The notification should have been resolved (cancelled from claude side).
	got, _ := db.GetNotification(capturedNotifID)
	if got == nil || got.Status != "resolved" {
		t.Errorf("notification status after disconnect = %v, want resolved", got)
	}
}

// makeRequestWithCancel creates an http.Request whose context can be cancelled.
func makeRequestWithCancel(body []byte) (*http.Request, context.CancelFunc) {
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewReader(body))
	ctx, cancel := context.WithCancel(req.Context())
	return req.WithContext(ctx), cancel
}

// ==================== Question flow ====================

func TestQuestion_TransitionsToWaitingPermission(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	toolInput, _ := json.Marshal(map[string]string{"question": "Are you sure?"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolInput: toolInput,
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess, _ := db.GetSession("sess-1")
		if sess != nil && sess.Status == "waiting_permission" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertStatus(t, db, "sess-1", "waiting_permission")

	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "answered"}, "mobile")
	<-done
}

func TestQuestion_Answer_ReturnsAllow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	toolInput, _ := json.Marshal(map[string]string{"question": "Proceed?"})
	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolInput: toolInput,
		})
	}()

	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	answerPayload, _ := json.Marshal(map[string]string{"answer": "yes"})
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "answered", Response: answerPayload}, "mobile")

	w := <-resultCh
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	output := resp["hookSpecificOutput"].(map[string]interface{})
	if output["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v, want allow", output["permissionDecision"])
	}
}

func TestQuestion_Skip_ReturnsDeny(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	toolInput, _ := json.Marshal(map[string]string{"question": "Proceed?"})
	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolInput: toolInput,
		})
	}()

	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "denied"}, "mobile")

	w := <-resultCh
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	output := resp["hookSpecificOutput"].(map[string]interface{})
	if output["permissionDecision"] != "deny" {
		t.Errorf("permissionDecision = %v, want deny", output["permissionDecision"])
	}
}

// ==================== Window rename (with fake tmux) ====================

func TestStop_RenamesWindowToIdle(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	ft := &fakeTmux{}
	ctx.Tmux = ft
	ctx.PaneMap.Set("sess-1", "%3")
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStop, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	if len(ft.renames) == 0 {
		t.Error("expected RenameWindow call on Stop")
	}
}

func TestPromptSubmit_RenamesWindowToActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	ft := &fakeTmux{}
	ctx.Tmux = ft
	ctx.PaneMap.Set("sess-1", "%3")
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handlePromptSubmit, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		Message:   "do something",
	})

	if len(ft.renames) == 0 {
		t.Error("expected RenameWindow call on PromptSubmit")
	}
}

// ==================== Full lifecycle sequences ====================

func TestLifecycle_NormalSession(t *testing.T) {
	ctx, db, _ := setupCtx(t)

	// SessionStart
	callHook(handleSessionStart, ctx, hookInput{SessionID: "sess-L1", CWD: "/tmp/proj", Model: "sonnet"})
	assertStatus(t, db, "sess-L1", "idle")

	// PromptSubmit
	callHook(handlePromptSubmit, ctx, hookInput{SessionID: "sess-L1", CWD: "/tmp/proj", Message: "refactor auth"})
	assertStatus(t, db, "sess-L1", "active")

	// Tool cycle
	callHook(handleToolPre, ctx, hookInput{SessionID: "sess-L1", CWD: "/tmp/proj", ToolName: "Read"})
	assertStatus(t, db, "sess-L1", "active")
	callHook(handleToolPost, ctx, hookInput{SessionID: "sess-L1", CWD: "/tmp/proj", ToolName: "Read"})
	assertStatus(t, db, "sess-L1", "active")

	// Stop
	callHook(handleStop, ctx, hookInput{SessionID: "sess-L1", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L1", "idle")

	// SessionEnd
	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-L1", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L1", "terminated")

	sess, _ := db.GetSession("sess-L1")
	if sess.EndedAt == nil {
		t.Error("ended_at not set after full lifecycle")
	}
}

func TestLifecycle_WithCompaction(t *testing.T) {
	ctx, db, _ := setupCtx(t)

	callHook(handleSessionStart, ctx, hookInput{SessionID: "sess-L2", CWD: "/tmp/proj"})
	callHook(handlePromptSubmit, ctx, hookInput{SessionID: "sess-L2", CWD: "/tmp/proj", Message: "big task"})
	assertStatus(t, db, "sess-L2", "active")

	callHook(handlePreCompact, ctx, hookInput{SessionID: "sess-L2", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L2", "compacting")

	callHook(handlePostCompact, ctx, hookInput{SessionID: "sess-L2", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L2", "active")

	callHook(handleStop, ctx, hookInput{SessionID: "sess-L2", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L2", "idle")
}

func TestLifecycle_WithPermission(t *testing.T) {
	ctx, db, _ := setupCtx(t)

	callHook(handleSessionStart, ctx, hookInput{SessionID: "sess-L3", CWD: "/tmp/proj"})
	callHook(handlePromptSubmit, ctx, hookInput{SessionID: "sess-L3", CWD: "/tmp/proj", Message: "deploy to prod"})
	assertStatus(t, db, "sess-L3", "active")

	var capturedNotifID string
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				capturedNotifID = n.ID
			}
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		callHook(handlePermission, ctx, hookInput{SessionID: "sess-L3", CWD: "/tmp/proj", ToolName: "Bash"})
	}()

	for capturedNotifID == "" {
		time.Sleep(5 * time.Millisecond)
	}
	assertStatus(t, db, "sess-L3", "waiting_permission")

	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "approved"}, "mobile")
	<-done
	assertStatus(t, db, "sess-L3", "active")

	callHook(handleStop, ctx, hookInput{SessionID: "sess-L3", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L3", "idle")

	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-L3", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L3", "terminated")
}

func TestLifecycle_ManagedSession_WithStopFailure(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	ctx.PaneMap.Set("sess-L4", "%5")
	ctx.RemovePendingPane = func(string) string { return "" }

	callHook(handleSessionStart, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj"})
	sess, _ := db.GetSession("sess-L4")
	if !sess.Managed {
		t.Fatal("expected managed=true")
	}

	callHook(handlePromptSubmit, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj", Message: "dangerous op"})
	assertStatus(t, db, "sess-L4", "active")

	callHook(handleStopFailure, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L4", "error")

	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L4", "terminated")
}
