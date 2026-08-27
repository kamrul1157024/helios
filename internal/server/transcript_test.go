package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// transcriptEnv is a PublicServer over an in-memory store with one session
// whose transcript is a file the test writes to.
type transcriptEnv struct {
	t    *testing.T
	srv  *PublicServer
	path string
}

func newTranscriptEnv(t *testing.T) *transcriptEnv {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create transcript: %v", err)
	}
	if err := db.UpsertSession(&store.Session{
		SessionID:      "s1",
		Source:         "claude",
		CWD:            "/tmp",
		Status:         "idle",
		TranscriptPath: &path,
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	shared := NewShared(db, notifications.NewManager(db), newStubBackend())
	return &transcriptEnv{t: t, srv: &PublicServer{shared: shared}, path: path}
}

func (e *transcriptEnv) append(texts ...string) {
	e.t.Helper()
	f, err := os.OpenFile(e.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		e.t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	for _, text := range texts {
		line := fmt.Sprintf(
			`{"type":"assistant","timestamp":"2026-08-12T10:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`,
			text,
		) + "\n"
		if _, err := f.WriteString(line); err != nil {
			e.t.Fatalf("append: %v", err)
		}
	}
}

func (e *transcriptEnv) get(query string) *transcript.TranscriptResult {
	e.t.Helper()
	r := httptest.NewRequest("GET", "/api/sessions/s1/transcript"+query, nil)
	w := httptest.NewRecorder()
	e.srv.handleSessionTranscript(w, r)
	if w.Code != 200 {
		e.t.Fatalf("GET %s = %d: %s", query, w.Code, w.Body.String())
	}
	var result transcript.TranscriptResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		e.t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return &result
}

func texts(msgs []transcript.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestTranscriptEndpointPagesFromTheEnd(t *testing.T) {
	e := newTranscriptEnv(t)
	e.append("one", "two", "three")

	newest := e.get("?limit=2")
	if got := texts(newest.Messages); fmt.Sprint(got) != "[two three]" {
		t.Fatalf("newest page = %v, want [two three]", got)
	}
	if !newest.HasMore || newest.Total != 3 {
		t.Fatalf("HasMore=%v Total=%d, want true and 3", newest.HasMore, newest.Total)
	}

	older := e.get("?limit=2&offset=2")
	if got := texts(older.Messages); fmt.Sprint(got) != "[one]" {
		t.Fatalf("older page = %v, want [one]", got)
	}
	if older.HasMore {
		t.Fatal("HasMore true at the start of the transcript")
	}
}

// A client watching a running session asks for what arrived since it last
// looked, rather than pulling the page again on every event.
func TestTranscriptEndpointServesDeltas(t *testing.T) {
	e := newTranscriptEnv(t)
	e.append("one", "two")

	first := e.get("?limit=50")
	newest := first.Messages[len(first.Messages)-1].Seq

	e.append("three")

	delta := e.get(fmt.Sprintf("?after_seq=%d&epoch=%s", newest, first.Epoch))
	if got := texts(delta.Messages); fmt.Sprint(got) != "[three]" {
		t.Fatalf("delta = %v, want [three]", got)
	}
	if delta.EpochChanged {
		t.Fatal("epoch reported as changed after a plain append")
	}
}

// A delta quoting an epoch that no longer holds has to come back as a whole
// page, or the client would splice new messages onto a transcript that is no
// longer the one they came from.
func TestTranscriptEndpointResetsOnStaleEpoch(t *testing.T) {
	e := newTranscriptEnv(t)
	e.append("one", "two")
	e.get("?limit=50")

	if err := os.WriteFile(e.path, nil, 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	e.append("fresh")

	delta := e.get("?after_seq=1&epoch=stale")
	if !delta.EpochChanged {
		t.Fatal("stale epoch not reported")
	}
	if got := texts(delta.Messages); fmt.Sprint(got) != "[fresh]" {
		t.Fatalf("messages = %v, want the newest page [fresh]", got)
	}
}

// Entering a git worktree moves a session's cwd, and Claude Code moves the
// transcript with it. The path recorded at SessionStart then points at nothing,
// and the session's messages have to be found where they actually are.
func TestTranscriptEndpointFollowsAMovedTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	e := newTranscriptEnv(t)
	e.append("one", "two")

	moved := filepath.Join(home, ".claude", "projects", "-tmp--claude-worktrees-feature")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	movedPath := filepath.Join(moved, "s1.jsonl")
	if err := os.WriteFile(movedPath, body, 0o600); err != nil {
		t.Fatalf("write moved transcript: %v", err)
	}
	if err := os.Remove(e.path); err != nil {
		t.Fatalf("remove old transcript: %v", err)
	}

	if got := texts(e.get("?limit=50").Messages); fmt.Sprint(got) != "[one two]" {
		t.Fatalf("messages = %v, want [one two]", got)
	}

	// And the new path is recorded, so the next read does not go looking again.
	sess, err := e.srv.shared.DB.GetSession("s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.TranscriptPath == nil || *sess.TranscriptPath != movedPath {
		t.Errorf("recorded path = %v, want %q", sess.TranscriptPath, movedPath)
	}
}

// A transcript that is simply gone is an empty one, not a failed request: the
// client polls this endpoint, and there is nothing for it to retry.
func TestTranscriptEndpointEmptyWhenTheFileIsGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	e := newTranscriptEnv(t)
	e.append("one")
	if err := os.Remove(e.path); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}

	if got := e.get("?limit=50"); got.Total != 0 {
		t.Errorf("total = %d, want 0", got.Total)
	}
}

func TestTranscriptEndpointEmptyForSessionWithoutTranscript(t *testing.T) {
	e := newTranscriptEnv(t)
	if err := e.srv.shared.DB.UpsertSession(&store.Session{
		SessionID: "s2",
		Source:    "claude",
		CWD:       "/tmp",
		Status:    "idle",
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	r := httptest.NewRequest("GET", "/api/sessions/s2/transcript", nil)
	w := httptest.NewRecorder()
	e.srv.handleSessionTranscript(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if total, _ := body["total"].(float64); total != 0 {
		t.Fatalf("total = %v, want 0", body["total"])
	}
}
