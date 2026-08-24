package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/store"
)

type fakeNotifier struct {
	sent    []map[string]interface{}
	clients int
}

func (f *fakeNotifier) Show(payload map[string]interface{}) int {
	if f.clients == 0 {
		return 0
	}
	f.sent = append(f.sent, payload)
	return f.clients
}

func setup(t *testing.T) (*Server, *fakeNotifier, *store.Store) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	notify := &fakeNotifier{clients: 1}
	return New(db, notify), notify, db
}

// post sends one JSON-RPC request. session goes in the header, as Helios
// injects it at session start.
func post(t *testing.T, s *Server, session, method string, params interface{}) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(raw)))
	if session != "" {
		req.Header.Set(sessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", method, rec.Code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: decode: %v", method, err)
	}
	return out
}

func callRaw(t *testing.T, s *Server, session, name string, args map[string]interface{}) (string, bool) {
	t.Helper()
	resp := post(t, s, session, "tools/call",
		map[string]interface{}{"name": name, "arguments": args})
	result, _ := resp["result"].(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("no content in %+v", result)
	}
	first, _ := content[0].(map[string]interface{})
	text, _ := first["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func callTool(t *testing.T, s *Server, session, name string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	text, isErr := callRaw(t, s, session, name, args)
	if isErr {
		t.Fatalf("%s reported an error: %s", name, text)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("%s: decode %q: %v", name, text, err)
	}
	return payload
}

func TestHandshake(t *testing.T) {
	s, _, _ := setup(t)

	init := post(t, s, "", "initialize", map[string]interface{}{})
	result, _ := init["result"].(map[string]interface{})
	if result["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion = %v, want %s", result["protocolVersion"], protocolVersion)
	}

	list := post(t, s, "", "tools/list", nil)
	listResult, _ := list["result"].(map[string]interface{})
	tools, _ := listResult["tools"].([]interface{})
	if len(tools) != len(toolOrder) {
		t.Fatalf("got %d tools, want %d", len(tools), len(toolOrder))
	}
	for i, want := range toolOrder {
		entry, _ := tools[i].(map[string]interface{})
		if entry["name"] != want {
			t.Fatalf("tool %d = %v, want %s", i, entry["name"], want)
		}
	}
}

func TestNotificationGetsNoBody(t *testing.T) {
	s, _, _ := setup(t)

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("notification answered with a body: %q", rec.Body.String())
	}
}

func TestUnknownMethodAndUnknownTool(t *testing.T) {
	s, _, _ := setup(t)

	resp := post(t, s, "", "resources/list", nil)
	rpcErr, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an error for an unsupported method, got %+v", resp)
	}
	if rpcErr["code"] != float64(-32601) {
		t.Fatalf("code = %v, want -32601", rpcErr["code"])
	}

	// An unknown tool is a tool-level failure, not a protocol fault: the agent
	// should read it and correct itself.
	if _, isErr := callRaw(t, s, "s1", "helios_nope", map[string]interface{}{}); !isErr {
		t.Fatal("unknown tool did not report isError")
	}
}

func TestShow_BroadcastsWhatTheAgentAskedFor(t *testing.T) {
	s, notify, _ := setup(t)

	got := callTool(t, s, "s1", "helios_show", map[string]interface{}{
		"view": "file",
		"path": "internal/oauth/registration.go",
		"line": 190,
		"note": "the 400 came from here",
	})
	if got["shown"] != true {
		t.Fatalf("shown = %v", got["shown"])
	}

	if len(notify.sent) != 1 {
		t.Fatalf("broadcast %d times, want 1", len(notify.sent))
	}
	sent := notify.sent[0]
	if sent["session_id"] != "s1" {
		t.Errorf("session_id = %v; it must come from the header, not the agent", sent["session_id"])
	}
	if sent["view"] != "file" || sent["path"] != "internal/oauth/registration.go" {
		t.Errorf("wrong payload: %+v", sent)
	}
	if sent["line"] != 190 {
		t.Errorf("line = %v, want 190", sent["line"])
	}
	if sent["note"] != "the 400 came from here" {
		t.Errorf("note lost: %+v", sent)
	}
}

// Validation answers with the fix, so the agent corrects itself and the human
// is never involved.
func TestShow_ValidationCorrectsTheAgent(t *testing.T) {
	s, notify, _ := setup(t)

	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"file without path", map[string]interface{}{"view": "file"}, "needs a path"},
		{"terminal with a path", map[string]interface{}{"view": "terminal", "path": "x.go"}, "does not take a path"},
		{"unknown view", map[string]interface{}{"view": "sidebar"}, "unknown view"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callRaw(t, s, "s1", "helios_show", tc.args)
			if !isErr {
				t.Fatalf("accepted a bad call: %s", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("message %q does not contain %q", text, tc.want)
			}
		})
	}

	if len(notify.sent) != 0 {
		t.Fatalf("a rejected call still broadcast: %+v", notify.sent)
	}
}

// diff is the one view where a path is optional: without one it shows the whole
// change, which is why there is no separate git view.
func TestShow_DiffTakesAnOptionalPath(t *testing.T) {
	s, notify, _ := setup(t)

	callTool(t, s, "s1", "helios_show", map[string]interface{}{"view": "diff"})
	callTool(t, s, "s1", "helios_show", map[string]interface{}{"view": "diff", "path": "a.go", "base": "main"})

	if len(notify.sent) != 2 {
		t.Fatalf("broadcast %d times, want 2", len(notify.sent))
	}
	if _, carried := notify.sent[0]["path"]; carried {
		t.Error("a pathless diff carried a path")
	}
	if notify.sent[1]["base"] != "main" {
		t.Errorf("base lost: %+v", notify.sent[1])
	}
}

// With nobody watching, the agent should write prose instead of pointing at a
// screen. It can only do that if the result says so.
func TestShow_ReportsWhenNobodyIsWatching(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db, &fakeNotifier{clients: 0})

	got := callTool(t, s, "s1", "helios_show", map[string]interface{}{"view": "terminal"})
	if got["shown"] != false {
		t.Fatalf("shown = %v, want false", got["shown"])
	}
	if got["reason"] != "no client attached" {
		t.Fatalf("reason = %v", got["reason"])
	}
}

// A client Helios did not start has no panel. Saying so is more useful than
// silently doing nothing.
func TestShow_RefusedWithoutASession(t *testing.T) {
	s, _, _ := setup(t)

	text, isErr := callRaw(t, s, "", "helios_show", map[string]interface{}{"view": "terminal"})
	if !isErr {
		t.Fatal("a session-less caller was allowed to show a view")
	}
	if !strings.Contains(text, "not started by Helios") {
		t.Fatalf("unhelpful message: %s", text)
	}
}

// A long-lived install has hundreds of dead sessions. Returning them buries the
// ones an agent can actually address.
func TestSessions_OmitsDeadSessionsUnlessAsked(t *testing.T) {
	s, _, db := setup(t)

	for _, sess := range []store.Session{
		{SessionID: "live", Source: "claude", CWD: "/tmp/a", Status: "active"},
		{SessionID: "dead", Source: "claude", CWD: "/tmp/b", Status: "terminated"},
	} {
		if err := db.UpsertSession(&sess); err != nil {
			t.Fatalf("upsert %s: %v", sess.SessionID, err)
		}
	}

	listed := callTool(t, s, "s1", "helios_sessions", map[string]interface{}{})
	sessions, _ := listed["sessions"].([]interface{})
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want just the live one", len(sessions))
	}
	first, _ := sessions[0].(map[string]interface{})
	if first["session"] != "live" {
		t.Fatalf("listed %v, want the live session", first["session"])
	}

	withAll := callTool(t, s, "s1", "helios_sessions", map[string]interface{}{"all": true})
	if all, _ := withAll["sessions"].([]interface{}); len(all) != 2 {
		t.Fatalf("all=true returned %d, want 2", len(all))
	}
}
