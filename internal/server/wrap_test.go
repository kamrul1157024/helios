package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// stubBackend records what the server asks of a backend without running any
// terminals. Only adoption and handle lookup matter here.
type stubBackend struct {
	handles      map[string]string
	adoptErr     error
	adoptedAt    []string
	interruptErr error
	interrupted  []string
}

func newStubBackend() *stubBackend {
	return &stubBackend{handles: map[string]string{}}
}

func (b *stubBackend) Adopt(sessionID, handle, cwd string) error {
	if b.adoptErr != nil {
		return b.adoptErr
	}
	b.handles[sessionID] = handle
	b.adoptedAt = append(b.adoptedAt, sessionID)
	return nil
}

func (b *stubBackend) Handle(sessionID string) (string, bool) {
	h, ok := b.handles[sessionID]
	return h, ok
}

func (b *stubBackend) Name() string    { return "stub" }
func (b *stubBackend) Available() bool { return true }
func (b *stubBackend) Alive(id string) bool {
	_, ok := b.handles[id]
	return ok
}
func (b *stubBackend) Forget(id string) { delete(b.handles, id) }
func (b *stubBackend) Start(sessionID, cwd string, argv []string) (string, error) {
	b.handles[sessionID] = "sock-" + sessionID
	return b.handles[sessionID], nil
}
func (b *stubBackend) Snapshot() map[string]string {
	out := map[string]string{}
	for k, v := range b.handles {
		out[k] = v
	}
	return out
}
func (b *stubBackend) SendText(sessionID, text string) error         { return nil }
func (b *stubBackend) SendKey(sessionID string, k backend.Key) error { return nil }
func (b *stubBackend) Interrupt(sessionID string) error {
	if b.interruptErr != nil {
		return b.interruptErr
	}
	b.interrupted = append(b.interrupted, sessionID)
	return nil
}
func (b *stubBackend) Kill(sessionID string) error              { b.Forget(sessionID); return nil }
func (b *stubBackend) Capture(sessionID string) (string, error) { return "", nil }
func (b *stubBackend) Rename(sessionID, name string) error      { return nil }
func (b *stubBackend) Sweep() []string                          { return nil }
func (b *stubBackend) Status() backend.Status {
	return backend.Status{Name: "stub", Available: true}
}

// newInternalTestServer wires the real internal routes over an in-memory store
// and a stub backend.
func newInternalTestServer(t *testing.T) (*httptest.Server, *Shared, *stubBackend) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	be := newStubBackend()
	shared := NewShared(db, notifications.NewManager(db), be)
	srv := httptest.NewServer(NewInternalServer(0, shared).httpServer.Handler)
	t.Cleanup(srv.Close)
	return srv, shared, be
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// ==================== POST /internal/wrap ====================

// `helios wrap` starts the terminal itself and then tells the daemon about it,
// so the session has to exist and be bound to that terminal before any hook
// arrives — that is what makes it addressable from the phone right away.
func TestWrap_RegistersManagedSessionBoundToTerminal(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)

	resp := postJSON(t, srv.URL+"/internal/wrap",
		`{"session_id":"sess-1","handle":"/tmp/sess-1.sock","cwd":"/tmp/proj"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	sess, err := shared.DB.GetSession("sess-1")
	if err != nil || sess == nil {
		t.Fatalf("session not registered: %v", err)
	}
	if !sess.Managed {
		t.Error("session should be managed")
	}
	if sess.Status != "starting" {
		t.Errorf("status = %q, want starting", sess.Status)
	}
	if sess.Source != "claude" {
		t.Errorf("source = %q, want claude", sess.Source)
	}
	if sess.CWD != "/tmp/proj" {
		t.Errorf("cwd = %q, want /tmp/proj", sess.CWD)
	}

	if h, ok := be.Handle("sess-1"); !ok || h != "/tmp/sess-1.sock" {
		t.Errorf("terminal handle = %q (%v), want /tmp/sess-1.sock", h, ok)
	}
}

// The trust dialog only appears before the agent reports in, so the session
// joins the pending set the watcher polls.
func TestWrap_AddsSessionToTrustWatch(t *testing.T) {
	srv, shared, _ := newInternalTestServer(t)

	postJSON(t, srv.URL+"/internal/wrap",
		`{"session_id":"sess-1","handle":"/tmp/sess-1.sock","cwd":"/tmp/proj"}`)

	p, ok := shared.Pending.Get("sess-1")
	if !ok {
		t.Fatal("session should be pending trust")
	}
	if p.CWD != "/tmp/proj" {
		t.Errorf("pending cwd = %q, want /tmp/proj", p.CWD)
	}
}

func TestWrap_RejectsMissingSessionID(t *testing.T) {
	srv, shared, _ := newInternalTestServer(t)

	resp := postJSON(t, srv.URL+"/internal/wrap", `{"handle":"/tmp/x.sock","cwd":"/tmp"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	sessions, err := shared.DB.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("registered %d sessions on a bad request, want 0", len(sessions))
	}
}

// A terminal that cannot be adopted still leaves a session record: the command
// is already running, and dropping it would hide it from every client.
func TestWrap_AdoptFailureStillRegistersSession(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	be.adoptErr = errors.New("socket gone")

	resp := postJSON(t, srv.URL+"/internal/wrap",
		`{"session_id":"sess-1","handle":"/tmp/sess-1.sock","cwd":"/tmp/proj"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if sess, _ := shared.DB.GetSession("sess-1"); sess == nil {
		t.Fatal("session should have been registered anyway")
	}
}

// ==================== terminal injection ====================

func seedSession(t *testing.T, db *store.Store, sessionID, source string) {
	t.Helper()
	if err := db.UpsertSession(&store.Session{
		SessionID: sessionID,
		Source:    source,
		CWD:       "/tmp/proj",
		Status:    "active",
	}); err != nil {
		t.Fatalf("seed %s: %v", sessionID, err)
	}
}

// The terminal handle is not stored: it is injected per request from the
// backend, which is what lets clients tell a live session from a cold one.
func TestInjectTerminal_WarmSessionCarriesHandle(t *testing.T) {
	_, shared, be := newInternalTestServer(t)
	be.handles["sess-warm"] = "/tmp/warm.sock"

	sess := &store.Session{SessionID: "sess-warm"}
	shared.injectTerminal(sess)

	if sess.Terminal == nil || *sess.Terminal != "/tmp/warm.sock" {
		t.Errorf("terminal = %v, want /tmp/warm.sock", sess.Terminal)
	}
}

func TestInjectTerminal_ColdSessionHasNoHandle(t *testing.T) {
	_, shared, _ := newInternalTestServer(t)

	sess := &store.Session{SessionID: "sess-cold"}
	shared.injectTerminal(sess)

	if sess.Terminal != nil {
		t.Errorf("terminal = %v, want nil for a cold session", *sess.Terminal)
	}
}

func listInternalSessions(t *testing.T, url string) map[string]store.Session {
	t.Helper()
	resp, err := http.Get(url + "/internal/sessions")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	defer resp.Body.Close()

	var body struct {
		Sessions []store.Session `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	out := map[string]store.Session{}
	for _, s := range body.Sessions {
		out[s.SessionID] = s
	}
	return out
}

func TestListSessions_InjectsTerminalOnlyForWarmSessions(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSession(t, shared.DB, "sess-warm", "claude")
	seedSession(t, shared.DB, "sess-cold", "claude")
	be.handles["sess-warm"] = "/tmp/warm.sock"

	got := listInternalSessions(t, srv.URL)

	warm, ok := got["sess-warm"]
	if !ok {
		t.Fatal("warm session missing from the list")
	}
	if warm.Terminal == nil || *warm.Terminal != "/tmp/warm.sock" {
		t.Errorf("warm terminal = %v, want /tmp/warm.sock", warm.Terminal)
	}
	if cold := got["sess-cold"]; cold.Terminal != nil {
		t.Errorf("cold terminal = %v, want nil", *cold.Terminal)
	}
}

// ==================== POST /internal/sessions/{id}/stop ====================

func seedSessionWithStatus(t *testing.T, db *store.Store, sessionID, status string) {
	t.Helper()
	if err := db.UpsertSession(&store.Session{
		SessionID: sessionID,
		Source:    "claude",
		CWD:       "/tmp/proj",
		Status:    status,
	}); err != nil {
		t.Fatalf("seed %s: %v", sessionID, err)
	}
}

func sessionStatus(t *testing.T, db *store.Store, sessionID string) string {
	t.Helper()
	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil {
		t.Fatalf("get %s: %v", sessionID, err)
	}
	return sess.Status
}

// Interrupting a turn produces no Stop hook, so the stop has to settle the
// status itself or the session looks busy forever.
func TestStop_InterruptsAndSettlesToIdle(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "active")
	be.handles["sess-1"] = "/tmp/sess-1.sock"

	resp := postJSON(t, srv.URL+"/internal/sessions/sess-1/stop", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if len(be.interrupted) != 1 || be.interrupted[0] != "sess-1" {
		t.Errorf("interrupted = %v, want [sess-1]", be.interrupted)
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "idle" {
		t.Errorf("status = %q, want idle", got)
	}
}

// A permission prompt is a turn in progress too.
func TestStop_WorksWhileWaitingOnPermission(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "waiting_permission")
	be.handles["sess-1"] = "/tmp/sess-1.sock"

	postJSON(t, srv.URL+"/internal/sessions/sess-1/stop", "")

	if len(be.interrupted) != 1 {
		t.Errorf("interrupted = %v, want the session interrupted", be.interrupted)
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "idle" {
		t.Errorf("status = %q, want idle", got)
	}
}

// An active status with no terminal behind it is stale; stopping settles it
// rather than reporting success and leaving clients showing a busy session.
func TestStop_ColdSessionStillSettles(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "active")

	resp := postJSON(t, srv.URL+"/internal/sessions/sess-1/stop", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(be.interrupted) != 0 {
		t.Errorf("interrupted = %v, want nothing — there is no terminal", be.interrupted)
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "idle" {
		t.Errorf("status = %q, want idle", got)
	}
}

func TestStop_IdleSessionIsRejected(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.handles["sess-1"] = "/tmp/sess-1.sock"

	resp := postJSON(t, srv.URL+"/internal/sessions/sess-1/stop", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if len(be.interrupted) != 0 {
		t.Errorf("interrupted = %v, want nothing", be.interrupted)
	}
}

// A failed interrupt leaves the agent running, so the status must keep saying
// so — reporting idle would hide a turn that is still burning tokens.
func TestStop_FailedInterruptKeepsSessionActive(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "active")
	be.handles["sess-1"] = "/tmp/sess-1.sock"
	be.interruptErr = errors.New("socket gone")

	resp := postJSON(t, srv.URL+"/internal/sessions/sess-1/stop", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
}

// Clients are told without polling: this is what refreshes the phone and the
// TUI list the moment the agent is stopped.
func TestStop_BroadcastsIdleOverSSE(t *testing.T) {
	srv, shared, be := newInternalTestServer(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "active")
	be.handles["sess-1"] = "/tmp/sess-1.sock"

	resp, err := http.Get(srv.URL + "/internal/events")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer resp.Body.Close()

	// Broadcast only reaches subscribers already registered.
	for i := 0; shared.SSE.ClientCount() == 0; i++ {
		if i > 100 {
			t.Fatal("SSE client never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The type rides on its own `event:` line and the payload is the bare
	// update — the shape every client parses against.
	type frame struct{ event, data string }
	frames := make(chan frame, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		var eventType string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frames <- frame{eventType, strings.TrimPrefix(line, "data: ")}
				return
			}
		}
	}()

	postJSON(t, srv.URL+"/internal/sessions/sess-1/stop", "")

	select {
	case f := <-frames:
		if f.event != "session_status" {
			t.Errorf("event = %q, want session_status", f.event)
		}
		var got struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		}
		if err := json.Unmarshal([]byte(f.data), &got); err != nil {
			t.Fatalf("decode %q: %v", f.data, err)
		}
		if got.SessionID != "sess-1" || got.Status != "idle" {
			t.Errorf("payload = %+v, want sess-1 idle", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no session_status event after stopping the session")
	}
}

// Queuing a prompt writes into the terminal, so a session can only advertise
// the feature while it has one.
func TestListSessions_PromptQueueFollowsTerminal(t *testing.T) {
	provider.RegisterProvider(provider.ProviderInfo{
		ID:           "queueing",
		Name:         "Queueing",
		Capabilities: provider.ProviderCapabilities{PromptQueue: true},
	}, nil, nil)

	srv, shared, be := newInternalTestServer(t)
	seedSession(t, shared.DB, "sess-warm", "queueing")
	seedSession(t, shared.DB, "sess-cold", "queueing")
	be.handles["sess-warm"] = "/tmp/warm.sock"

	got := listInternalSessions(t, srv.URL)

	if !got["sess-warm"].SupportsPromptQueue {
		t.Error("a warm session of a queueing provider should support the queue")
	}
	if got["sess-cold"].SupportsPromptQueue {
		t.Error("a cold session has no terminal to queue into")
	}
}
