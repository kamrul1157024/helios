package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/provider"
)

// hookCommand renders the shell for one hook.
//
// Codex has no HTTP handler type — only "command" and "mcp_tool" — so every
// hook pipes its stdin through curl and hands the reply back on stdout. That
// is enough for a blocking hook: Codex waits for the process, and curl's
// stdout becomes the decision. Measured; see docs/specs/46-codex-provider.md.
//
// -sS -f is deliberate. -f makes curl exit non-zero without printing the error
// body, so a daemon that is down or a path that 404s leaves stdout empty and
// Codex proceeds. Fail-open is right here: a dead daemon must not wedge the
// agent, and a half-written HTML error page reaching Codex's JSON parser would
// be worse than nothing.
//
// The header is how a hook says which helios session it belongs to. Codex
// mints its own id and offers no flag to set one, so the launch environment
// carries ours and the hook sends it back.
func hookCommand(base, route string) string {
	return "cat | curl -sS -f -X POST " +
		"-H 'Content-Type: application/json' " +
		`-H "X-Helios-Session: $` + HeliosSessionEnv + `" ` +
		"-d @- " + base + "/" + route
}

// hookEntry is one matcher group with its single handler.
func hookEntry(base, route string, timeout int) []interface{} {
	handler := map[string]interface{}{
		"type":    "command",
		"command": hookCommand(base, route),
	}
	if timeout > 0 {
		handler["timeout"] = timeout
	}
	return []interface{}{
		map[string]interface{}{
			"matcher": "*",
			"hooks":   []interface{}{handler},
		},
	}
}

// sessionEndTimeout is what Codex allows a SessionEnd hook, whatever we ask
// for: it clamps to 3s and warns. Asking for the clamp keeps the warning out
// of the user's terminal on every session.
const sessionEndTimeout = 3

func hookConfig(port int) map[string]interface{} {
	base := fmt.Sprintf("http://localhost:%d/hooks/codex", port)
	blocking := HookTimeoutSeconds
	return map[string]interface{}{
		"description": "helios",
		"hooks": map[string]interface{}{
			"SessionStart":     hookEntry(base, "session/start", 0),
			"SessionEnd":       hookEntry(base, "session/end", sessionEndTimeout),
			"UserPromptSubmit": hookEntry(base, "prompt/submit", 0),
			"PreToolUse":       hookEntry(base, "tool/pre", 0),
			"PostToolUse":      hookEntry(base, "tool/post", 0),
			// The only blocking one. It is what makes a Codex permission
			// request answerable from a phone.
			"PermissionRequest": hookEntry(base, "permission", blocking),
			"PreCompact":        hookEntry(base, "compact/pre", 0),
			"PostCompact":       hookEntry(base, "compact/post", 0),
			"SubagentStart":     hookEntry(base, "subagent/start", 0),
			"SubagentStop":      hookEntry(base, "subagent/stop", 0),
			"Stop":              hookEntry(base, "stop", 0),
		},
	}
}

// codexHome is where Codex keeps its configuration, honouring CODEX_HOME.
func codexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func hooksPath() string { return filepath.Join(codexHome(), "hooks.json") }

// InstallHooks writes the hook table Codex will read.
func (p *Provider) InstallHooks(scope provider.Scope) error {
	path := hooksPath()
	if scope == provider.ScopeProject {
		path = filepath.Join(".codex", "hooks.json")
	}
	// Merge, never replace. The file is the user's, and it may hold hooks they
	// wrote; owning it wholesale would delete them. Only the events helios
	// serves are touched — the Claude installer merges the same way.
	merged := map[string]interface{}{}
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &merged); err != nil {
			merged = map[string]interface{}{}
		}
	}
	events, _ := merged["hooks"].(map[string]interface{})
	if events == nil {
		events = map[string]interface{}{}
	}
	ours, ok := hookConfig(p.port)["hooks"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("build hooks: malformed config")
	}
	for event, entry := range ours {
		events[event] = entry
	}
	merged["hooks"] = events
	if merged["description"] == nil {
		merged["description"] = "helios"
	}
	cfg := merged

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hooks: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write hooks: %w", err)
	}

	// Read it back and re-parse. Codex ignores a malformed hooks.json in total
	// silence — no warning, no error, no exit code — so a bad write would
	// present as a daemon that simply never hears from the agent.
	if err := validateInstalled(path, cfg); err != nil {
		return fmt.Errorf("verify hooks at %s: %w", path, err)
	}
	return nil
}

func validateInstalled(path string, want map[string]interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	gotEvents, _ := got["hooks"].(map[string]interface{})
	wantEvents, _ := want["hooks"].(map[string]interface{})
	for event, entry := range wantEvents {
		if hashOf(gotEvents[event]) != hashOf(entry) {
			return fmt.Errorf("event %s was not written as intended", event)
		}
	}
	return nil
}

func hashOf(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HookConfigHash is the hash of the events this build would write.
func (p *Provider) HookConfigHash() string { return hashOf(hookConfig(p.port)["hooks"]) }

// ourEventsCurrent reports whether every event helios owns is present and
// matches this build. Events belonging to anyone else are not our business.
func ourEventsCurrent(installed map[string]interface{}, port int) bool {
	events, _ := installed["hooks"].(map[string]interface{})
	ours, _ := hookConfig(port)["hooks"].(map[string]interface{})
	for event, entry := range ours {
		if hashOf(events[event]) != hashOf(entry) {
			return false
		}
	}
	return true
}

// HookHealth reports whether Codex will actually run our hooks.
//
// Effective is a genuinely separate question here, unlike for Claude. Codex
// refuses to run an untrusted hook and reports nothing at all: the file is
// read, the hooks are skipped, the turn succeeds. A daemon checking only its
// own last write would report healthy while receiving no events and leaving
// every session at "starting" for ever.
//
// Trust state is not recorded in hooks.json and `codex doctor` does not report
// it, so it cannot be read directly. Effective is inferred from evidence
// instead: a hook has been received since the file was last written.
func (p *Provider) HookHealth() provider.HookHealth {
	h := provider.HookHealth{}
	path := hooksPath()

	data, err := os.ReadFile(path)
	if err != nil {
		h.Detail = "no " + path + "; run `helios hooks install`"
		return h
	}
	h.Installed = true

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		h.Detail = path + " is not valid JSON; codex ignores it in silence"
		return h
	}
	h.Current = ourEventsCurrent(got, p.port)
	if !h.Current {
		h.Detail = "hooks are from an older helios; run `helios hooks install`"
		return h
	}

	h.Effective = hooksSeenRecently()
	if !h.Effective {
		h.Detail = "set up, but codex has not run them yet — approve with /hooks"
	}
	return h
}

// evidenceFile is where the last-hook timestamp is persisted.
//
// A file, not the daemon's database, because two processes need it: the
// daemon writes it when a hook arrives, and the setup TUI reads it to decide
// what to report. Kept in the database it was invisible to the TUI, which
// then said "installed but not trusted" for ever — including to people whose
// hooks were trusted and working.
const evidenceFile = "codex-hooks-seen"

// stateDir is where helios keeps its own files. Set by whoever registers the
// provider, since this package cannot import the daemon that owns the path.
var stateDir atomic.Pointer[string]

// SetStateDir tells the provider where to record that hooks are running.
func SetStateDir(dir string) { stateDir.Store(&dir) }

func evidencePath() string {
	dir := stateDir.Load()
	if dir == nil || *dir == "" {
		return ""
	}
	return filepath.Join(*dir, evidenceFile)
}

// hookEvidence records that a Codex hook reached the daemon.
//
// It is the only signal that hooks are trusted, since Codex neither exposes
// trust state nor complains when it skips them.
//
// Guarded: every inbound hook writes it, from its own goroutine, while the
// health endpoint reads it. The test suite never delivers two hooks at once,
// so the race detector would not have caught this.
var hookEvidence struct {
	mu   sync.Mutex
	last time.Time
}

// NoteHookReceivedFor records an inbound hook for a provider. Only Codex needs
// the evidence, so anything else is ignored.
func NoteHookReceivedFor(providerID string) {
	if providerID != "codex" {
		return
	}
	now := time.Now()
	hookEvidence.mu.Lock()
	hookEvidence.last = now
	hookEvidence.mu.Unlock()

	path := evidencePath()
	if path == "" {
		return
	}
	stamp := []byte(now.UTC().Format(time.RFC3339))
	if err := os.WriteFile(path, stamp, 0o644); err != nil {
		log.Printf("codex: record hook evidence: %v", err)
	}
}

// hookEvidenceTTL is how long a received hook vouches for the install. Long
// enough to span an idle session, short enough that a revoked trust surfaces.
const hookEvidenceTTL = 24 * time.Hour

func hooksSeenRecently() bool {
	hookEvidence.mu.Lock()
	last := hookEvidence.last
	hookEvidence.mu.Unlock()

	if last.IsZero() {
		if path := evidencePath(); path != "" {
			if raw, err := os.ReadFile(path); err == nil {
				last, _ = time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
			}
		}
	}
	return !last.IsZero() && time.Since(last) < hookEvidenceTTL
}

// RemoveHooks deletes helios's entries and leaves everything else alone.
//
// Not os.Remove: the file may hold hooks the user wrote, and uninstalling
// helios is not permission to delete them.
func (p *Provider) RemoveHooks() error {
	path := hooksPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read hooks: %w", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse hooks at %s: %w", path, err)
	}
	events, _ := cfg["hooks"].(map[string]interface{})
	ours, _ := hookConfig(p.port)["hooks"].(map[string]interface{})
	for event := range ours {
		delete(events, event)
	}
	if len(events) == 0 {
		// Nothing of anyone's left; the file has no reason to exist.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove hooks: %w", err)
		}
		return nil
	}
	cfg["hooks"] = events
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hooks: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// sendKeyWhenReady sends a key, waits, and sends it again if the screen has
// not changed.
//
// Codex drops a keystroke that arrives while a dialog is still painting. One
// retry is enough in practice and costs nothing when the first key lands: a
// second Return at an ordinary prompt submits an empty line.
func sendKeyWhenReady(sessionID string, key backend.Key) error {
	before, _ := terminalBackend.Capture(sessionID)
	if err := terminalBackend.SendKey(sessionID, key); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	after, err := terminalBackend.Capture(sessionID)
	if err != nil || !strings.EqualFold(before, after) {
		return nil
	}
	return terminalBackend.SendKey(sessionID, key)
}

// resetHookEvidence forgets that any hook has arrived. For tests.
func resetHookEvidence() {
	hookEvidence.mu.Lock()
	defer hookEvidence.mu.Unlock()
	hookEvidence.last = time.Time{}
	if path := evidencePath(); path != "" {
		os.Remove(path)
	}
}
