package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// ==================== Launch ====================

// The default is deliberately the opposite of Claude's. Codex's
// PermissionRequest hook fires only when Codex would ask a human, so the
// permissive mode silences the hook and the phone goes dead.
func TestLaunchDefaultsToAModeThatKeepsThePhoneUseful(t *testing.T) {
	p := New(7654)
	launch, err := p.Launch(provider.SessionSpec{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launch.Mode != ModeWorkspaceWrite {
		t.Errorf("mode = %q, want %q", launch.Mode, ModeWorkspaceWrite)
	}
	if !hasFlagPair(launch.Argv, "-a", "on-request") {
		t.Errorf("argv %q does not ask for approvals; the permission card would never fire", launch.Argv)
	}
	if !hasFlagPair(launch.Argv, "-s", "workspace-write") {
		t.Errorf("argv %q has the wrong sandbox", launch.Argv)
	}
}

// full-auto is the trap: it is the mode a user reads as "get out of my way",
// and it is the one that stops Helios being able to answer anything.
func TestFullAutoSilencesTheApprovalHook(t *testing.T) {
	launch, _ := New(0).Launch(provider.SessionSpec{PermissionMode: ModeFullAuto})
	if !hasFlagPair(launch.Argv, "-a", "never") {
		t.Errorf("argv %q, want -a never", launch.Argv)
	}
}

func TestSkipPermissionsOverridesTheMode(t *testing.T) {
	launch, _ := New(0).Launch(provider.SessionSpec{
		PermissionMode:  ModeReadOnly,
		SkipPermissions: true,
	})
	if launch.Mode != ModeBypass {
		t.Errorf("mode = %q, want %q", launch.Mode, ModeBypass)
	}
	if !slices.Contains(launch.Argv, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("argv %q does not bypass", launch.Argv)
	}
}

// The prompt is positional and must come last, or Codex reads the flags after
// it as more prompt.
func TestPromptIsLastAndWhole(t *testing.T) {
	const prompt = `fix the "auth bug" && run $(tests)`
	launch, _ := New(0).Launch(provider.SessionSpec{Prompt: prompt, Model: "gpt-5.6-sol"})
	if got := launch.Argv[len(launch.Argv)-1]; got != prompt {
		t.Errorf("last argv = %q, want the prompt verbatim", got)
	}
}

// The environment is the whole answer to Codex having no --session-id: the
// hook table's curl sends it back as a header, and it is the only way a hook
// can name a session helios knows.
func TestLaunchCarriesTheHeliosSessionID(t *testing.T) {
	launch, _ := New(0).Launch(provider.SessionSpec{SessionID: "helios-abc"})
	if launch.Env[HeliosSessionEnv] != "helios-abc" {
		t.Errorf("env[%s] = %q, want helios-abc", HeliosSessionEnv, launch.Env[HeliosSessionEnv])
	}
}

// ==================== Resume ====================

// Codex mints its own id, so resume needs the one it reported — not the one
// helios minted.
func TestResumeUsesTheIDCodexMinted(t *testing.T) {
	launch, err := New(0).Resume("helios-abc", "01a04ccb-34dc-7833", ModeReadOnly)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !slices.Contains(launch.Argv, "resume") {
		t.Fatalf("argv %q is not a resume", launch.Argv)
	}
	if launch.Argv[len(launch.Argv)-1] != "01a04ccb-34dc-7833" {
		t.Errorf("argv %q does not end with the codex id", launch.Argv)
	}
	if launch.Env[HeliosSessionEnv] != "helios-abc" {
		t.Error("a resumed session must still identify itself to helios")
	}
}

// A session whose start hook never arrived has no codex id. Refusing is the
// honest answer: a fresh conversation pretending to be the old one is worse
// than not waking it.
func TestResumeWithoutACodexIDRefuses(t *testing.T) {
	launch, err := New(0).Resume("helios-abc", "", ModeWorkspaceWrite)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(launch.Argv) != 0 {
		t.Errorf("argv = %q, want none", launch.Argv)
	}
}

// ==================== Screen watching ====================

// Codex's trust dialog is worded differently from Claude's, which is the whole
// reason the daemon asks providers instead of holding one list of phrases.
func TestMatchesItsOwnTrustDialog(t *testing.T) {
	// Verbatim from a real session under a helios ptyhost.
	const screen = `You are in /tmp/cxproj
  Do you trust the contents of this directory? Working with untrusted contents
  comes with higher risk of prompt injection.
› 1. Yes, continue
  2. No, quit`

	prompt := New(0).MatchScreen(screen)
	if prompt == nil {
		t.Fatal("codex trust dialog not recognised")
	}
	if prompt.Type != "codex.trust" {
		t.Errorf("type = %q, want codex.trust", prompt.Type)
	}
}

func TestDoesNotClaimClaudesTrustDialog(t *testing.T) {
	const screen = "Do you trust the files in this folder?\n Yes, I trust this folder"
	if prompt := New(0).MatchScreen(screen); prompt != nil {
		t.Errorf("codex claimed Claude's dialog: %+v", prompt)
	}
}

// ==================== Hook install ====================

func TestHookTableIsWrittenAndVerified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	p := New(7654)
	if err := p.InstallHooks(provider.ScopeUser); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse hooks: %v", err)
	}

	// Codex has no http handler type, so every hook must be a command.
	for event, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type != "command" {
					t.Errorf("%s: handler type %q, want command", event, h.Type)
				}
				if !strings.Contains(h.Command, "curl -sS -f") {
					t.Errorf("%s: command must fail open with -f: %q", event, h.Command)
				}
				if !strings.Contains(h.Command, HeliosSessionEnv) {
					t.Errorf("%s: command does not send the helios session header", event)
				}
			}
		}
	}

	// The one hook that holds a human decision needs a timeout that outlasts
	// the daemon's own wait, or codex walks away from a prompt still on screen.
	perm := cfg.Hooks["PermissionRequest"]
	if len(perm) == 0 || len(perm[0].Hooks) == 0 {
		t.Fatal("no PermissionRequest hook installed")
	}
	if perm[0].Hooks[0].Timeout != HookTimeoutSeconds {
		t.Errorf("PermissionRequest timeout = %d, want %d",
			perm[0].Hooks[0].Timeout, HookTimeoutSeconds)
	}

	// SessionEnd is clamped to 3s by codex whatever we ask, and asking for the
	// clamp keeps a warning out of the user's terminal on every session.
	if end := cfg.Hooks["SessionEnd"]; len(end) > 0 && end[0].Hooks[0].Timeout != sessionEndTimeout {
		t.Errorf("SessionEnd timeout = %d, want %d", end[0].Hooks[0].Timeout, sessionEndTimeout)
	}
}

// Codex ignores a malformed hooks.json in total silence, so a health check
// that only asked "did I write a file" would report healthy while receiving
// nothing.
func TestHookHealthReportsAnUninstalledTable(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	h := New(7654).HookHealth()
	if h.Installed || h.Current || h.Effective {
		t.Errorf("health = %+v, want all false", h)
	}
	if h.Detail == "" {
		t.Error("no detail telling the user what to do")
	}
}

// Installed and current is not the same as running. Codex reads an untrusted
// table and declines to run it without saying so, so Effective stays false
// until a hook actually arrives.
func TestHookHealthSeparatesInstalledFromEffective(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	resetHookEvidence()

	p := New(7654)
	if err := p.InstallHooks(provider.ScopeUser); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	h := p.HookHealth()
	if !h.Installed || !h.Current {
		t.Fatalf("health = %+v, want installed and current", h)
	}
	if h.Effective {
		t.Error("Effective is true before any hook has arrived")
	}
	if !strings.Contains(h.Detail, "/hooks") {
		t.Errorf("detail %q does not tell the user how to trust them", h.Detail)
	}

	NoteHookReceivedFor("codex")
	if !New(7654).HookHealth().Effective {
		t.Error("a received hook should be evidence the table is running")
	}
}

// ==================== Transcript ====================

func TestParsesARealRollout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	// Shapes taken from real sessions; see docs/specs/46-codex-provider.md.
	lines := []string{
		`{"timestamp":"t0","ordinal":0,"type":"session_meta","payload":{"cwd":"/w","model":"gpt-5.6-sol"}}`,
		`{"timestamp":"t1","ordinal":3,"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>\nlots of framework text\n</skills_instructions>"}]}}`,
		`{"timestamp":"t2","ordinal":5,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/w</cwd>\n</environment_context>"}]}}`,
		`{"timestamp":"t3","ordinal":8,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`,
		`{"timestamp":"t4","ordinal":9,"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"timestamp":"t5","ordinal":11,"type":"response_item","payload":{"type":"reasoning","id":"rs_1"}}`,
		`{"timestamp":"t6","ordinal":12,"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"const r = await tools.exec_command({cmd:\"echo hi\",workdir:\"/w\"})"}}`,
		`{"timestamp":"t7","ordinal":13,"type":"response_item","payload":{"type":"custom_tool_call_output","output":[{"type":"input_text","text":"Script completed\nhi\n"}]}}`,
		`{"timestamp":"t8","ordinal":14,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	res, err := New(0).ParseTranscript(path, 50, 0)
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}

	var roles []transcript.MessageRole
	for _, m := range res.Messages {
		roles = append(roles, m.Role)
	}
	want := []transcript.MessageRole{
		transcript.RoleUser, transcript.RoleToolUse,
		transcript.RoleToolResult, transcript.RoleAssistant,
	}
	if !slices.Equal(roles, want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}

	// The framework text and the injected context must not read as the user.
	for _, m := range res.Messages {
		if strings.Contains(m.Content, "skills_instructions") {
			t.Error("developer preamble leaked into the transcript")
		}
		if strings.Contains(m.Content, "environment_context") {
			t.Error("injected context rendered as a user turn")
		}
	}
	if res.Messages[0].Content != "hi" {
		t.Errorf("first user message = %q, want hi", res.Messages[0].Content)
	}

	// Ordinal is monotonic but not dense: event_msg rows consume numbers too.
	if res.Messages[0].Seq != 8 {
		t.Errorf("Seq = %d, want the rollout ordinal 8", res.Messages[0].Seq)
	}

	// Discovery reads transcripts of sessions helios never hosted, where no
	// hook ever normalised the tool call, so the command has to come out of
	// the Code Mode wrapper.
	if got := res.Messages[1].Summary; got != "echo hi" {
		t.Errorf("tool summary = %q, want the command out of the JS wrapper", got)
	}
	if strings.HasPrefix(res.Messages[2].Summary, "Script completed") {
		t.Errorf("tool output kept its preamble: %q", res.Messages[2].Summary)
	}
}

// Paging is the transcript package's contract, shared so that the same API
// call answers identically whichever provider parsed the file. limit <= 0
// means an empty page, not the whole transcript.
func TestPagingFollowsTheSharedContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	var lines []string
	for i := 0; i < 5; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"timestamp":"t","ordinal":%d,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"m%d"}]}}`,
			i, i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	p := New(0)

	res, err := p.ParseTranscript(path, 2, 0)
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	if res.Total != 5 || res.Returned != 2 || !res.HasMore {
		t.Errorf("newest page = %+v, want total 5, returned 2, more", res)
	}
	if res.Messages[1].Content != "m4" {
		t.Errorf("last message = %q, want the newest", res.Messages[1].Content)
	}

	res, _ = p.ParseTranscript(path, 0, 0)
	if res.Returned != 0 {
		t.Errorf("limit 0 returned %d messages, want none", res.Returned)
	}

	res, _ = p.ParseTranscript(path, 2, 4)
	if res.Returned != 1 || res.HasMore {
		t.Errorf("oldest page = %+v, want 1 message and no more", res)
	}
}

func TestWrapperElementMatchesStructurallyNotByName(t *testing.T) {
	// The tag varies between codex versions and modes, so the rule cannot be a
	// list of names.
	for _, s := range []string{
		"<environment_context>\n x \n</environment_context>",
		"<recommended_plugins>y</recommended_plugins>",
		"<some_future_tag>z</some_future_tag>",
	} {
		if !isWrapperElement(s) {
			t.Errorf("not recognised as a wrapper: %q", s)
		}
	}
	for _, s := range []string{
		"hi",
		"read the <config> file",
		"<a>x</b>",
	} {
		if isWrapperElement(s) {
			t.Errorf("real user text treated as a wrapper: %q", s)
		}
	}
}

func TestRolloutSessionID(t *testing.T) {
	got := rolloutSessionID("rollout-2026-08-29T09-06-53-01a04cc5-9a25-7391-9113-bc91fb8f35ca.jsonl")
	if want := "01a04cc5-9a25-7391-9113-bc91fb8f35ca"; got != want {
		t.Errorf("id = %q, want %q", got, want)
	}
}

func hasFlagPair(argv []string, flag, value string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) && argv[i+1] == value {
			return true
		}
	}
	return false
}
