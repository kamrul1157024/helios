package daemon

import (
	"os"
	"testing"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
)

// ==================== Test infrastructure ====================

func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func strPtr(s string) *string { return &s }

// fakeBackend satisfies backend.Backend. live holds sessionID → handle for
// terminals that are still running; anything mapped but not live is swept.
type fakeBackend struct {
	handles map[string]string
	live    map[string]bool
}

func newFakeBackend(liveSessions ...string) *fakeBackend {
	f := &fakeBackend{handles: map[string]string{}, live: map[string]bool{}}
	for _, id := range liveSessions {
		f.handles[id] = "sock-" + id
		f.live[id] = true
	}
	return f
}

// track registers a session whose terminal has already died.
func (f *fakeBackend) trackDead(sessionID string) {
	f.handles[sessionID] = "sock-" + sessionID
	f.live[sessionID] = false
}

func (f *fakeBackend) Name() string     { return "fake" }
func (f *fakeBackend) Available() bool  { return true }
func (f *fakeBackend) Forget(id string) { delete(f.handles, id); delete(f.live, id) }
func (f *fakeBackend) Alive(id string) bool {
	return f.live[id]
}

func (f *fakeBackend) Start(sessionID, cwd, command string) (string, error) {
	f.handles[sessionID] = "sock-" + sessionID
	f.live[sessionID] = true
	return f.handles[sessionID], nil
}

func (f *fakeBackend) Adopt(sessionID, handle, cwd string) error {
	f.handles[sessionID] = handle
	f.live[sessionID] = true
	return nil
}

func (f *fakeBackend) Handle(sessionID string) (string, bool) {
	h, ok := f.handles[sessionID]
	return h, ok
}

func (f *fakeBackend) Snapshot() map[string]string {
	out := map[string]string{}
	for k, v := range f.handles {
		out[k] = v
	}
	return out
}

func (f *fakeBackend) SendText(sessionID, text string) error         { return nil }
func (f *fakeBackend) SendKey(sessionID string, k backend.Key) error { return nil }
func (f *fakeBackend) Interrupt(sessionID string) error              { return nil }
func (f *fakeBackend) Kill(sessionID string) error                   { f.Forget(sessionID); return nil }
func (f *fakeBackend) Capture(sessionID string) (string, error)      { return "", nil }
func (f *fakeBackend) Rename(sessionID, name string) error           { return nil }
func (f *fakeBackend) Status() backend.Status                        { return backend.Status{Name: "fake", Available: true} }

func (f *fakeBackend) Sweep() []string {
	var dead []string
	for id := range f.handles {
		if !f.live[id] {
			dead = append(dead, id)
		}
	}
	for _, id := range dead {
		f.Forget(id)
	}
	return dead
}

func seedSession(t *testing.T, db *store.Store, sessionID, cwd, status string, managed bool) {
	t.Helper()
	sess := &store.Session{
		SessionID: sessionID,
		Source:    "claude",
		CWD:       cwd,
		Status:    status,
		Managed:   managed,
		LastEvent: strPtr("seed"),
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

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

// ==================== reapStaleSessions ====================

// A dead terminal drops out of the backend, but the session survives it: cold
// is a resumable state, not the end of the conversation.
func TestReapStaleSessions_DeadTerminalGoesColdNotTerminated(t *testing.T) {
	db := setupTestStore(t)
	sse := server.NewSSEBroadcaster()
	be := newFakeBackend()
	be.trackDead("sess-dead")

	seedSession(t, db, "sess-dead", "/tmp/proj", "active", false)

	reapStaleSessions(db, be, sse)

	assertStatus(t, db, "sess-dead", "active")
	if _, ok := be.Handle("sess-dead"); ok {
		t.Error("dead session should have been dropped by the backend")
	}
}

// Nothing reaps a managed session that never left "starting": if its launch
// stalled, the user resumes it by hand.
func TestReapStaleSessions_LeavesStuckStartingAlone(t *testing.T) {
	db := setupTestStore(t)
	sse := server.NewSSEBroadcaster()
	be := newFakeBackend("sess-stuck")

	seedSession(t, db, "sess-stuck", "/tmp/proj", "starting", true)

	reapStaleSessions(db, be, sse)

	assertStatus(t, db, "sess-stuck", "starting")
}

func TestReapStaleSessions_KeepsLiveTerminal(t *testing.T) {
	db := setupTestStore(t)
	sse := server.NewSSEBroadcaster()
	be := newFakeBackend("sess-live")

	seedSession(t, db, "sess-live", "/tmp/proj", "active", false)

	reapStaleSessions(db, be, sse)

	assertStatus(t, db, "sess-live", "active")
	if _, ok := be.Handle("sess-live"); !ok {
		t.Error("live session should still be known to the backend")
	}
}

func TestReapStaleSessions_TranscriptBackfill(t *testing.T) {
	db := setupTestStore(t)
	sse := server.NewSSEBroadcaster()
	be := newFakeBackend("sess-bt")

	// Create a temp JSONL transcript with a user message.
	dir := t.TempDir()
	transcriptFile := dir + "/session.jsonl"
	line := `{"type":"user","timestamp":"2024-01-01T00:00:00Z","message":{"content":"fix the login bug"}}` + "\n"
	if err := os.WriteFile(transcriptFile, []byte(line), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	sess := &store.Session{
		SessionID:      "sess-bt",
		Source:         "claude",
		CWD:            "/tmp/proj",
		Status:         "idle",
		Managed:        false,
		LastEvent:      strPtr("Stop"),
		TranscriptPath: &transcriptFile,
	}
	if err := db.UpsertSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	reapStaleSessions(db, be, sse)

	got, _ := db.GetSession("sess-bt")
	if got.LastUserMessage == nil || *got.LastUserMessage != "fix the login bug" {
		t.Errorf("last_user_message = %v, want 'fix the login bug'", got.LastUserMessage)
	}
}

// ==================== claudeIsIdle ====================

func TestClaudeIsIdle_True_WhenOnlyPromptPresent(t *testing.T) {
	if !claudeIsIdle("some output\n❯ ") {
		t.Error("expected idle when only ❯ is present")
	}
}

func TestClaudeIsIdle_False_WhenGenerating(t *testing.T) {
	if claudeIsIdle("Reading file…\n❯ ") {
		t.Error("expected not idle when … is present")
	}
}

func TestClaudeIsIdle_False_WhenNoPrompt(t *testing.T) {
	if claudeIsIdle("some random output") {
		t.Error("expected not idle when ❯ is absent")
	}
}

func TestClaudeIsIdle_False_WhenEmpty(t *testing.T) {
	if claudeIsIdle("") {
		t.Error("expected not idle for empty string")
	}
}

// ==================== lastUserMessageFromTranscript ====================

func TestLastUserMessageFromTranscript_StringContent(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.jsonl"
	line := `{"type":"user","message":{"content":"fix auth"}}` + "\n"
	os.WriteFile(path, []byte(line), 0644)

	got := lastUserMessageFromTranscript(path)
	if got != "fix auth" {
		t.Errorf("got %q, want 'fix auth'", got)
	}
}

func TestLastUserMessageFromTranscript_ArrayContent(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.jsonl"
	line := `{"type":"user","message":{"content":[{"type":"text","text":"refactor login"}]}}` + "\n"
	os.WriteFile(path, []byte(line), 0644)

	got := lastUserMessageFromTranscript(path)
	if got != "refactor login" {
		t.Errorf("got %q, want 'refactor login'", got)
	}
}

func TestLastUserMessageFromTranscript_ReturnsLastUserMessage(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.jsonl"
	content := `{"type":"user","message":{"content":"first message"}}` + "\n" +
		`{"type":"assistant","message":{"content":"ok"}}` + "\n" +
		`{"type":"user","message":{"content":"second message"}}` + "\n"
	os.WriteFile(path, []byte(content), 0644)

	got := lastUserMessageFromTranscript(path)
	if got != "second message" {
		t.Errorf("got %q, want 'second message'", got)
	}
}

func TestLastUserMessageFromTranscript_MissingFile(t *testing.T) {
	got := lastUserMessageFromTranscript("/nonexistent/path.jsonl")
	if got != "" {
		t.Errorf("got %q, want empty for missing file", got)
	}
}

func TestLastUserMessageFromTranscript_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.jsonl"
	os.WriteFile(path, []byte(""), 0644)

	got := lastUserMessageFromTranscript(path)
	if got != "" {
		t.Errorf("got %q, want empty for empty file", got)
	}
}
