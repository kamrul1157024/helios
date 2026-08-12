package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
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

// fakeBackend satisfies backend.Backend and records the calls hooks make.
type fakeBackend struct {
	handles map[string]string
	renames []string // "sessionID:name"
	kills   []string
	keys    []string // "sessionID:key"
	texts   []string // "sessionID:text"
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{handles: map[string]string{}}
}

// live registers a session as having a terminal.
func (f *fakeBackend) live(sessionID string) { f.handles[sessionID] = "sock-" + sessionID }

func (f *fakeBackend) Name() string    { return "fake" }
func (f *fakeBackend) Available() bool { return true }

func (f *fakeBackend) Start(sessionID, cwd string, argv []string) (string, error) {
	f.live(sessionID)
	return f.handles[sessionID], nil
}

func (f *fakeBackend) Adopt(sessionID, handle, cwd string) error {
	f.handles[sessionID] = handle
	return nil
}

func (f *fakeBackend) Handle(sessionID string) (string, bool) {
	h, ok := f.handles[sessionID]
	return h, ok
}

func (f *fakeBackend) Alive(sessionID string) bool {
	_, ok := f.handles[sessionID]
	return ok
}

func (f *fakeBackend) Forget(sessionID string) { delete(f.handles, sessionID) }

func (f *fakeBackend) Snapshot() map[string]string {
	out := map[string]string{}
	for k, v := range f.handles {
		out[k] = v
	}
	return out
}

func (f *fakeBackend) SendText(sessionID, text string) error {
	f.texts = append(f.texts, sessionID+":"+text)
	return nil
}

func (f *fakeBackend) SendKey(sessionID string, k backend.Key) error {
	f.keys = append(f.keys, sessionID+":"+string(k))
	return nil
}

func (f *fakeBackend) Interrupt(sessionID string) error { return nil }

func (f *fakeBackend) Kill(sessionID string) error {
	f.kills = append(f.kills, sessionID)
	f.Forget(sessionID)
	return nil
}

func (f *fakeBackend) Capture(sessionID string) (string, error) { return "", nil }

func (f *fakeBackend) Rename(sessionID, name string) error {
	f.renames = append(f.renames, sessionID+":"+name)
	return nil
}

func (f *fakeBackend) Sweep() []string { return nil }

func (f *fakeBackend) Status() backend.Status {
	return backend.Status{Name: "fake", Available: true}
}

// setupCtx builds a HookContext wired to an in-memory store.
// SSE events are collected into sseEvents.
func setupCtx(t *testing.T) (*provider.HookContext, *store.Store, *[]string) {
	t.Helper()
	db := openMemoryStore(t)
	mgr := notifications.NewManager(db)
	var sseEvents []string

	ctx := &provider.HookContext{
		DB:       db,
		Mgr:      mgr,
		Terminal: newFakeBackend(),
		Notify: func(eventType string, _ interface{}) {
			sseEvents = append(sseEvents, eventType)
		},
		Report:         func(provider.ReportEvent) {},
		SessionStarted: func(string) {},
	}
	return ctx, db, &sseEvents
}

// terminalOf returns the fake backend behind a context.
func terminalOf(ctx *provider.HookContext) *fakeBackend {
	return ctx.Terminal.(*fakeBackend)
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

// captureNotifIDs installs a Notify interceptor that publishes notification IDs
// on a channel. The hook handlers under test block until their notification is
// resolved, so they run on their own goroutine and the ID has to cross
// goroutines through a channel rather than a shared variable.
func captureNotifIDs(ctx *provider.HookContext) <-chan string {
	ids := make(chan string, 4)
	ctx.Notify = func(eventType string, data interface{}) {
		if eventType == "notification" {
			if n, ok := data.(*store.Notification); ok {
				select {
				case ids <- n.ID:
				default:
				}
			}
		}
	}
	return ids
}

// awaitNotifID blocks until the next notification ID is captured.
func awaitNotifID(t *testing.T, ids <-chan string) string {
	t.Helper()
	select {
	case id := <-ids:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for notification")
		return ""
	}
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

func TestSessionStart_ManagedTrue_WhenTerminalExists(t *testing.T) {
	ctx, db, sseEvents := setupCtx(t)
	terminalOf(ctx).live("sess-managed")

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-managed",
		CWD:       "/tmp/proj",
	})

	sess, _ := db.GetSession("sess-managed")
	if !sess.Managed {
		t.Error("managed = false, want true when the session has a terminal")
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

func TestSessionStart_StopsTrustWatch(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	var stopped []string
	ctx.SessionStarted = func(sessionID string) { stopped = append(stopped, sessionID) }
	terminalOf(ctx).live("sess-pending")

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-pending",
		CWD:       "/tmp/proj",
	})

	sess, _ := db.GetSession("sess-pending")
	if !sess.Managed {
		t.Error("managed = false, want true when the session has a terminal")
	}
	if len(stopped) != 1 || stopped[0] != "sess-pending" {
		t.Errorf("SessionStarted calls = %v, want [sess-pending]", stopped)
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

// The detail used to be sessionContext(), which showed the user their own last
// prompt back and said nothing about what broke.
func TestStopFailure_DetailIsTheErrorText(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	const errText = "API Error: Response stalled mid-stream. The response above may be incomplete."

	callHook(handleStopFailure, ctx, hookInput{
		SessionID:      "sess-1",
		CWD:            "/tmp/proj",
		TranscriptPath: writeTranscript(t, apiErrorLine(errText)),
	})

	notif := onlyErrorNotif(t, db)
	if notif.Detail == nil || *notif.Detail != errText {
		t.Errorf("detail = %v, want %q", notif.Detail, errText)
	}
	if notif.Title == nil || *notif.Title != "Session error" {
		t.Errorf("title = %v, want \"Session error\"", notif.Title)
	}
}

func TestStopFailure_FallsBackToSessionContext(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID:      "sess-1",
		CWD:            "/tmp/proj",
		TranscriptPath: writeTranscript(t, assistantLine("no error here")),
	})

	notif := onlyErrorNotif(t, db)
	if notif.Detail == nil || *notif.Detail == "" {
		t.Error("detail is empty, want the session context fallback")
	}
}

func TestStopFailure_ReasonBeatsTranscript(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID:      "sess-1",
		CWD:            "/tmp/proj",
		TranscriptPath: writeTranscript(t, apiErrorLine("stale transcript error")),
		Reason:         "reason from the CLI",
	})

	notif := onlyErrorNotif(t, db)
	if notif.Detail == nil || *notif.Detail != "reason from the CLI" {
		t.Errorf("detail = %v, want the CLI-supplied reason", notif.Detail)
	}
}

func TestStopFailure_RateLimitPayload(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID:      "sess-1",
		CWD:            "/tmp/proj",
		TranscriptPath: writeTranscript(t, apiErrorLine("Claude AI usage limit reached|1754899200")),
	})

	notif := onlyErrorNotif(t, db)
	if notif.Title == nil || *notif.Title != "Rate limit reached" {
		t.Errorf("title = %v, want \"Rate limit reached\"", notif.Title)
	}
	payload := decodePayload(t, notif)
	if payload["is_rate_limit"] != true {
		t.Errorf("is_rate_limit = %v, want true", payload["is_rate_limit"])
	}
	if payload["reset_at"] != "2025-08-11T08:00:00Z" {
		t.Errorf("reset_at = %v, want 2025-08-11T08:00:00Z", payload["reset_at"])
	}
	if payload["session_id"] != "sess-1" {
		t.Errorf("session_id = %v, want sess-1", payload["session_id"])
	}
	if payload["retryable"] != true {
		t.Errorf("retryable = %v, want true", payload["retryable"])
	}
}

func TestStopFailure_NoResetAtForATransientError(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID:      "sess-1",
		CWD:            "/tmp/proj",
		TranscriptPath: writeTranscript(t, apiErrorLine("API Error: Stream idle timeout - no chunks received")),
	})

	payload := decodePayload(t, onlyErrorNotif(t, db))
	if payload["is_rate_limit"] != false {
		t.Errorf("is_rate_limit = %v, want false", payload["is_rate_limit"])
	}
	if _, ok := payload["reset_at"]; ok {
		t.Errorf("reset_at = %v, want absent", payload["reset_at"])
	}
}

// Without a decision slot the notification can never be resolved, so it sits
// pending forever — TruncateNotifications skips pending rows.
func TestStopFailure_RegistersDecisionSlot(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStopFailure, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	notif := onlyErrorNotif(t, db)
	if !ctx.Mgr.HasPending(notif.ID) {
		t.Error("no decision slot registered; the notification can never resolve")
	}
}

// onlyErrorNotif returns the single claude.error notification in the store.
func onlyErrorNotif(t *testing.T, db *store.Store) *store.Notification {
	t.Helper()
	notifs, err := db.ListNotifications("claude", "", "claude.error")
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("want 1 claude.error notification, got %d", len(notifs))
	}
	return &notifs[0]
}

func decodePayload(t *testing.T, n *store.Notification) map[string]interface{} {
	t.Helper()
	if n.Payload == nil {
		t.Fatal("notification has no payload")
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(*n.Payload), &out); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return out
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

func TestSessionEnd_ForgetsTerminal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	terminalOf(ctx).live("sess-1")

	callHook(handleSessionEnd, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	if _, ok := ctx.Terminal.Handle("sess-1"); ok {
		t.Error("backend still maps the session after SessionEnd")
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

	notifIDs := captureNotifIDs(ctx)

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
	capturedNotifID := awaitNotifID(t, notifIDs)
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "approved"}, "mobile")
	<-done
}

func TestPermission_Approve_ResumesActive_AndReturnsAllow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	notifIDs := captureNotifIDs(ctx)

	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolName:  "Bash",
		})
	}()

	capturedNotifID := awaitNotifID(t, notifIDs)
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

	notifIDs := captureNotifIDs(ctx)

	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolName:  "Bash",
		})
	}()

	capturedNotifID := awaitNotifID(t, notifIDs)
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

	notifIDs := captureNotifIDs(ctx)

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
	capturedNotifID := awaitNotifID(t, notifIDs)
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

	notifIDs := captureNotifIDs(ctx)

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

	capturedNotifID := awaitNotifID(t, notifIDs)
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "answered"}, "mobile")
	<-done
}

func TestQuestion_Answer_ReturnsAllow(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	notifIDs := captureNotifIDs(ctx)

	toolInput, _ := json.Marshal(map[string]string{"question": "Proceed?"})
	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolInput: toolInput,
		})
	}()

	capturedNotifID := awaitNotifID(t, notifIDs)
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

	notifIDs := captureNotifIDs(ctx)

	toolInput, _ := json.Marshal(map[string]string{"question": "Proceed?"})
	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolInput: toolInput,
		})
	}()

	capturedNotifID := awaitNotifID(t, notifIDs)
	ctx.Mgr.Resolve(capturedNotifID, notifications.Decision{Status: "denied"}, "mobile")

	w := <-resultCh
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	output := resp["hookSpecificOutput"].(map[string]interface{})
	if output["permissionDecision"] != "deny" {
		t.Errorf("permissionDecision = %v, want deny", output["permissionDecision"])
	}
}

// ==================== Terminal relabelling ====================

func TestStop_RenamesTerminalToIdle(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	ft := terminalOf(ctx)
	ft.live("sess-1")
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	callHook(handleStop, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
	})

	if len(ft.renames) == 0 {
		t.Error("expected Rename call on Stop")
	}
}

func TestPromptSubmit_RenamesTerminalToActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	ft := terminalOf(ctx)
	ft.live("sess-1")
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handlePromptSubmit, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		Message:   "do something",
	})

	if len(ft.renames) == 0 {
		t.Error("expected Rename call on PromptSubmit")
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

	notifIDs := captureNotifIDs(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		callHook(handlePermission, ctx, hookInput{SessionID: "sess-L3", CWD: "/tmp/proj", ToolName: "Bash"})
	}()

	capturedNotifID := awaitNotifID(t, notifIDs)
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
	terminalOf(ctx).live("sess-L4")

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
