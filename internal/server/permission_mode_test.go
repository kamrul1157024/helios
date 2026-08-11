package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// wakingBackend is a stubBackend that can also resume, which the restart path
// requires. It counts kill/wake pairs so tests can tell a real restart from a
// stored-only change.
type wakingBackend struct {
	*stubBackend
	killed []string
	woken  []string
}

func (b *wakingBackend) Kill(sessionID string) error {
	b.killed = append(b.killed, sessionID)
	b.Forget(sessionID)
	return nil
}

func (b *wakingBackend) Wake(sessionID, cwd string) (bool, error) {
	b.woken = append(b.woken, sessionID)
	b.handles[sessionID] = "sock-" + sessionID
	return true, nil
}

// permModeEnv is a Shared over an in-memory store with a resumable backend.
type permModeEnv struct {
	t      *testing.T
	db     *store.Store
	be     *wakingBackend
	shared *Shared
}

func newPermModeEnv(t *testing.T) *permModeEnv {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	be := &wakingBackend{stubBackend: newStubBackend()}
	return &permModeEnv{t: t, db: db, be: be, shared: NewShared(db, notifications.NewManager(db), be)}
}

// session inserts a claude session in the given status. warm decides whether
// the backend reports a live terminal for it.
func (e *permModeEnv) session(id, status string, warm bool) {
	e.t.Helper()
	if err := e.db.UpsertSession(&store.Session{
		SessionID: id,
		Source:    "claude",
		CWD:       "/tmp",
		Status:    status,
	}); err != nil {
		e.t.Fatalf("upsert session: %v", err)
	}
	if warm {
		e.be.handles[id] = "sock-" + id
	}
}

// set calls the handler and returns the recorder plus the decoded body.
func (e *permModeEnv) set(id, mode string) (*httptest.ResponseRecorder, map[string]interface{}) {
	e.t.Helper()
	w := httptest.NewRecorder()
	e.shared.setPermissionMode(w, id, mode)
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		e.t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return w, body
}

func (e *permModeEnv) storedMode(id string) string {
	e.t.Helper()
	sess, err := e.db.GetSession(id)
	if err != nil || sess == nil {
		e.t.Fatalf("get session %s: %v", id, err)
	}
	if sess.PermissionMode == nil {
		return ""
	}
	return *sess.PermissionMode
}

// TestSetPermissionModeRestartsWarmSession is the happy path: the mode is
// stored and the agent is relaunched under it. Kill must come before Wake,
// because Wake adopts a live host and would otherwise leave the old process
// running in the old mode while reporting success.
func TestSetPermissionModeRestartsWarmSession(t *testing.T) {
	e := newPermModeEnv(t)
	e.session("s1", "idle", true)

	w, body := e.set("s1", "plan")

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if body["restarted"] != true {
		t.Errorf("restarted = %v, want true for a warm session", body["restarted"])
	}
	if got := e.storedMode("s1"); got != "plan" {
		t.Errorf("stored mode = %q, want plan", got)
	}
	if len(e.be.killed) != 1 || len(e.be.woken) != 1 {
		t.Errorf("killed = %v, woken = %v, want one of each", e.be.killed, e.be.woken)
	}
}

// TestSetPermissionModeColdSessionOnlyStores pins the cheap path: a session
// with no terminal needs no restart, because the stored mode is read when it
// next wakes.
func TestSetPermissionModeColdSessionOnlyStores(t *testing.T) {
	e := newPermModeEnv(t)
	e.session("s1", "idle", false)

	w, body := e.set("s1", "acceptEdits")

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if body["restarted"] != false {
		t.Errorf("restarted = %v, want false: a cold session has nothing to restart", body["restarted"])
	}
	if got := e.storedMode("s1"); got != "acceptEdits" {
		t.Errorf("stored mode = %q, want acceptEdits", got)
	}
	if len(e.be.killed) != 0 {
		t.Errorf("killed = %v, want none", e.be.killed)
	}
}

// TestSetPermissionModeRefusesBusySession is the guard that makes restart-based
// switching safe: restarting mid-turn would discard the agent's work, and a
// pending permission prompt would be stranded with no one to answer it.
func TestSetPermissionModeRefusesBusySession(t *testing.T) {
	for _, status := range []string{"active", "waiting_permission", "compacting", "starting"} {
		t.Run(status, func(t *testing.T) {
			e := newPermModeEnv(t)
			e.session("s1", status, true)

			w, body := e.set("s1", "plan")

			if w.Code != 409 {
				t.Fatalf("status = %d, want 409 for a %s session", w.Code, status)
			}
			if body["error"] != "session_busy" {
				t.Errorf("error = %v, want session_busy", body["error"])
			}
			if got := e.storedMode("s1"); got != "" {
				t.Errorf("stored mode = %q, want none: a refused switch must not persist", got)
			}
			if len(e.be.killed) != 0 {
				t.Errorf("killed = %v, want none: a refused switch must not touch the terminal", e.be.killed)
			}
		})
	}
}

// TestSetPermissionModeRejectsUnknownMode matters because claude rejects an
// unknown mode at startup: storing one would leave the session unable to wake.
func TestSetPermissionModeRejectsUnknownMode(t *testing.T) {
	e := newPermModeEnv(t)
	e.session("s1", "idle", true)

	w, _ := e.set("s1", "yolo")

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := e.storedMode("s1"); got != "" {
		t.Errorf("stored mode = %q, want none", got)
	}
}

func TestSetPermissionModeUnknownSession(t *testing.T) {
	e := newPermModeEnv(t)

	if w, _ := e.set("nope", "plan"); w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestSetPermissionModeRefusesEndedSession(t *testing.T) {
	e := newPermModeEnv(t)
	e.session("s1", "terminated", false)

	w, body := e.set("s1", "plan")

	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if body["error"] != "session_ended" {
		t.Errorf("error = %v, want session_ended", body["error"])
	}
}
