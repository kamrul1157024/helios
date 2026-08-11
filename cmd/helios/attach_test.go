package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

// ==================== resolveBinary ====================

// The host is spawned detached and executes the command itself, so it cannot
// be relied on to have the PATH the user typed the command under.
func TestResolveBinary_ResolvesToAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir)

	got := resolveBinary([]string{"claude", "--session-id", "abc"})
	want := []string{bin, "--session-id", "abc"}
	if !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// Resolution can fail, and the bare name is then the best guess.
func TestResolveBinary_UnresolvableBinaryKeepsBareName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := resolveBinary([]string{"claude", "-p"})
	if want := []string{"claude", "-p"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// Nothing reinterprets the command, so arguments a shell would have mangled
// pass through as single words.
func TestResolveBinary_LeavesArgumentsAlone(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	args := []string{"two words", "$HOME", "`id`", `say "hi"`, "it's"}
	got := resolveBinary(append([]string{"claude"}, args...))
	if want := append([]string{"claude"}, args...); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}
