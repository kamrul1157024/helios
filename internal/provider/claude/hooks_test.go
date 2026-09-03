package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/terminal"
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
//
// The recording fields are mutex-guarded: question answering is serialised per
// session by the code under test, and the test that proves it drives two
// answers at once.
type fakeBackend struct {
	mu      sync.Mutex
	handles map[string]string
	renames []string // "sessionID:name"
	kills   []string
	keys    []string // "sessionID:key"
	texts   []string // "sessionID:text"
	evicted map[string]bool
	screen  string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{handles: map[string]string{}, evicted: map[string]bool{}}
}

// Evict and EvictedRecently mirror the host backend: a mark set before the kill
// and consumed by the first caller to ask.
func (f *fakeBackend) Evict(sessionID string) error {
	f.mu.Lock()
	f.evicted[sessionID] = true
	f.mu.Unlock()
	return f.Kill(sessionID)
}

func (f *fakeBackend) EvictedRecently(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	was := f.evicted[sessionID]
	delete(f.evicted, sessionID)
	return was
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, sessionID+":"+text)
	return nil
}

func (f *fakeBackend) SendKey(sessionID string, k backend.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, sessionID+":"+string(k))
	if k == backend.KeyUp || k == backend.KeyDown {
		f.screen = moveHighlight(f.screen, k)
	}
	return nil
}

// moveHighlight walks the ❯ marker one row, so a fake screen answers arrows the
// way a real dialog does. A picker that reads the screen between keystrokes
// arrows forever without it.
func moveHighlight(screen string, k backend.Key) string {
	lines := strings.Split(screen, "\n")
	at := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			at = i
			break
		}
	}
	if at < 0 {
		return screen
	}
	to := at + 1
	if k == backend.KeyUp {
		to = at - 1
	}
	if to < 0 || to >= len(lines) || strings.TrimSpace(lines[to]) == "" {
		return screen
	}
	lines[at] = strings.Replace(lines[at], "❯ ", "  ", 1)
	lines[to] = "❯ " + strings.TrimLeft(lines[to], " ")
	return strings.Join(lines, "\n")
}

// sentKeys returns a copy of the recorded keystrokes.
func (f *fakeBackend) sentKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...)
}

func (f *fakeBackend) Interrupt(sessionID string) error { return nil }

func (f *fakeBackend) Kill(sessionID string) error {
	f.kills = append(f.kills, sessionID)
	f.Forget(sessionID)
	return nil
}

// Capture reports whatever the test put on the session's screen. An approved
// plan is answered on the CLI's own dialog, which means reading it first.
func (f *fakeBackend) Capture(sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screen, nil
}

func (f *fakeBackend) setScreen(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screen = text
}

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

	notify := func(eventType string, _ interface{}) {
		sseEvents = append(sseEvents, eventType)
	}
	// Mirrors server.NewShared: the manager announces its own resolutions, so
	// tests see the same events clients would.
	mgr.SetBroadcaster(notify)

	ctx := &provider.HookContext{
		DB:             db,
		Mgr:            mgr,
		Terminal:       newFakeBackend(),
		Notify:         notify,
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

func TestSessionStart_EmitsStatusEvent_WhenTerminalExists(t *testing.T) {
	ctx, _, sseEvents := setupCtx(t)
	terminalOf(ctx).live("sess-with-terminal")

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-with-terminal",
		CWD:       "/tmp/proj",
	})

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
	ctx, _, _ := setupCtx(t)
	var stopped []string
	ctx.SessionStarted = func(sessionID string) { stopped = append(stopped, sessionID) }
	terminalOf(ctx).live("sess-pending")

	callHook(handleSessionStart, ctx, hookInput{
		SessionID: "sess-pending",
		CWD:       "/tmp/proj",
	})

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

// A session helios stopped to reclaim memory has not ended. Killing the host
// kills the agent, which runs this very hook on the way down — so without the
// eviction mark every cold session was filed as terminated, the archival state
// a person chooses. That is the bug this pins.
func TestSessionEnd_LeavesAnEvictedSessionAlone(t *testing.T) {
	ctx, db, sseEvents := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")
	be := terminalOf(ctx)
	be.live("sess-1")

	if err := be.Evict("sess-1"); err != nil {
		t.Fatalf("evict: %v", err)
	}
	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-1", CWD: "/tmp/proj"})

	assertStatus(t, db, "sess-1", "idle")
	sess, _ := db.GetSession("sess-1")
	if sess.EndedAt != nil && *sess.EndedAt != "" {
		t.Errorf("ended_at = %q; a cold session has not ended", *sess.EndedAt)
	}
	for _, e := range *sseEvents {
		if e == "session_status" {
			t.Error("broadcast a status change for a session that only went cold")
		}
	}
}

// The mark answers one exit. A session evicted, woken, and later quit by the
// user must still end properly.
func TestSessionEnd_TerminatesNormallyAfterAnEviction(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")
	be := terminalOf(ctx)
	be.live("sess-1")

	if err := be.Evict("sess-1"); err != nil {
		t.Fatalf("evict: %v", err)
	}
	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-1", CWD: "/tmp/proj"})

	// Woken again, then really quit.
	be.live("sess-1")
	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-1", CWD: "/tmp/proj"})

	assertStatus(t, db, "sess-1", "terminated")
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

// questionInput builds an AskUserQuestion tool input in the shape Claude
// sends: {"questions": [...]}.
func questionInput(t *testing.T, questions ...map[string]interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{"questions": questions})
	if err != nil {
		t.Fatalf("marshal tool input: %v", err)
	}
	return raw
}

func oneQuestion(question, header string, options ...string) map[string]interface{} {
	opts := make([]interface{}, 0, len(options))
	for _, o := range options {
		opts = append(opts, map[string]string{"label": o, "description": o})
	}
	return map[string]interface{}{"question": question, "header": header, "options": opts}
}

func notifsOfType(t *testing.T, db *store.Store, nType string) []store.Notification {
	t.Helper()
	notifs, err := db.ListNotifications("claude", "", nType)
	if err != nil {
		t.Fatalf("list %s notifications: %v", nType, err)
	}
	return notifs
}

func onlyQuestionNotif(t *testing.T, db *store.Store) *store.Notification {
	t.Helper()
	notifs := notifsOfType(t, db, "claude.question")
	if len(notifs) != 1 {
		t.Fatalf("want 1 claude.question notification, got %d", len(notifs))
	}
	return &notifs[0]
}

// startQuestion fires the question hook on its own goroutine and waits for its
// notification, which is the point from which any surface can answer.
func startQuestion(t *testing.T, ctx *provider.HookContext,
	input hookInput) (<-chan *httptest.ResponseRecorder, string) {
	t.Helper()
	ids := captureNotifIDs(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callHook(handleQuestion, ctx, input) }()
	return done, awaitNotifID(t, ids)
}

// questionOutput is handleQuestion's reply to the CLI.
type questionOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

func questionAnswerOf(t *testing.T, w *httptest.ResponseRecorder) questionOutput {
	t.Helper()
	var resp struct {
		HookSpecificOutput questionOutput `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp.HookSpecificOutput
}

// The hook blocks again: the answer travels back in its response, so returning
// early would hand Claude nothing and let it render a UI helios cannot answer.
func TestQuestion_BlocksUntilSomeoneAnswers(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	done, notifID := startQuestion(t, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
	})

	select {
	case <-done:
		t.Fatal("handleQuestion returned before anyone answered")
	case <-time.After(200 * time.Millisecond):
	}
	assertStatus(t, db, "sess-1", "waiting_permission")
	// Without a reserved slot an immediate answer arrives with nobody waiting.
	if !ctx.Mgr.HasPending(notifID) {
		t.Error("question reserved no decision slot")
	}

	ctx.Mgr.Resolve(notifID, notifications.Decision{
		Status:   "answered",
		Response: json.RawMessage(`{"selections":[{"question_index":0,"option_index":1}]}`),
	}, "mobile")

	out := questionAnswerOf(t, awaitResponse(t, done))
	if out.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse", out.HookEventName)
	}
	// PreToolUse has no "here is the tool's result", so the answer rides back on
	// a deny. See docs/specs/36-helios-owned-hitl.md.
	if out.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", out.PermissionDecision)
	}
	if !strings.Contains(out.PermissionDecisionReason, `"No"`) {
		t.Errorf("reason = %q, want the chosen option in it", out.PermissionDecisionReason)
	}
	assertStatus(t, db, "sess-1", "active")
}

// Nobody answered before the hook's budget ran out. Claude still has to be told
// something, or the turn ends on a bare denial.
func TestQuestion_UnansweredSaysSo(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	done, notifID := startQuestion(t, ctx, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj",
		ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
	})
	// What waitForDecision does when its timer fires.
	ctx.Mgr.Resolve(notifID, notifications.Decision{Status: "denied"}, "mobile")

	out := questionAnswerOf(t, awaitResponse(t, done))
	if out.PermissionDecisionReason != unansweredReason {
		t.Errorf("reason = %q, want the unanswered wording", out.PermissionDecisionReason)
	}
}

// The whole point of the overlay: the person at the terminal answers, one
// question at a time, and the phone's card goes away.
func TestQuestion_AnsweredFromTheTerminal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	ids := captureNotifIDs(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1", CWD: "/tmp/proj",
			ToolInput: questionInput(t,
				oneQuestion("Which scope?", "Banner scope", "Every host", "Only the active host"),
				oneQuestion("How should it wake?", "Wake strategy", "Poll", "Heartbeat watchdog")),
		})
	}()
	notifID := awaitNotifID(t, ids)

	first := awaitOverlay(t, overlays)
	if !strings.Contains(first.Title, "Banner scope") || !strings.Contains(first.Title, "1/2") {
		t.Errorf("title = %q, want the first question's header and its place in the set", first.Title)
	}
	hitlCtl.HandleInput("sess-1", []byte("2\r"))

	second := awaitOverlay(t, overlays)
	for second.Title == first.Title {
		second = awaitOverlay(t, overlays) // the highlight redraw for question one
	}
	if !strings.Contains(second.Title, "Wake strategy") {
		t.Errorf("title = %q, want the second question", second.Title)
	}
	hitlCtl.HandleInput("sess-1", []byte("2\r"))

	reason := questionAnswerOf(t, awaitResponse(t, done)).PermissionDecisionReason
	for _, want := range []string{"Only the active host", "Heartbeat watchdog"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason = %q, want %q in it", reason, want)
		}
	}

	got, err := db.GetNotification(notifID)
	if err != nil || got == nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got.ResolvedSource == nil || *got.ResolvedSource != "terminal" {
		t.Errorf("resolved source = %v, want terminal", got.ResolvedSource)
	}
	awaitCleared(t, overlays)
}

// Escape out of a question and Claude is told it went unanswered, rather than
// being left to wait out the hook's budget.
func TestQuestion_SkippedFromTheTerminal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- callHook(handleQuestion, ctx, hookInput{
			SessionID: "sess-1", CWD: "/tmp/proj",
			ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
		})
	}()
	awaitOverlay(t, overlays)

	hitlCtl.HandleInput("sess-1", []byte("\x1b"))

	if got := questionAnswerOf(t, awaitResponse(t, done)).PermissionDecisionReason; got != skippedReason {
		t.Errorf("reason = %q, want the skipped wording", got)
	}
}

// A free-text question has no options to list, so the overlay stays away and
// the phone keeps it. Painting a choice-less box would block the terminal on a
// prompt it cannot answer.
// A question with no options wants free text. The overlay opens the field for
// it rather than sending the whole set to the phone, which is what it did
// before the field existed.
func TestQuestion_FreeTextOpensTheFieldInTheTerminal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, _ := withTerminal(ctx)

	done, notifID := startQuestion(t, ctx, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj",
		ToolInput: questionInput(t, oneQuestion("What should it be called?", "Name")),
	})

	o := awaitOverlay(t, overlays)
	if o.Input == nil || !o.Input.Active {
		t.Fatalf("painted %+v, want an open answer field", o)
	}

	ctx.Mgr.Resolve(notifID, notifications.Decision{
		Status:   "answered",
		Response: json.RawMessage(`{"text":"helios"}`),
	}, "mobile")
	if got := questionAnswerOf(t, awaitResponse(t, done)).PermissionDecisionReason; !strings.Contains(got, "helios") {
		t.Errorf("reason = %q, want the typed answer in it", got)
	}
}

// AskUserQuestion trips both the PermissionRequest hook and the PreToolUse
// question hook. Raising a card from each put two approvals on the phone for
// one question — and since the permission hook blocks the tool, the CLI never
// rendered the question UI that answering the other card is supposed to drive.
func TestPermission_AskUserQuestionRaisesNoSecondApproval(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1",
			CWD:       "/tmp/proj",
			ToolName:  askUserQuestionTool,
			ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
		})
	}()

	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handlePermission blocked on AskUserQuestion; the CLI can never render its question UI")
	}

	if notifs := notifsOfType(t, db, "claude.permission"); len(notifs) != 0 {
		t.Fatalf("AskUserQuestion raised %d permission notifications, want 0 — "+
			"claude.question is the only surface for a question", len(notifs))
	}

	// "ask", not "allow": allow skips the interactive prompt, and for this tool
	// that prompt is the question picker, so the tool returns no answers at all.
	var resp permResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	if got := resp.HookSpecificOutput.Decision.Behavior; got != "ask" {
		t.Errorf("decision behavior = %q, want ask — allow would skip the picker "+
			"and answer the question with nothing", got)
	}
	if got := resp.HookSpecificOutput.HookEventName; got != "PermissionRequest" {
		t.Errorf("hookEventName = %q, want PermissionRequest", got)
	}
}

// The bypass is scoped to AskUserQuestion; every other tool still asks.
func TestPermission_OtherToolsStillRaiseAnApproval(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	notifIDs := captureNotifIDs(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		})
	}()

	notifID := awaitNotifID(t, notifIDs)
	if notifs := notifsOfType(t, db, "claude.permission"); len(notifs) != 1 {
		t.Fatalf("Bash raised %d permission notifications, want 1", len(notifs))
	}
	ctx.Mgr.Resolve(notifID, notifications.Decision{Status: "approved"}, "mobile")
	<-done
}

// Wrapping rather than splicing would produce {"questions":{"questions":[...]}}
// and the mobile card's payload['questions'] accessor would cast a Map to a
// List, silently emptying the card.
func TestQuestion_PayloadKeepsQuestionsAtTopLevel(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	done, notifID := startQuestion(t, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
	})
	defer func() {
		ctx.Mgr.Resolve(notifID, notifications.Decision{Status: "denied"}, "mobile")
		awaitResponse(t, done)
	}()

	payload := decodePayload(t, onlyQuestionNotif(t, db))
	questions, ok := payload["questions"].([]interface{})
	if !ok {
		t.Fatalf("payload[questions] = %T, want a JSON array", payload["questions"])
	}
	if len(questions) != 1 {
		t.Fatalf("questions length = %d, want 1", len(questions))
	}
	if payload["session_id"] != "sess-1" {
		t.Errorf("payload[session_id] = %v, want sess-1", payload["session_id"])
	}
}

// Both PreToolUse matchers fire for AskUserQuestion. Writing "active" from the
// wildcard one is what used to erase waiting_permission a millisecond after
// handleQuestion set it.
func TestToolPre_LeavesQuestionStatusAlone(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "waiting_permission")

	callHook(handleToolPre, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "AskUserQuestion",
	})

	assertStatus(t, db, "sess-1", "waiting_permission")
}

func TestToolPre_StillMarksOtherToolsActive(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "idle")

	callHook(handleToolPre, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Read",
	})

	assertStatus(t, db, "sess-1", "active")
}

// The backstop for the escape path: the tool completed, so the question is gone
// and every surface has to stop offering it.
func TestToolPost_ResolvesQuestionButNotPermission(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	done, _ := startQuestion(t, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
	})
	perm := &store.Notification{
		ID:            "notif-perm",
		Source:        "claude",
		SourceSession: "sess-1",
		CWD:           "/tmp/proj",
		Type:          "claude.permission",
		Status:        "pending",
	}
	if err := db.CreateNotification(perm); err != nil {
		t.Fatalf("create permission notification: %v", err)
	}

	callHook(handleToolPost, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "AskUserQuestion",
	})
	awaitResponse(t, done) // resolving the question releases the blocked hook

	if got := onlyQuestionNotif(t, db).Status; got != "resolved" {
		t.Errorf("question status = %q, want resolved", got)
	}
	stored, err := db.GetNotification("notif-perm")
	if err != nil {
		t.Fatalf("get permission notification: %v", err)
	}
	if stored.Status != "pending" {
		t.Errorf("permission status = %q, want pending", stored.Status)
	}
}

// Answering in the CLI tells the daemon nothing. The tool running afterwards
// does: the permission card must not outlive the tool call it was asking
// about, or the phone keeps offering to approve work already done.
func TestToolPost_ResolvesPermissionForTheSameTool(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	seedPermission(t, db, "notif-perm", "sess-1", "Bash")

	callHook(handleToolPost, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Bash",
	})

	assertNotificationStatus(t, db, "notif-perm", "resolved")
}

// A turn can have several tool calls in flight, so a finished one must not
// retract the card for a different tool still waiting on an answer.
func TestToolPost_LeavesOtherToolsPermissionPending(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	seedPermission(t, db, "notif-perm", "sess-1", "Write")

	callHook(handleToolPost, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Bash",
	})

	assertNotificationStatus(t, db, "notif-perm", "pending")
}

// Another session's card is another session's business.
func TestToolPost_LeavesOtherSessionsPermissionPending(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	seedSession(t, db, "sess-2", "/tmp/other", "active")
	seedPermission(t, db, "notif-perm", "sess-2", "Bash")

	callHook(handleToolPost, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Bash",
	})

	assertNotificationStatus(t, db, "notif-perm", "pending")
}

// A tool that ran and failed was still permitted by somebody.
func TestToolPostFailure_ResolvesPermissionForTheSameTool(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	seedPermission(t, db, "notif-perm", "sess-1", "Bash")

	callHook(handleToolPostFailure, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolName:  "Bash",
	})

	assertNotificationStatus(t, db, "notif-perm", "resolved")
}

func seedPermission(t *testing.T, db *store.Store, id, sessionID, toolName string) {
	t.Helper()
	payload := fmt.Sprintf(`{"tool_name":%q,"tool_input":{}}`, toolName)
	notif := &store.Notification{
		ID:            id,
		Source:        "claude",
		SourceSession: sessionID,
		CWD:           "/tmp/proj",
		Type:          "claude.permission",
		Status:        "pending",
		Title:         &toolName,
		Payload:       &payload,
	}
	if err := db.CreateNotification(notif); err != nil {
		t.Fatalf("create permission notification: %v", err)
	}
}

func assertNotificationStatus(t *testing.T, db *store.Store, id, want string) {
	t.Helper()
	stored, err := db.GetNotification(id)
	if err != nil {
		t.Fatalf("get notification %s: %v", id, err)
	}
	if stored.Status != want {
		t.Errorf("notification %s status = %q, want %q", id, stored.Status, want)
	}
}

// Escaping out of a question returns Claude to its prompt without a
// PostToolUse, so idle_prompt is the only signal the question is gone.
func TestIdlePrompt_ResolvesPendingQuestion(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")

	done, _ := startQuestion(t, ctx, hookInput{
		SessionID: "sess-1",
		CWD:       "/tmp/proj",
		ToolInput: questionInput(t, oneQuestion("Proceed?", "Plan", "Yes", "No")),
	})

	callHook(handleNotification, ctx, hookInput{
		SessionID:     "sess-1",
		CWD:           "/tmp/proj",
		HookEventName: "idle_prompt",
	})
	awaitResponse(t, done)

	if got := onlyQuestionNotif(t, db).Status; got != "resolved" {
		t.Errorf("question status = %q, want resolved", got)
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

func TestLifecycle_TerminalSession_WithStopFailure(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	terminalOf(ctx).live("sess-L4")

	callHook(handleSessionStart, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj"})

	callHook(handlePromptSubmit, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj", Message: "dangerous op"})
	assertStatus(t, db, "sess-L4", "active")

	callHook(handleStopFailure, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L4", "error")

	callHook(handleSessionEnd, ctx, hookInput{SessionID: "sess-L4", CWD: "/tmp/proj"})
	assertStatus(t, db, "sess-L4", "terminated")
}

// ==================== Approving from the terminal ====================

// paintedOverlays stands in for a session's terminal, recording what helios
// drew over it. Painting happens on the hook's goroutine while the test drives
// keys from its own, so both directions travel by channel.
type paintedOverlays struct {
	painted chan terminal.Overlay
	cleared chan string
}

func (p *paintedOverlays) SetOverlay(sessionID string, o terminal.Overlay) error {
	p.painted <- o
	return nil
}

func (p *paintedOverlays) ClearOverlay(sessionID string) error {
	p.cleared <- sessionID
	return nil
}

// OverlayProtocol reports a host of this build, so a prompt that needs
// checkboxes or the answer field is painted rather than left to the phone.
func (p *paintedOverlays) OverlayProtocol(sessionID string) int {
	return terminal.HostProtocol
}

// withTerminal gives a context a terminal that can be painted on, as a live
// session has, and returns what lands on it.
func withTerminal(ctx *provider.HookContext) (*paintedOverlays, *hitl.Controller) {
	p := &paintedOverlays{
		painted: make(chan terminal.Overlay, 8),
		cleared: make(chan string, 8),
	}
	ctx.HITL = hitl.NewController(p)
	return p, ctx.HITL
}

func awaitOverlay(t *testing.T, p *paintedOverlays) terminal.Overlay {
	t.Helper()
	select {
	case o := <-p.painted:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was painted on the terminal")
		return terminal.Overlay{}
	}
}

func awaitCleared(t *testing.T, p *paintedOverlays) {
	t.Helper()
	select {
	case <-p.cleared:
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt was never taken down")
	}
}

// askPermission fires the hook on its own goroutine and returns the response
// channel plus the overlay it painted.
func askPermission(t *testing.T, ctx *provider.HookContext, p *paintedOverlays,
	input hookInput) (<-chan *httptest.ResponseRecorder, terminal.Overlay) {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callHook(handlePermission, ctx, input) }()
	return done, awaitOverlay(t, p)
}

func permDecision(t *testing.T, w *httptest.ResponseRecorder) permResponse {
	t.Helper()
	var resp permResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp
}

func awaitResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("the hook never answered the CLI")
		return nil
	}
}

// The suggestion the CLI offers when it would write a rule; the shape matters
// only in that it comes back verbatim in updatedPermissions.
var bashSuggestion = json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash"}]}]`)

func TestPermission_PaintsTheApprovalOnTheTerminal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, _ := withTerminal(ctx)
	notifIDs := captureNotifIDs(ctx)

	done, o := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"rm -rf build/"}`),
	})

	if o.Title != "Bash" {
		t.Errorf("title = %q, want the tool name", o.Title)
	}
	if len(o.Body) == 0 || !strings.Contains(o.Body[0], "rm -rf build/") {
		t.Errorf("body = %v, want the command being approved", o.Body)
	}
	// Without a suggestion there is no rule to write, so there is nothing for
	// "don't ask again" to mean.
	want := []string{allowOnce, denyChoice}
	if len(o.Options) != len(want) || o.Options[0] != want[0] || o.Options[1] != want[1] {
		t.Errorf("options = %v, want %v", o.Options, want)
	}

	ctx.Mgr.Resolve(awaitNotifID(t, notifIDs), notifications.Decision{Status: "approved"}, "mobile")
	awaitResponse(t, done)
}

func TestPermission_OffersDontAskAgainOnlyWithASuggestion(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, _ := withTerminal(ctx)
	notifIDs := captureNotifIDs(ctx)

	done, o := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		ToolInput:             json.RawMessage(`{"command":"ls"}`),
		PermissionSuggestions: bashSuggestion,
	})

	want := []string{allowOnce, allowAlways, denyChoice}
	if len(o.Options) != len(want) {
		t.Fatalf("options = %v, want %v", o.Options, want)
	}
	for i := range want {
		if o.Options[i] != want[i] {
			t.Errorf("option %d = %q, want %q", i, o.Options[i], want[i])
		}
	}

	ctx.Mgr.Resolve(awaitNotifID(t, notifIDs), notifications.Decision{Status: "approved"}, "mobile")
	awaitResponse(t, done)
}

// The hole this closes: before the overlay, a person sitting at the terminal
// could not approve anything, because the blocking hook left the CLI no UI.
func TestPermission_TerminalApproves(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)
	notifIDs := captureNotifIDs(ctx)

	done, _ := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
	})
	notifID := awaitNotifID(t, notifIDs)

	hitlCtl.HandleInput("sess-1", []byte("\r")) // "Allow once" is preselected

	w := awaitResponse(t, done)
	if got := permDecision(t, w).HookSpecificOutput.Decision.Behavior; got != "allow" {
		t.Errorf("behavior = %q, want allow", got)
	}
	assertStatus(t, db, "sess-1", "active")

	// One notification, resolved once, and the record says who did it.
	got, err := db.GetNotification(notifID)
	if err != nil || got == nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("notification status = %q, want approved", got.Status)
	}
	if got.ResolvedSource == nil || *got.ResolvedSource != "terminal" {
		t.Errorf("resolved source = %v, want terminal", got.ResolvedSource)
	}
	awaitCleared(t, overlays)
}

func TestPermission_TerminalDenies(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, o := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
	})

	// Walk down to "Deny" and confirm, the way a person would.
	hitlCtl.HandleInput("sess-1", []byte("\x1b[B"))
	if moved := awaitOverlay(t, overlays); moved.Selected != len(o.Options)-1 {
		t.Fatalf("selected = %d, want the last option", moved.Selected)
	}
	hitlCtl.HandleInput("sess-1", []byte("\r"))

	w := awaitResponse(t, done)
	if got := permDecision(t, w).HookSpecificOutput.Decision.Behavior; got != "deny" {
		t.Errorf("behavior = %q, want deny", got)
	}
	awaitCleared(t, overlays)
}

// Escape is a deny, not a hang: the tool call has to be answered either way.
func TestPermission_EscapeDenies(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, _ := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
	})
	hitlCtl.HandleInput("sess-1", []byte("\x1b"))

	w := awaitResponse(t, done)
	if got := permDecision(t, w).HookSpecificOutput.Decision.Behavior; got != "deny" {
		t.Errorf("behavior = %q, want deny", got)
	}
}

func TestPermission_TerminalAllowAlwaysAppliesTheSuggestion(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, _ := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		ToolInput:             json.RawMessage(`{"command":"ls"}`),
		PermissionSuggestions: bashSuggestion,
	})

	hitlCtl.HandleInput("sess-1", []byte("2\r")) // "Allow, and don't ask again"

	decision := permDecision(t, awaitResponse(t, done)).HookSpecificOutput.Decision
	if decision.Behavior != "allow" {
		t.Fatalf("behavior = %q, want allow", decision.Behavior)
	}
	// The rule the CLI offered has to come back, or "don't ask again" asks again.
	if !strings.Contains(string(decision.UpdatedPermissions), `"addRules"`) {
		t.Errorf("updatedPermissions = %s, want the suggested rule", decision.UpdatedPermissions)
	}
}

// Whichever surface answers, the other one stops showing the question.
func TestPermission_ThePhonesAnswerTakesThePromptDown(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)
	notifIDs := captureNotifIDs(ctx)

	done, _ := askPermission(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
	})
	notifID := awaitNotifID(t, notifIDs)

	ctx.Mgr.Resolve(notifID, notifications.Decision{Status: "approved"}, "mobile")
	awaitResponse(t, done)
	awaitCleared(t, overlays)

	// A keystroke that lands after the phone won belongs to the agent again.
	hitlCtl.HandleInput("sess-1", []byte("\r"))
	got, _ := db.GetNotification(notifID)
	if got.ResolvedSource == nil || *got.ResolvedSource != "mobile" {
		t.Errorf("resolved source = %v, want the phone that answered first", got.ResolvedSource)
	}
}

// A cold session has no terminal to paint on. That used to be the only case,
// and it still has to work.
func TestPermission_WithoutATerminalTheHookStillWaitsForThePhone(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	ctx.HITL = hitl.NewController(nil)
	notifIDs := captureNotifIDs(ctx)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		})
	}()

	ctx.Mgr.Resolve(awaitNotifID(t, notifIDs), notifications.Decision{Status: "approved"}, "mobile")
	w := awaitResponse(t, done)
	if got := permDecision(t, w).HookSpecificOutput.Decision.Behavior; got != "allow" {
		t.Errorf("behavior = %q, want allow", got)
	}
}

// Hooks run with whatever context the daemon built; a provider that never wired
// HITL must not take the permission path down with it.
func TestPermission_NoHITLControllerIsNotFatal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	ctx.HITL = nil
	notifIDs := captureNotifIDs(ctx)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- callHook(handlePermission, ctx, hookInput{
			SessionID: "sess-1", CWD: "/tmp/proj", ToolName: "Bash",
		})
	}()

	ctx.Mgr.Resolve(awaitNotifID(t, notifIDs), notifications.Decision{Status: "approved"}, "mobile")
	awaitResponse(t, done)
}

// ==================== Answering an elicitation from the terminal ====================

// elicitResponse is handleElicitation's reply to the CLI. The handler declares
// it inline, so the test declares its own view of the same wire shape.
type elicitResponse struct {
	HookSpecificOutput struct {
		HookEventName string                 `json:"hookEventName"`
		Action        string                 `json:"action"`
		Content       map[string]interface{} `json:"content,omitempty"`
	} `json:"hookSpecificOutput"`
}

func askElicitation(t *testing.T, ctx *provider.HookContext, p *paintedOverlays,
	input hookInput) (<-chan *httptest.ResponseRecorder, terminal.Overlay) {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callHook(handleElicitation, ctx, input) }()
	return done, awaitOverlay(t, p)
}

func elicitDecision(t *testing.T, w *httptest.ResponseRecorder) elicitResponse {
	t.Helper()
	var resp elicitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp
}

// A url elicitation carries no content, so the terminal can answer it in full.
func TestElicitation_URLModeIsAnswerableFromTheTerminal(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, o := askElicitation(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj",
		MCPServerName: "linear", Mode: "url",
		Message: "Authorise helios", URL: "https://linear.app/oauth/abc",
	})

	if !strings.Contains(o.Title, "linear") {
		t.Errorf("title = %q, want the server that asked", o.Title)
	}
	// The address has to be on the screen or there is nothing to act on.
	if !strings.Contains(strings.Join(o.Body, "\n"), "https://linear.app/oauth/abc") {
		t.Errorf("body = %v, want the url", o.Body)
	}
	want := []string{elicitContinue, elicitDecline}
	if len(o.Options) != len(want) || o.Options[0] != want[0] || o.Options[1] != want[1] {
		t.Fatalf("options = %v, want %v", o.Options, want)
	}

	hitlCtl.HandleInput("sess-1", []byte("\r"))

	if got := elicitDecision(t, awaitResponse(t, done)).HookSpecificOutput.Action; got != "accept" {
		t.Errorf("action = %q, want accept", got)
	}
	awaitCleared(t, overlays)
}

// A form's schema is arbitrary and an overlay is a list of choices, so the
// terminal gets the one answer it can give. Offering "Continue" here would
// accept with no content and break the server that asked.
func TestElicitation_FormModeOnlyOffersDecline(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, hitlCtl := withTerminal(ctx)

	done, o := askElicitation(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj",
		MCPServerName: "linear", Mode: "form",
		Message:         "Which team?",
		RequestedSchema: json.RawMessage(`{"type":"object","properties":{"team":{"type":"string"}}}`),
	})

	if len(o.Options) != 1 || o.Options[0] != elicitDecline {
		t.Fatalf("options = %v, want only %q", o.Options, elicitDecline)
	}
	if !strings.Contains(strings.Join(o.Body, "\n"), formHint) {
		t.Errorf("body = %v, want it to say where the form can be answered", o.Body)
	}

	hitlCtl.HandleInput("sess-1", []byte("\r"))

	if got := elicitDecision(t, awaitResponse(t, done)).HookSpecificOutput.Action; got != "decline" {
		t.Errorf("action = %q, want decline", got)
	}
}

// The overlay is an addition, not a replacement: the phone still answers the
// form, and doing so takes the terminal's copy down.
func TestElicitation_ThePhoneStillFillsInTheForm(t *testing.T) {
	ctx, db, _ := setupCtx(t)
	seedSession(t, db, "sess-1", "/tmp/proj", "active")
	overlays, _ := withTerminal(ctx)
	notifIDs := captureNotifIDs(ctx)

	done, _ := askElicitation(t, ctx, overlays, hookInput{
		SessionID: "sess-1", CWD: "/tmp/proj",
		MCPServerName: "linear", Mode: "form", Message: "Which team?",
	})

	ctx.Mgr.Resolve(awaitNotifID(t, notifIDs), notifications.Decision{
		Status:   "answered",
		Response: json.RawMessage(`{"action":"accept","content":{"team":"infra"}}`),
	}, "mobile")

	out := elicitDecision(t, awaitResponse(t, done)).HookSpecificOutput
	if out.Action != "accept" {
		t.Errorf("action = %q, want accept", out.Action)
	}
	if out.Content["team"] != "infra" {
		t.Errorf("content = %v, want the answered form", out.Content)
	}
	awaitCleared(t, overlays)
	assertStatus(t, db, "sess-1", "waiting_permission")
}

// The hook is a hint that now is a good time to look, not a description of
// what happened. It must not depend on the tool name or on the tool input:
// working out which tools write, per provider, is the coupling the daemon's
// digest exists to avoid. See docs/specs/54-file-change-events.md.
func TestToolPostPokesTheFileWatcher(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*provider.HookContext, http.ResponseWriter, *http.Request, json.RawMessage)
		tool    string
		input   string
	}{
		{"write", handleToolPost, "Write", `{"file_path":"/tmp/a.txt"}`},
		{"bash", handleToolPost, "Bash", `{"command":"sed -i s/a/b/ x"}`},
		{"no tool input", handleToolPost, "Bash", ""},
		// A command that failed at step nine still wrote at steps one to eight.
		{"failure", handleToolPostFailure, "Bash", `{"command":"make"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, _ := setupCtx(t)
			pokes := 0
			ctx.FilesTouched = func() { pokes++ }

			in := hookInput{SessionID: "s1", ToolName: tc.tool}
			if tc.input != "" {
				in.ToolInput = json.RawMessage(tc.input)
			}
			callHook(tc.handler, ctx, in)

			if pokes != 1 {
				t.Errorf("poked %d times, want 1", pokes)
			}
		})
	}
}

// A provider built without a watcher — every context in these tests before
// this change — must not panic.
func TestToolPostWithoutAWatcher(t *testing.T) {
	ctx, _, _ := setupCtx(t)
	ctx.FilesTouched = nil
	callHook(handleToolPost, ctx, hookInput{SessionID: "s1", ToolName: "Write"})
}
