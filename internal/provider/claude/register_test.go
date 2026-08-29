package claude

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
)

// flagValue returns the argument after name, and whether name was present.
func flagValue(argv []string, name string) (string, bool) {
	i := slices.Index(argv, name)
	if i < 0 || i+1 >= len(argv) {
		return "", i >= 0
	}
	return argv[i+1], true
}

func TestSessionArgsDefaultsToAutoPermissionMode(t *testing.T) {
	argv := sessionArgs(provider.SessionSpec{SessionID: "s1", CWD: "/tmp"})

	got, ok := flagValue(argv, "--permission-mode")
	if !ok {
		t.Fatalf("argv = %v, want a --permission-mode flag", argv)
	}
	if got != "auto" {
		t.Errorf("--permission-mode = %q, want auto", got)
	}
}

func TestSessionArgsHonoursExplicitMode(t *testing.T) {
	argv := sessionArgs(provider.SessionSpec{SessionID: "s1", PermissionMode: "plan"})

	if got, _ := flagValue(argv, "--permission-mode"); got != "plan" {
		t.Errorf("--permission-mode = %q, want plan: an explicit mode must beat the default", got)
	}
	if n := strings.Count(strings.Join(argv, " "), "--permission-mode"); n != 1 {
		t.Errorf("--permission-mode appears %d times, want 1", n)
	}
}

// TestSessionArgsSkipPermissionsExcludesMode pins the one interaction that
// matters: --dangerously-skip-permissions bypasses the hook chain the modes run
// through, so sending both would be contradictory.
func TestSessionArgsSkipPermissionsExcludesMode(t *testing.T) {
	argv := sessionArgs(provider.SessionSpec{SessionID: "s1", SkipPermissions: true, PermissionMode: "plan"})

	if !slices.Contains(argv, "--dangerously-skip-permissions") {
		t.Errorf("argv = %v, want --dangerously-skip-permissions", argv)
	}
	if slices.Contains(argv, "--permission-mode") {
		t.Errorf("argv = %v, want no --permission-mode alongside the skip flag", argv)
	}
}

// TestSessionArgsPromptStaysLast guards the positional: a flag appended after
// the prompt would be read as more prompt text.
func TestSessionArgsPromptStaysLast(t *testing.T) {
	argv := sessionArgs(provider.SessionSpec{
		SessionID: "s1",
		Model:     "opus",
		Prompt:    "fix the build",
	})

	if argv[len(argv)-1] != "fix the build" {
		t.Errorf("argv = %v, want the prompt last", argv)
	}
	if got, _ := flagValue(argv, "--model"); got != "opus" {
		t.Errorf("--model = %q, want opus", got)
	}
	if got, _ := flagValue(argv, "--session-id"); got != "s1" {
		t.Errorf("--session-id = %q, want s1", got)
	}
}

// TestSessionArgsOmitsEmptyFields keeps blank values from reaching the CLI as
// empty flag arguments, which it rejects.
func TestSessionArgsOmitsEmptyFields(t *testing.T) {
	argv := sessionArgs(provider.SessionSpec{})

	for _, flag := range []string{"--session-id", "--model"} {
		if slices.Contains(argv, flag) {
			t.Errorf("argv = %v, want no %s for an empty spec", argv, flag)
		}
	}
	if argv[0] != "claude" {
		t.Errorf("argv[0] = %q, want claude", argv[0])
	}
}

// TestResumeArgsCarriesStoredMode is the reason the mode is persisted at all:
// --permission-mode is a per-invocation flag, so a wake that omitted it would
// silently undo the user's switch.
func TestResumeArgsCarriesStoredMode(t *testing.T) {
	argv := ResumeArgs("s1", "plan")

	if got, _ := flagValue(argv, "--resume"); got != "s1" {
		t.Errorf("--resume = %q, want s1", got)
	}
	if got, _ := flagValue(argv, "--permission-mode"); got != "plan" {
		t.Errorf("--permission-mode = %q, want plan", got)
	}
}

// TestResumeArgsFallsBackForUnusableModes covers anything a stale client sends:
// claude rejects an unknown mode at startup, so a bad stored value would leave
// the session unable to wake.
func TestResumeArgsFallsBackForUnusableModes(t *testing.T) {
	for _, mode := range []string{"yolo", "default"} {
		argv := ResumeArgs("s1", mode)
		if got, _ := flagValue(argv, "--permission-mode"); got != DefaultPermissionMode {
			t.Errorf("ResumeArgs(%q) mode = %q, want %s", mode, got, DefaultPermissionMode)
		}
	}
}

// TestResumeArgsOmitsUnsetMode is the wrapped-session case: nothing recorded
// means the user started the session themselves and Helios never picked a mode
// for it. Sending DefaultPermissionMode on the wake would escalate a session
// that launched under the CLI's own default.
func TestResumeArgsOmitsUnsetMode(t *testing.T) {
	argv := ResumeArgs("s1", "")

	if slices.Contains(argv, "--permission-mode") {
		t.Errorf("argv = %v, want no --permission-mode for an unset mode", argv)
	}
	if got, _ := flagValue(argv, "--resume"); got != "s1" {
		t.Errorf("--resume = %q, want s1", got)
	}
}

// TestLaunchPermissionModeMatchesArgv pins the recorded mode to the one the
// agent is actually launched with: they are read back together on the next
// wake, so a disagreement would resume the session in a mode it never ran in.
func TestLaunchPermissionModeMatchesArgv(t *testing.T) {
	for _, spec := range []provider.SessionSpec{
		{},
		{PermissionMode: "plan"},
		{PermissionMode: "nonsense"},
	} {
		argv := sessionArgs(spec)
		want, _ := flagValue(argv, "--permission-mode")
		if got := LaunchPermissionMode(spec); got != want {
			t.Errorf("LaunchPermissionMode(%+v) = %q, argv has %q", spec, got, want)
		}
	}
}

// Skipping permissions has no resume equivalent, so it is recorded as the mode
// that carries the same intent — otherwise the wake would drop it entirely and
// start asking a user who explicitly opted out.
func TestLaunchPermissionModeRecordsSkipAsBypass(t *testing.T) {
	spec := provider.SessionSpec{SkipPermissions: true}

	if got := LaunchPermissionMode(spec); got != "bypassPermissions" {
		t.Errorf("LaunchPermissionMode(skip) = %q, want bypassPermissions", got)
	}
	if argv := sessionArgs(spec); slices.Contains(argv, "--permission-mode") {
		t.Errorf("argv = %v, want --dangerously-skip-permissions alone", argv)
	}
}

// TestPermissionModesMatchCLIChoices guards against drift from the vocabulary
// `claude --permission-mode` actually accepts. "default" is deliberately absent
// — the CLI has no such choice, despite it appearing in hook payloads.
func TestPermissionModesMatchCLIChoices(t *testing.T) {
	want := []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}
	for _, mode := range want {
		if !ValidPermissionMode(mode) {
			t.Errorf("ValidPermissionMode(%q) = false, want true", mode)
		}
	}
	if len(PermissionModes) != len(want) {
		t.Errorf("PermissionModes = %v, want the same %d the CLI lists", PermissionModes, len(want))
	}
	if ValidPermissionMode("default") {
		t.Error("ValidPermissionMode(\"default\") = true, but the CLI rejects it")
	}
}

func TestSessionArgsInjectsSessionScopedMCPConfig(t *testing.T) {
	mcpPort = 7654
	t.Cleanup(func() { mcpPort = 0 })

	argv := sessionArgs(provider.SessionSpec{SessionID: "s1", CWD: "/tmp"})

	raw, ok := flagValue(argv, "--mcp-config")
	if !ok {
		t.Fatalf("argv = %v, want a --mcp-config flag", argv)
	}

	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("--mcp-config is not valid JSON: %v", err)
	}

	helios, present := config.MCPServers["helios"]
	if !present {
		t.Fatalf("no helios server in %s", raw)
	}
	if helios.URL != "http://127.0.0.1:7654/mcp" {
		t.Errorf("url = %q", helios.URL)
	}
	// The agent must never need to know its own id, so it travels in a header.
	if helios.Headers["X-Helios-Session"] != "s1" {
		t.Errorf("session header = %q, want s1", helios.Headers["X-Helios-Session"])
	}
}

// Measured 2026-08-24: --mcp-config alone merges with the user's own MCP
// servers, but adding --strict-mcp-config replaces them. Passing it would
// silently strip every other MCP server from every Helios session.
func TestSessionArgsNeverPassesStrictMCPConfig(t *testing.T) {
	mcpPort = 7654
	t.Cleanup(func() { mcpPort = 0 })

	argv := sessionArgs(provider.SessionSpec{SessionID: "s1", CWD: "/tmp"})
	if slices.Contains(argv, "--strict-mcp-config") {
		t.Fatalf("argv = %v; --strict-mcp-config would remove the user's other MCP servers", argv)
	}
}

// A daemon that reported no port cannot describe a reachable server, and an
// unreachable one would make every session log a failed MCP connection.
func TestSessionArgsOmitsMCPConfigWithoutAPort(t *testing.T) {
	mcpPort = 0

	argv := sessionArgs(provider.SessionSpec{SessionID: "s1", CWD: "/tmp"})
	if _, ok := flagValue(argv, "--mcp-config"); ok {
		t.Fatalf("argv = %v, want no --mcp-config when no port is known", argv)
	}
}

// The hook URL and the MCP config answer different questions, and only the
// second is optional. Conflating them wrote every hook URL as
// http://localhost:0 whenever the MCP feature flag was off, and the daemon
// heard nothing from any session.
func TestHookURLsIgnoreTheMCPPort(t *testing.T) {
	cfg := hookConfig(7654)
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "localhost:0") {
		t.Fatalf("a hook URL points at port 0:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), "localhost:7654/hooks/claude") {
		t.Errorf("hook URLs do not use the port given:\n%s", encoded)
	}
}

// MCP off means no --mcp-config on the argv, and nothing else.
func TestMCPPortOffLeavesHooksAlone(t *testing.T) {
	p := New(7654, 0)
	launch, err := p.Launch(provider.SessionSpec{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, a := range launch.Argv {
		if a == "--mcp-config" {
			t.Error("MCP config injected while the feature is off")
		}
	}
	if p.hookPort != 7654 {
		t.Errorf("hookPort = %d, want 7654", p.hookPort)
	}
}
