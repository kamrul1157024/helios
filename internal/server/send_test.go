package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// sendBackend is a stub that can wake sessions, and that lets a test drive
// what the agent does in response to being woken or typed into.
type sendBackend struct {
	*stubBackend

	mu     sync.Mutex
	woken  []string
	sent   []string
	onWake func()
	onSend func()
	// screens is what Capture hands back, one per call, holding on the last.
	// A TUI painting itself is a run of different screens followed by one that
	// stops changing, and that is all awaitQuietScreen reads.
	screens []string
	screen  string
}

// Capture answers with the next queued screen, and keeps answering with the
// last one once they run out. Driven by the call rather than by a clock, so a
// test of the settle does not depend on how fast this machine sleeps.
func (b *sendBackend) Capture(sessionID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.screens) > 0 {
		b.screen = b.screens[0]
		b.screens = b.screens[1:]
	}
	return b.screen, nil
}

func (b *sendBackend) queueScreens(screens ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.screens = append(b.screens, screens...)
}

func (b *sendBackend) framesLeft() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.screens)
}

func (b *sendBackend) Wake(sessionID, cwd string) (bool, error) {
	b.mu.Lock()
	b.woken = append(b.woken, sessionID)
	b.handles[sessionID] = "sock-" + sessionID
	hook := b.onWake
	b.mu.Unlock()

	if hook != nil {
		hook()
	}
	return true, nil
}

func (b *sendBackend) SendText(sessionID, text string) error {
	b.mu.Lock()
	b.sent = append(b.sent, text)
	hook := b.onSend
	b.mu.Unlock()

	if hook != nil {
		hook()
	}
	return nil
}

func (b *sendBackend) sentTexts() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sent...)
}

func (b *sendBackend) wokenNone() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.woken) == 0
}

func (b *sendBackend) wakeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.woken)
}

// newSendTest wires the real send handler over an in-memory store. The handler
// is called directly: the point of these tests is the delivery protocol, not
// the router or its auth.
func newSendTest(t *testing.T) (*PublicServer, *Shared, *sendBackend) {
	t.Helper()

	boot, ack, bootAck := agentBootTimeout, promptAckTimeout, bootPromptAckTimeout
	agentBootTimeout, promptAckTimeout = 300*time.Millisecond, 300*time.Millisecond
	bootPromptAckTimeout = 300 * time.Millisecond
	t.Cleanup(func() {
		agentBootTimeout, promptAckTimeout, bootPromptAckTimeout = boot, ack, bootAck
	})

	interval, settle, blank := screenSettleInterval, screenSettleTimeout, screenBlankGrace
	screenSettleInterval = 5 * time.Millisecond
	screenSettleTimeout = 300 * time.Millisecond
	screenBlankGrace = 20 * time.Millisecond
	t.Cleanup(func() {
		screenSettleInterval, screenSettleTimeout, screenBlankGrace = interval, settle, blank
	})

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	be := &sendBackend{stubBackend: newStubBackend()}
	shared := NewShared(db, notifications.NewManager(db), be)
	return &PublicServer{shared: shared}, shared, be
}

func sendPrompt(t *testing.T, s *PublicServer, id, message string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	body := `{"message":` + strconv.Quote(message) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/send", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleSessionSend(rec, req)

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return rec, payload
}

// The host's socket exists seconds before the agent reads from its terminal.
// Typing into that gap is how a prompt vanished without a trace.
func TestSend_ColdSessionTypesOnlyAfterTheAgentIsUp(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	var mu sync.Mutex
	agentUp, typedTooEarly := false, false

	be.onWake = func() {
		go func() {
			time.Sleep(30 * time.Millisecond)
			mu.Lock()
			agentUp = true
			mu.Unlock()
			shared.Signals.Fire(SignalAgentReady, "sess-1")
		}()
	}
	be.onSend = func() {
		mu.Lock()
		if !agentUp {
			typedTooEarly = true
		}
		mu.Unlock()
		shared.Signals.Fire(SignalPromptSubmitted, "sess-1")
	}

	rec, payload := sendPrompt(t, s, "sess-1", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if typedTooEarly {
		t.Error("prompt was typed before the agent reported in")
	}
	if payload["resumed"] != true {
		t.Errorf("resumed = %v, want true", payload["resumed"])
	}
}

// A session that never finishes booting must not be typed into at all, and
// must not be left looking like it is working on something.
func TestSend_ColdSessionThatNeverReportsIsRefused(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	rec, _ := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
	if got := be.sentTexts(); len(got) != 0 {
		t.Errorf("typed %q into a session that was not up", got)
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "idle" {
		t.Errorf("status = %q, want it left idle", got)
	}
}

// The agent's own hook is the only proof a prompt landed. Without it the send
// failed, however well the typing itself went.
func TestSend_UnacknowledgedPromptIsAnError(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.onWake = func() { shared.Signals.Fire(SignalAgentReady, "sess-1") }

	rec, payload := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
	if payload["error"] == nil {
		t.Errorf("body = %s, want an error", rec.Body.String())
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "idle" {
		t.Errorf("status = %q, want it left idle — the prompt never arrived", got)
	}
}

// The prompt may yet be sitting in a dialog the agent put up, so a second copy
// would be a second turn. One attempt, then an honest failure.
func TestSend_UnacknowledgedPromptIsNotRetyped(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.onWake = func() { shared.Signals.Fire(SignalAgentReady, "sess-1") }

	sendPrompt(t, s, "sess-1", "hello")

	if got := be.sentTexts(); len(got) != 1 {
		t.Errorf("typed %d times (%q), want exactly one attempt", len(got), got)
	}
}

// An acknowledged prompt is the only success, and the hook that acknowledged
// it owns the status — writing "active" here too is what made a lost prompt
// look like a working one.
func TestSend_AcknowledgedPromptSucceeds(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.onWake = func() { shared.Signals.Fire(SignalAgentReady, "sess-1") }
	be.onSend = func() {
		// What the real hook does before it reports in.
		shared.DB.UpdateSessionStatus("sess-1", "active", "UserPromptSubmit")
		shared.Signals.Fire(SignalPromptSubmitted, "sess-1")
	}

	rec, payload := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if payload["success"] != true {
		t.Errorf("success = %v, want true", payload["success"])
	}
	if got := be.sentTexts(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("typed %q, want [hello]", got)
	}
	if got := sessionStatus(t, shared.DB, "sess-1"); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	sess, err := shared.DB.GetSession("sess-1")
	if err != nil || sess == nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.LastUserMessage == nil || *sess.LastUserMessage != "hello" {
		t.Errorf("last user message = %v, want hello", sess.LastUserMessage)
	}
}

// A session already running has nothing to wait for: it is reading its
// terminal now, and there will be no second SessionStart to wait on.
func TestSend_LiveSessionSkipsTheBootWait(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.handles["sess-1"] = "/tmp/sess-1.sock"
	be.onSend = func() { shared.Signals.Fire(SignalPromptSubmitted, "sess-1") }

	rec, payload := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if payload["resumed"] != false {
		t.Errorf("resumed = %v, want false", payload["resumed"])
	}
	if be.wakeCount() != 0 {
		t.Error("a live session should not have been woken")
	}
}

// A prompt queued behind a turn in progress is the agent's to pick up when it
// is done, so there is no acknowledgement coming and nothing to wait for.
func TestSend_QueuedPromptDoesNotWaitForAnAcknowledgement(t *testing.T) {
	provider.Deregister("queueing")
	if err := provider.Register(&queueingProvider{}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	t.Cleanup(func() { provider.Deregister("queueing") })

	s, shared, be := newSendTest(t)
	if err := shared.DB.UpsertSession(&store.Session{
		SessionID: "sess-1",
		Source:    "queueing",
		CWD:       "/tmp/proj",
		Status:    "active",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	be.handles["sess-1"] = "/tmp/sess-1.sock"

	rec, payload := sendPrompt(t, s, "sess-1", "and also this")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if payload["queued"] != true {
		t.Errorf("queued = %v, want true", payload["queued"])
	}
}

// queueingProvider is a provider whose agent holds a prompt sent mid-turn.
// Implementing Queuer is what declares that; there is no flag to set.
type queueingProvider struct{ sent []string }

func (q *queueingProvider) Info() provider.Info {
	return provider.Info{ID: "queueing", Name: "Queueing", Kind: provider.KindNative}
}

func (q *queueingProvider) Launch(provider.SessionSpec) (provider.Launch, error) {
	return provider.Launch{Argv: []string{"queueing"}}, nil
}

func (q *queueingProvider) QueuePrompt(sessionID, resumeID, text string) error {
	q.sent = append(q.sent, text)
	return nil
}

// A session created a moment ago is live but not yet listening: StartSession
// returns when the terminal is up, and the agent reads from it some time after
// that. Typing into that gap loses the prompt exactly as typing into a waking
// session does — and the app reaches it whenever a new session is given files,
// because the upload needs an id only the create can hand back.
func TestSend_NewSessionTypesOnlyAfterTheAgentIsUp(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "starting")
	be.handles["sess-1"] = "sock-sess-1"

	var mu sync.Mutex
	agentUp, typedTooEarly := false, false

	// What handleSessionStart does when the agent finally reports in.
	go func() {
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		agentUp = true
		mu.Unlock()
		shared.Signals.Fire(SignalAgentReady, "sess-1")
		shared.DB.UpdateSessionStatus("sess-1", "idle", "SessionStart")
	}()
	be.onSend = func() {
		mu.Lock()
		if !agentUp {
			typedTooEarly = true
		}
		mu.Unlock()
		shared.Signals.Fire(SignalPromptSubmitted, "sess-1")
	}

	rec, _ := sendPrompt(t, s, "sess-1", "look at ~/.helios/uploads/sess-1/shot.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if typedTooEarly {
		t.Error("prompt was typed before the agent reported in")
	}
	if !be.wokenNone() {
		t.Error("a live session was woken, and waking one kills the terminal it already has")
	}
}

// Reporting in is not the same as reading the terminal. The ready hook comes
// from the agent process, which is up well before its TUI has claimed the
// terminal, and the raw-mode switch that claim performs discards whatever is
// sitting in the input buffer. A prompt typed into that window is not late —
// it is gone, with no error and a session left looking idle because nothing
// was ever asked of it. So the screen has to stop moving first.
func TestSend_NewSessionTypesOnlyAfterTheScreenSettles(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "starting")
	be.handles["sess-1"] = "sock-sess-1"
	be.queueScreens("booting", "loading mcp servers", "welcome", "welcome  > ")

	// The status is written before the signal, so the wait ends whichever side
	// of the subscribe this lands on.
	go func() {
		shared.DB.UpdateSessionStatus("sess-1", "idle", "SessionStart")
		shared.Signals.Fire(SignalAgentReady, "sess-1")
	}()

	var mu sync.Mutex
	stillPainting := false
	be.onSend = func() {
		mu.Lock()
		stillPainting = be.framesLeft() > 0
		mu.Unlock()
		shared.Signals.Fire(SignalPromptSubmitted, "sess-1")
	}

	rec, _ := sendPrompt(t, s, "sess-1", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if stillPainting {
		t.Error("prompt was typed while the agent was still painting its screen")
	}
}

// A backend with no mirror to read answers blank forever. Waiting the whole
// settle out on one buys nothing and costs every send that goes through it.
func TestSend_BlankScreenDoesNotHoldTheSend(t *testing.T) {
	s, shared, be := newSendTest(t)
	// Cold, so the wake path runs and the send counts as one at a booting
	// agent. The stub never paints, which is what a backend with no mirror
	// behind it looks like.
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.onWake = func() { shared.Signals.Fire(SignalAgentReady, "sess-1") }
	be.onSend = func() { shared.Signals.Fire(SignalPromptSubmitted, "sess-1") }

	start := time.Now()
	rec, _ := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if waited := time.Since(start); waited >= screenSettleTimeout {
		t.Errorf("waited %s on a screen that was never going to say anything", waited)
	}
}

// The signal is not remembered, so an agent that reported in between the read
// and the subscribe would leave the caller waiting out the whole boot timeout
// for something that has already happened.
func TestSend_NewSessionThatIsAlreadyUpDoesNotWait(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")
	be.handles["sess-1"] = "sock-sess-1"
	be.onSend = func() { shared.Signals.Fire(SignalPromptSubmitted, "sess-1") }

	start := time.Now()
	rec, _ := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if waited := time.Since(start); waited >= agentBootTimeout {
		t.Errorf("waited %s for an agent that was already up", waited)
	}
}

// A session that never reports in is not typed into, however live its terminal.
func TestSend_NewSessionThatNeverReportsIsRefused(t *testing.T) {
	s, shared, be := newSendTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "starting")
	be.handles["sess-1"] = "sock-sess-1"

	rec, _ := sendPrompt(t, s, "sess-1", "hello")

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
	if got := be.sentTexts(); len(got) != 0 {
		t.Errorf("typed %q into a session that was not up", got)
	}
}
