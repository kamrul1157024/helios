package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sessionStub is one entry of the daemon's /internal/sessions payload. A nil
// terminal is a cold session.
type sessionStub struct {
	SessionID string  `json:"session_id"`
	Terminal  *string `json:"terminal,omitempty"`
}

// fakeDaemon serves the two endpoints resolveTerminalSocket talks to. resume
// records the session it was asked to wake and answers with resumeSocket, or
// with resumeErr when that is set.
type fakeDaemon struct {
	sessions    []sessionStub
	resumeSock  string
	resumeErr   string
	resumedWith string
}

func (f *fakeDaemon) start(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"sessions": f.sessions})
	})
	mux.HandleFunc("POST /internal/sessions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		f.resumedWith = r.PathValue("id")
		json.NewEncoder(w).Encode(map[string]string{
			"terminal": f.resumeSock,
			"error":    f.resumeErr,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func sock(s string) *string { return &s }

func TestResolveTerminalSocket_WarmSessionByFullID(t *testing.T) {
	d := &fakeDaemon{sessions: []sessionStub{
		{SessionID: "aaaa-1111", Terminal: sock("/tmp/a.sock")},
		{SessionID: "bbbb-2222", Terminal: sock("/tmp/b.sock")},
	}}

	got, err := resolveTerminalSocket(d.start(t), "bbbb-2222")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/tmp/b.sock" {
		t.Errorf("socket = %q, want /tmp/b.sock", got)
	}
	if d.resumedWith != "" {
		t.Errorf("warm session should not have been resumed (resumed %q)", d.resumedWith)
	}
}

func TestResolveTerminalSocket_UniquePrefix(t *testing.T) {
	d := &fakeDaemon{sessions: []sessionStub{
		{SessionID: "aaaa-1111", Terminal: sock("/tmp/a.sock")},
		{SessionID: "bbbb-2222", Terminal: sock("/tmp/b.sock")},
	}}

	got, err := resolveTerminalSocket(d.start(t), "bb")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/tmp/b.sock" {
		t.Errorf("socket = %q, want /tmp/b.sock", got)
	}
}

// A full ID that also prefixes a longer one resolves to itself rather than
// being reported as ambiguous.
func TestResolveTerminalSocket_ExactIDBeatsPrefix(t *testing.T) {
	d := &fakeDaemon{sessions: []sessionStub{
		{SessionID: "abc", Terminal: sock("/tmp/short.sock")},
		{SessionID: "abcdef", Terminal: sock("/tmp/long.sock")},
	}}

	got, err := resolveTerminalSocket(d.start(t), "abc")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/tmp/short.sock" {
		t.Errorf("socket = %q, want /tmp/short.sock", got)
	}
}

func TestResolveTerminalSocket_AmbiguousPrefix(t *testing.T) {
	d := &fakeDaemon{sessions: []sessionStub{
		{SessionID: "abc-1", Terminal: sock("/tmp/a.sock")},
		{SessionID: "abc-2", Terminal: sock("/tmp/b.sock")},
	}}

	_, err := resolveTerminalSocket(d.start(t), "abc")
	if err == nil || !strings.Contains(err.Error(), "matches 2 sessions") {
		t.Fatalf("err = %v, want an ambiguity error", err)
	}
}

func TestResolveTerminalSocket_UnknownSession(t *testing.T) {
	d := &fakeDaemon{sessions: []sessionStub{{SessionID: "abc", Terminal: sock("/tmp/a.sock")}}}

	_, err := resolveTerminalSocket(d.start(t), "zzz")
	if err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("err = %v, want a not-found error", err)
	}
}

// A cold session has no socket, so attaching wakes it and uses the socket the
// daemon hands back.
func TestResolveTerminalSocket_ColdSessionResumes(t *testing.T) {
	d := &fakeDaemon{
		sessions:   []sessionStub{{SessionID: "cold-1", Terminal: nil}},
		resumeSock: "/tmp/woken.sock",
	}

	got, err := resolveTerminalSocket(d.start(t), "cold-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/tmp/woken.sock" {
		t.Errorf("socket = %q, want /tmp/woken.sock", got)
	}
	if d.resumedWith != "cold-1" {
		t.Errorf("resumed %q, want cold-1", d.resumedWith)
	}
}

// An empty terminal string is as cold as a missing one.
func TestResolveTerminalSocket_EmptyTerminalResumes(t *testing.T) {
	d := &fakeDaemon{
		sessions:   []sessionStub{{SessionID: "cold-2", Terminal: sock("")}},
		resumeSock: "/tmp/woken.sock",
	}

	if _, err := resolveTerminalSocket(d.start(t), "cold-2"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if d.resumedWith != "cold-2" {
		t.Errorf("resumed %q, want cold-2", d.resumedWith)
	}
}

func TestResolveTerminalSocket_ResumeFailureSurfacesDaemonError(t *testing.T) {
	d := &fakeDaemon{
		sessions:  []sessionStub{{SessionID: "cold-3"}},
		resumeErr: "no transcript to resume",
	}

	_, err := resolveTerminalSocket(d.start(t), "cold-3")
	if err == nil || !strings.Contains(err.Error(), "no transcript to resume") {
		t.Fatalf("err = %v, want the daemon's message", err)
	}
}

func TestResolveTerminalSocket_DaemonDown(t *testing.T) {
	// A port nothing is listening on: the client should say helios is not
	// running rather than leak a dial error.
	_, err := resolveTerminalSocket("http://127.0.0.1:1", "abc")
	if err == nil || !strings.Contains(err.Error(), "helios is not running") {
		t.Fatalf("err = %v, want a not-running error", err)
	}
}

// ==================== wrapCommand ====================

func TestWrapCommand_ClaudeGetsMintedSessionID(t *testing.T) {
	id, cmd, isClaude := wrapCommand([]string{"claude"})
	if !isClaude {
		t.Fatal("claude should be recognised")
	}
	if id == "" {
		t.Fatal("a session ID should have been minted")
	}
	want := []string{"claude", "--session-id", id}
	if strings.Join(cmd, " ") != strings.Join(want, " ") {
		t.Errorf("cmd = %v, want %v", cmd, want)
	}
}

// The minted flag goes right after the binary so the agent's own arguments
// keep their order and position.
func TestWrapCommand_MintedIDPrecedesUserArgs(t *testing.T) {
	id, cmd, _ := wrapCommand([]string{"claude", "--model", "opus"})
	want := []string{"claude", "--session-id", id, "--model", "opus"}
	if strings.Join(cmd, " ") != strings.Join(want, " ") {
		t.Errorf("cmd = %v, want %v", cmd, want)
	}
}

func TestWrapCommand_ResumeReusesID(t *testing.T) {
	id, cmd, isClaude := wrapCommand([]string{"claude", "--resume", "existing-id"})
	if !isClaude {
		t.Fatal("claude should be recognised")
	}
	if id != "existing-id" {
		t.Errorf("session ID = %q, want existing-id", id)
	}
	if len(cmd) != 3 {
		t.Errorf("cmd = %v, want it left untouched", cmd)
	}
}

func TestWrapCommand_SessionIDFlagReused(t *testing.T) {
	id, _, _ := wrapCommand([]string{"claude", "--session-id", "given-id"})
	if id != "given-id" {
		t.Errorf("session ID = %q, want given-id", id)
	}
}

// --continue with no value is claude's "latest session" form; there is no ID to
// reuse, so one is minted.
func TestWrapCommand_DanglingContinueMintsID(t *testing.T) {
	id, cmd, _ := wrapCommand([]string{"claude", "--continue"})
	if id == "" {
		t.Fatal("a session ID should have been minted")
	}
	if len(cmd) != 4 || cmd[1] != "--session-id" {
		t.Errorf("cmd = %v, want the minted flag injected", cmd)
	}
}

func TestWrapCommand_AbsolutePathToClaudeIsRecognised(t *testing.T) {
	_, _, isClaude := wrapCommand([]string{"/opt/homebrew/bin/claude"})
	if !isClaude {
		t.Error("a path ending in claude should be recognised")
	}
}

// Anything else still gets a session so it is addressable, but its arguments
// are left exactly as given.
func TestWrapCommand_NonClaudeLeavesArgsAlone(t *testing.T) {
	id, cmd, isClaude := wrapCommand([]string{"bash", "-l"})
	if isClaude {
		t.Error("bash should not be treated as claude")
	}
	if id == "" {
		t.Error("a session ID should have been minted")
	}
	if strings.Join(cmd, " ") != "bash -l" {
		t.Errorf("cmd = %v, want it left untouched", cmd)
	}
}

// ==================== runsHostOwnSession ====================

// A host types its command into a login shell whose rc file turns `claude`
// back into `helios wrap`. Wrapping again would attach the session to its own
// terminal and loop, so this one case runs in place.
func TestRunsHostOwnSession_HostsOwnCommandRunsInPlace(t *testing.T) {
	t.Setenv("HELIOS_SESSION_ID", "sess-1")

	if !runsHostOwnSession("sess-1") {
		t.Error("the host's own session should run in place")
	}
}

// Anything else started from inside a helios terminal is a session of its own:
// running it in place would leave it with no record and no terminal the daemon
// can reach, while the phone happily starts a second copy of it elsewhere.
func TestRunsHostOwnSession_OtherSessionsAreWrapped(t *testing.T) {
	t.Setenv("HELIOS_SESSION_ID", "sess-1")

	if runsHostOwnSession("sess-2") {
		t.Error("resuming another session should go through the normal wrap")
	}
	if runsHostOwnSession("") {
		t.Error("a session with no ID should go through the normal wrap")
	}
}

func TestRunsHostOwnSession_OutsideAHostNothingRunsInPlace(t *testing.T) {
	t.Setenv("HELIOS_SESSION_ID", "")

	if runsHostOwnSession("sess-1") {
		t.Error("outside a host every command is wrapped")
	}
}

// ==================== shellCommand ====================

// The host types this line into a login shell that has the user's rc file
// loaded — and that file is where the `claude` wrapper function lives. An
// absolute path is what stops the wrapper from swallowing the call.
func TestShellCommand_ResolvesBinaryToAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir)

	got := shellCommand([]string{"claude", "--session-id", "abc"})
	if got != bin+" --session-id abc" {
		t.Errorf("cmd = %q, want the resolved path", got)
	}
}

// Resolution can fail — the login shell may have a PATH this process does not
// — and the bare name is then the best guess.
func TestShellCommand_UnresolvableBinaryKeepsBareName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if got := shellCommand([]string{"claude", "-p"}); got != "claude -p" {
		t.Errorf("cmd = %q, want claude -p", got)
	}
}

// Arguments are typed through a shell, so anything it would reinterpret has to
// survive as one word.
func TestShellCommand_QuotesArgumentsTheShellWouldEat(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cases := []struct {
		arg  string
		want string
	}{
		{"two words", `'two words'`},
		{"$HOME", `'$HOME'`},
		{"`id`", "'`id`'"},
		{`say "hi"`, `'say "hi"'`},
		{"it's", `'it'\''s'`},
		{"--model=opus", "--model=opus"},
	}
	for _, tc := range cases {
		got := shellCommand([]string{"claude", tc.arg})
		if want := "claude " + tc.want; got != want {
			t.Errorf("shellCommand(%q) = %q, want %q", tc.arg, got, want)
		}
	}
}
