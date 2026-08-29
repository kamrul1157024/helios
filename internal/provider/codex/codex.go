// Package codex drives the OpenAI Codex CLI.
//
// Codex ships a hook engine with the same three-level shape as Claude's and a
// PermissionRequest that answers with the JSON Helios already writes, so most
// of this package is plumbing. Four differences drive what is here, all
// measured against codex-cli 0.150.1 and recorded in
// docs/specs/46-codex-provider.md:
//
//   - no HTTP handler type, so every hook is a command hook piping curl
//   - no --session-id, so Codex mints the id and Helios learns it from a hook
//   - PermissionRequest cannot write a rule, so there is no "don't ask again"
//   - two permission axes, and the permissive one silences the hook
package codex

import (
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/provider"
)

// Provider drives the Codex CLI.
type Provider struct {
	bin  string
	port int
}

// New returns the Codex provider. internalPort is the daemon's own port, which
// the hook table curls.
func New(internalPort int) *Provider {
	return &Provider{bin: findCodex(), port: internalPort}
}

func (p *Provider) Info() provider.Info {
	return provider.Info{
		ID:   "codex",
		Name: "OpenAI Codex",
		Icon: "smart_toy",
		Kind: provider.KindNative,
	}
}

// findCodex locates the codex binary, mirroring findClaude. The daemon runs in
// a non-interactive context that may not have the user's full PATH, so we fall
// back to a login shell lookup.
func findCodex() string {
	if p, err := exec.LookPath("codex"); err == nil && p != "" {
		return p
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	out, err := exec.Command(shell, "-l", "-c", "which codex").Output()
	if err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() {
				return p
			}
		}
	}
	return "codex"
}

// ==================== Permission modes ====================

// Codex has two axes where Claude has one: --sandbox and --ask-for-approval.
// These four names are the pairs worth offering, least to most trusting.
const (
	ModeReadOnly       = "read-only"
	ModeWorkspaceWrite = "workspace-write"
	ModeFullAuto       = "full-auto"
	ModeBypass         = "bypass"
)

// DefaultPermissionMode is what a Helios-started Codex session runs under.
//
// It is deliberately the opposite of Claude's choice. Claude's permissive mode
// keeps the phone useful, because its hook still fires and Helios answers.
// Codex's PermissionRequest hook fires *only* when Codex would ask a human, so
// --ask-for-approval never silences it and the phone goes dead. workspace-write
// with on-request asks more often and stays answerable from anywhere.
const DefaultPermissionMode = ModeWorkspaceWrite

var permissionModes = []string{ModeReadOnly, ModeWorkspaceWrite, ModeFullAuto, ModeBypass}

func (p *Provider) PermissionModes() []string { return permissionModes }

func (p *Provider) ValidMode(mode string) bool { return slices.Contains(permissionModes, mode) }

// modeArgs renders a mode as the flag pair it means.
//
// Ordering matters and is not obvious: -s and -a are global flags that must
// precede a subcommand, while some others must follow it. Getting it wrong is
// a hard argument error, not a warning.
func modeArgs(mode string) []string {
	switch mode {
	case ModeReadOnly:
		return []string{"-s", "read-only", "-a", "on-request"}
	case ModeFullAuto:
		return []string{"-s", "workspace-write", "-a", "never"}
	case ModeBypass:
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	default:
		return []string{"-s", "workspace-write", "-a", "on-request"}
	}
}

// launchMode reports the mode a spec resolves to, so the daemon records what
// the session is actually running in rather than inferring it later.
func launchMode(spec provider.SessionSpec) string {
	switch {
	case spec.SkipPermissions:
		return ModeBypass
	case slices.Contains(permissionModes, spec.PermissionMode):
		return spec.PermissionMode
	default:
		return DefaultPermissionMode
	}
}

// ==================== Launch ====================

// HeliosSessionEnv carries Helios's own session id into the agent's
// environment.
//
// This is the whole answer to Codex having no --session-id. Codex hooks
// inherit the session's environment, so the curl in the hook table sends this
// as a header and the daemon can tell which of its own sessions is calling.
// Without it a hook would arrive carrying only the id Codex minted, which
// Helios has never seen.
const HeliosSessionEnv = "HELIOS_SESSION"

func (p *Provider) Launch(spec provider.SessionSpec) (provider.Launch, error) {
	mode := launchMode(spec)
	argv := []string{p.bin}
	argv = append(argv, modeArgs(mode)...)
	if spec.Model != "" {
		argv = append(argv, "-m", spec.Model)
	}
	// Last, and positional: anything after it would be read as more prompt.
	if spec.Prompt != "" {
		argv = append(argv, spec.Prompt)
	}
	return provider.Launch{
		Argv: argv,
		Env:  map[string]string{HeliosSessionEnv: spec.SessionID},
		Mode: mode,
	}, nil
}

// Resume wakes a session by the id Codex minted, which is what resumeID holds.
//
// An empty resumeID means the session-start hook never reported in, so there
// is nothing to resume by. Returning empty argv is the honest answer: the
// caller falls back rather than starting a fresh conversation that pretends to
// be the old one.
func (p *Provider) Resume(sessionID, resumeID, mode string) (provider.Launch, error) {
	if resumeID == "" {
		return provider.Launch{}, nil
	}
	if !slices.Contains(permissionModes, mode) {
		mode = DefaultPermissionMode
	}
	argv := []string{p.bin}
	argv = append(argv, modeArgs(mode)...)
	argv = append(argv, "resume", resumeID)
	return provider.Launch{
		Argv: argv,
		Env:  map[string]string{HeliosSessionEnv: sessionID},
		Mode: mode,
	}, nil
}

// ==================== Models ====================

func (p *Provider) Models() ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{
		{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Description: "Default Codex model"},
		{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Description: "Larger context"},
	}, nil
}

func (p *Provider) Commands() []provider.Command {
	return []provider.Command{
		{Name: "/status", Description: "Show session status", Icon: "info"},
		{Name: "/model", Description: "Switch model", Icon: "swap_horiz"},
		{Name: "/permissions", Description: "Change approval and sandbox", Icon: "lock"},
		{Name: "/hooks", Description: "Review and trust hooks", Icon: "verified_user"},
		{Name: "/rollout", Description: "Print the transcript path", Icon: "description"},
		{Name: "/compact", Description: "Compact conversation context", Icon: "compress"},
	}
}

func (p *Provider) EventTypes() []provider.EventTypeInfo {
	return []provider.EventTypeInfo{
		{Type: "tool_pre", Label: "Tool Started", Description: "A tool is about to run", Category: "tools"},
		{Type: "tool_post", Label: "Tool Completed", Description: "A tool finished", Category: "tools"},
		{Type: "prompt_submit", Label: "Prompt Submitted", Description: "User sent a new prompt", Category: "actions"},
		{Type: "permission", Label: "Permission Needed", Description: "Waiting for approval", Category: "actions"},
		{Type: "stop", Label: "Turn Finished", Description: "The agent finished a turn", Category: "lifecycle"},
		{Type: "session_start", Label: "Session Started", Description: "A new session began", Category: "lifecycle"},
		{Type: "session_end", Label: "Session Ended", Description: "Session was closed", Category: "lifecycle"},
		{Type: "compact_pre", Label: "Compacting", Description: "Context compaction is starting", Category: "context"},
		{Type: "compact_post", Label: "Compacted", Description: "Context compaction finished", Category: "context"},
		{Type: "subagent_start", Label: "Subagent Started", Description: "A subagent was spawned", Category: "subagents"},
		{Type: "subagent_stop", Label: "Subagent Stopped", Description: "A subagent finished", Category: "subagents"},
	}
}

// ==================== Screen watching ====================

// trustPromptPatterns are phrases from Codex's own directory-trust dialog.
//
// It is a different dialog from Claude's, worded differently, and Helios sees
// neither through a hook: the agent is blocked before it reports in. Measured
// wording, 0.150.1:
//
//	Do you trust the contents of this directory?
//	› 1. Yes, continue    2. No, quit
var trustPromptPatterns = []string{
	"trust the contents of this directory",
	"do you trust the contents",
}

func (p *Provider) MatchScreen(screen string) *provider.ScreenPrompt {
	lower := strings.ToLower(screen)
	for _, pattern := range trustPromptPatterns {
		if strings.Contains(lower, pattern) {
			return &provider.ScreenPrompt{
				Type:   "codex.trust",
				Title:  "Directory trust required",
				Detail: "Codex is asking to trust this directory before it can run.",
			}
		}
	}
	return nil
}

// ==================== Prompt queue ====================

// terminalBackend is set by the daemon once shared state exists.
var terminalBackend backend.Backend

// SetBackend gives the action handlers access to session terminals.
func SetBackend(b backend.Backend) { terminalBackend = b }

func (p *Provider) SetBackend(b backend.Backend) { SetBackend(b) }

// QueuePrompt types the prompt into the session's terminal, which Codex holds
// until the current turn ends — the same behaviour Claude has.
//
// There is also `codex queue --thread <id> --message`, which does it out of
// band. It is not used: it needs the id Codex minted, so it fails on a session
// that has not reported in yet, and it buys nothing over the PTY path that
// already works. Recorded here so the subcommand's existence does not get
// mistaken for a reason to switch.
func (p *Provider) QueuePrompt(sessionID, resumeID, text string) error {
	if terminalBackend == nil {
		return errNoTerminal
	}
	return terminalBackend.SendText(sessionID, text)
}
