package codex

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
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
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	SetStateDir(home)
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
	// Says what happens next rather than naming one command. It used to
	// require "/hooks", which is the way to approve from inside a session
	// already running — the less likely case. Codex asks on its own at the
	// start of the next session.
	if !strings.Contains(strings.ToLower(h.Detail), "approve") {
		t.Errorf("detail %q does not say what will resolve this", h.Detail)
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

	res, err := transcript.Page(New(0).ParseLine, path, 50, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
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

	res, err := transcript.Page(p.ParseLine, path, 2, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if res.Total != 5 || res.Returned != 2 || !res.HasMore {
		t.Errorf("newest page = %+v, want total 5, returned 2, more", res)
	}
	if res.Messages[1].Content != "m4" {
		t.Errorf("last message = %q, want the newest", res.Messages[1].Content)
	}

	res, _ = transcript.Page(p.ParseLine, path, 0, 0)
	if res.Returned != 0 {
		t.Errorf("limit 0 returned %d messages, want none", res.Returned)
	}

	res, _ = transcript.Page(p.ParseLine, path, 2, 4)
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

// The hooks file is the user's. Helios owns the events it serves and nothing
// else, so an install must not delete hooks somebody wrote by hand and an
// uninstall must not take the file with it.
func TestInstallAndRemovePreserveUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "hooks.json")

	userHooks := `{"description":"mine","hooks":{"Notification":[{"matcher":"*",` +
		`"hooks":[{"type":"command","command":"say hi"}]}]}}`
	if err := os.WriteFile(path, []byte(userHooks), 0o600); err != nil {
		t.Fatalf("seed hooks: %v", err)
	}

	p := New(7654)
	if err := p.InstallHooks(provider.ScopeUser); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	events := readEvents(t, path)
	if _, kept := events["Notification"]; !kept {
		t.Error("install deleted a hook the user wrote")
	}
	if _, added := events["PermissionRequest"]; !added {
		t.Error("install did not add helios's own hooks")
	}

	if err := p.RemoveHooks(); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}
	events = readEvents(t, path)
	if _, kept := events["Notification"]; !kept {
		t.Error("uninstall deleted a hook the user wrote")
	}
	if _, present := events["PermissionRequest"]; present {
		t.Error("uninstall left helios's hooks behind")
	}
}

// A file holding only helios's hooks has no reason to survive an uninstall.
func TestRemoveDeletesAFileWeFullyOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	p := New(7654)
	if err := p.InstallHooks(provider.ScopeUser); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if err := p.RemoveHooks(); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Error("a file containing only our hooks should be removed")
	}
}

// Health asks whether the events helios owns are current, not whether the file
// as a whole matches — a user's own hook alongside ours is not "outdated".
func TestHealthIgnoresHooksThatAreNotOurs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	p := New(7654)
	if err := p.InstallHooks(provider.ScopeUser); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	path := filepath.Join(home, "hooks.json")
	cfg := map[string]interface{}{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	events := cfg["hooks"].(map[string]interface{})
	events["Notification"] = []interface{}{map[string]interface{}{"matcher": "*"}}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !p.HookHealth().Current {
		t.Error("a user's extra hook made helios report its own as outdated")
	}
}

func readEvents(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	events, _ := cfg["hooks"].(map[string]interface{})
	return events
}

// The daemon writes the evidence and the setup TUI reads it — different
// processes. Held in the daemon's database it was invisible to the TUI, which
// then reported "not trusted" for ever, including to people whose hooks were
// trusted and working.
func TestHookEvidenceIsVisibleToAnotherProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	SetStateDir(home)
	resetHookEvidence()

	p := New(7654)
	if err := p.InstallHooks(provider.ScopeUser); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	NoteHookReceivedFor("codex")

	// A fresh process: nothing in memory, only what is on disk.
	resetInMemoryEvidence()
	if !New(7654).HookHealth().Effective {
		t.Error("evidence did not survive into a process that never saw the hook")
	}
}

// resetInMemoryEvidence forgets the timestamp without deleting the file, which
// is what a restarted process looks like.
func resetInMemoryEvidence() {
	hookEvidence.mu.Lock()
	defer hookEvidence.mu.Unlock()
	hookEvidence.last = time.Time{}
}

// Naming a session costs one `codex exec`, and hooks are configured for the
// whole of Codex, so Helios's own titler reported itself as a session — five
// of the eight Codex rows on the machine this was found on, each one titled
// with the title prompt.
func TestOneShotRunsAreRecognised(t *testing.T) {
	dir := t.TempDir()
	write := func(name, meta string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(meta+"\n"), 0o600); err != nil {
			t.Fatalf("write rollout: %v", err)
		}
		return path
	}

	oneShot := write("exec.jsonl",
		`{"timestamp":"t","ordinal":0,"type":"session_meta","payload":{"cwd":"/tmp","source":"exec","originator":"codex_exec"}}`)
	interactive := write("cli.jsonl",
		`{"timestamp":"t","ordinal":0,"type":"session_meta","payload":{"cwd":"/work","source":"cli","originator":"codex-tui"}}`)

	if !IsOneShot(oneShot) {
		t.Error("a codex exec rollout was not recognised as one-shot")
	}
	if IsOneShot(interactive) {
		t.Error("an interactive session was taken for a one-shot run")
	}
	// A rollout that is not there, or not yet written, is not evidence of a
	// one-shot run — and guessing wrong there loses a real session.
	if IsOneShot(filepath.Join(dir, "absent.jsonl")) {
		t.Error("a missing rollout was taken for a one-shot run")
	}
}

// The whole point of recognising a one-shot run: it must not reach the
// database, from either direction — the hook it fires as it starts, or the
// rollout it leaves behind for discovery to find.
func TestOneShotRunsAreNotTracked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "08", "29")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	rollout := filepath.Join(day, "rollout-2026-08-29T20-30-44-01a04dee-183a-7461-9bef-5f05c0aa510a.jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"timestamp":"t","ordinal":0,"type":"session_meta","payload":{"cwd":"/tmp","source":"exec","originator":"codex_exec"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	New(0).Discover(db)
	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("discovery registered %d one-shot runs, want none", len(sessions))
	}

	ctx := &provider.HookContext{
		DB:             db,
		Notify:         func(string, interface{}) {},
		Report:         func(provider.ReportEvent) {},
		SessionStarted: func(string) {},
	}
	body, _ := json.Marshal(map[string]string{
		"session_id":      "01a04dee-183a-7461-9bef-5f05c0aa510a",
		"cwd":             "/tmp",
		"transcript_path": rollout,
	})
	w := httptest.NewRecorder()
	handleSessionStart(ctx, w, httptest.NewRequest("POST", "/hooks/codex/session/start", nil), body)

	if w.Code != 200 {
		t.Errorf("hook answered %d, want 200 — Codex reads anything else as a failure", w.Code)
	}
	sessions, err = db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("the session-start hook registered a one-shot run: %d rows", len(sessions))
	}

	// Every other hook too. Stop is the one that leaked: it filed a "Session
	// completed" notification for the titler's own reply, and titled the
	// session it had just heard from — which runs another exec, which stops.
	w = httptest.NewRecorder()
	stop, _ := json.Marshal(map[string]string{
		"session_id":             "01a04dee-183a-7461-9bef-5f05c0aa510a",
		"cwd":                    "/tmp",
		"transcript_path":        rollout,
		"last_assistant_message": "OK",
	})
	handleStop(ctx, w, httptest.NewRequest("POST", "/hooks/codex/stop", nil), stop)

	if w.Code != 200 {
		t.Errorf("stop hook answered %d, want 200", w.Code)
	}
	notifs, err := db.ListNotifications("", "", "")
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifs) != 0 {
		t.Errorf("a one-shot run raised %d notifications, want none", len(notifs))
	}
}

// Codex prepends the project's AGENTS.md as a user turn. Rendered, the history
// panel shows the user "saying" the contents of a config file they never
// typed. Captured from a real session in ~/workspace/opal-app/hypatia.
func TestInjectedAgentsFileIsNotAUserTurn(t *testing.T) {
	injected := "# AGENTS.md instructions for /home/u/workspace/opal-app/hypatia\n\n" +
		"<INSTRUCTIONS>\n# Agent Instructions for Hypatia\n</INSTRUCTIONS>"
	if !isInjectedContext(injected) {
		t.Error("AGENTS.md injection would render as a user message")
	}
	if !isInjectedContext("<environment_context>\n <cwd>/w</cwd>\n</environment_context>") {
		t.Error("the wrapper rule regressed")
	}
}

// The filter must stay narrow: hiding something the user typed is worse than
// showing something they did not.
func TestRealUserTextSurvivesTheFilter(t *testing.T) {
	for _, s := range []string{
		"say exactly CODEX-E2E-OK",
		"# Refactor the auth module",
		"# AGENTS.md needs updating",
		"read the <config> file",
	} {
		if isInjectedContext(s) {
			t.Errorf("a real user message was filtered out: %q", s)
		}
	}
}

// A fresh install shows two blocking dialogs back to back. Surfacing only the
// first left the session at "starting" with nothing on the phone to answer.
// Both captured verbatim from codex-cli 0.150.1 under a helios ptyhost.
func TestBothTrustDialogsAreRecognised(t *testing.T) {
	const directory = `You are in /w
  Do you trust the contents of this directory? Working with untrusted contents
› 1. Yes, continue
  2. No, quit`

	const hooks = `  11 hooks are new or changed.
  Hooks can run outside the sandbox after you trust them.
› 1. Review hooks
  2. Trust all and continue
  3. Continue without trusting (hooks won't run)`

	p := New(0)
	d := p.MatchScreen(directory)
	if d == nil || d.Type != "codex.trust" {
		t.Fatalf("directory dialog not recognised: %+v", d)
	}
	h := p.MatchScreen(hooks)
	if h == nil || h.Type != "codex.trust" {
		t.Fatalf("hook dialog not recognised: %+v", h)
	}
	// Different questions deserve different words, or the card tells the user
	// to approve a directory when it is asking about hooks.
	if d.Title == h.Title {
		t.Errorf("both dialogs share the title %q", d.Title)
	}
	if p.MatchScreen("just an ordinary prompt") != nil {
		t.Error("matched a screen with no dialog on it")
	}
}
