package claude

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/kamrul1157024/helios/internal/provider"
)

// findClaude locates the claude binary, mirroring the findTmux pattern.
// The daemon runs in a non-interactive context that may not have the user's
// full PATH, so we fall back to a login shell lookup if LookPath fails.
func findClaude() string {
	path, _ := provider.LookAgent("claude")
	return path
}

// DefaultPermissionMode is the permission mode every Helios-started Claude
// session gets unless something asks for another one.
//
// Helios sessions are driven from a phone as often as from a terminal, and a
// prompt that stops on the first permission question is a session the user
// cannot finish from the lock screen. "auto" still asks about the dangerous
// things; it just stops asking about the routine ones.
//
// It is also applied on resume (see cmd/helios/ptyhost.go): the mode is a
// per-invocation flag, not conversation state, so a session that went cold
// would silently come back in the CLI's own default without it.
const DefaultPermissionMode = "auto"

// PermissionModes are the values `claude --permission-mode` accepts, in the
// order they are worth offering: least to most trusting, with the two that
// need a warning last.
//
// Taken from the CLI's own choices list. Note that Helios can set any of them,
// including dontAsk, because it switches by restarting the session — the
// interactive Shift+Tab cycle reaches neither dontAsk nor, in some sessions,
// auto and bypassPermissions.
var PermissionModes = []string{
	"plan",
	"manual",
	"acceptEdits",
	"auto",
	"dontAsk",
	"bypassPermissions",
}

// ValidPermissionMode reports whether mode is one the CLI would accept.
// Passing an unknown one is not a soft failure: claude rejects it and the
// session never starts.
func ValidPermissionMode(mode string) bool {
	return slices.Contains(PermissionModes, mode)
}

// ResumeArgs returns the argv that brings an existing session back.
//
// The permission mode has to be repeated on every resume: it is a
// per-invocation flag, not conversation state, so a woken session would
// otherwise come back in the CLI's default and quietly drop whatever mode the
// user had chosen. An unrecognised mode means DefaultPermissionMode.
//
// An empty mode is different from an unrecognised one: it means Helios never
// chose a mode for this session, which is the case for one the user started
// themselves — `helios wrap -- claude`, or a session discovered from a
// transcript. Those launched in whatever default the CLI's own settings give
// them, so the flag is omitted here and they wake the way they started.
// Sending DefaultPermissionMode instead would silently escalate a session the
// user never asked to be permissive.
func ResumeArgs(sessionID, mode string) []string {
	argv := []string{findClaude(), "--resume", sessionID}
	if mode == "" {
		return argv
	}
	if !ValidPermissionMode(mode) {
		mode = DefaultPermissionMode
	}
	return append(argv, "--permission-mode", mode)
}

// LaunchPermissionMode reports the mode sessionArgs launches spec under, so a
// caller can record what the session is actually running in rather than
// inferring it later. Recording matters because an unrecorded mode means "the
// CLI's own default" to ResumeArgs.
//
// SkipPermissions maps to bypassPermissions: --dangerously-skip-permissions is
// a launch-only flag with no resume equivalent, and bypassPermissions is the
// mode that keeps the user's "stop asking me" across a wake. It is not the same
// flag — it runs the hook chain, which is what keeps the session visible to
// Helios at all — but it is the closest resumable form of the same request.
func LaunchPermissionMode(spec provider.SessionSpec) string {
	switch {
	case spec.SkipPermissions:
		return "bypassPermissions"
	case ValidPermissionMode(spec.PermissionMode):
		return spec.PermissionMode
	default:
		return DefaultPermissionMode
	}
}

// mcpPort is the daemon's internal port, set once by Register. Sessions reach
// the Helios MCP server there. Zero means no port was reported, and then no MCP
// config is injected at all.
var mcpPort int

// setMCPPort records where sessions reach the Helios MCP server. Zero means
// no MCP config is injected at all.
func setMCPPort(port int) { mcpPort = port }

// mcpConfig builds the --mcp-config value that points a session at the Helios
// MCP server and names the session it belongs to. The id travels in a header,
// so the agent never has to know its own id or pass it to a tool.
//
// This must never be paired with --strict-mcp-config. Measured 2026-08-24:
// --mcp-config on its own merges with the user's servers, while adding
// --strict-mcp-config replaces them. That would silently remove every other
// MCP server from every Helios session.
func mcpConfig(sessionID string) (string, bool) {
	if mcpPort == 0 || sessionID == "" {
		return "", false
	}
	encoded, err := json.Marshal(map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"helios": map[string]interface{}{
				"type":    "http",
				"url":     fmt.Sprintf("http://127.0.0.1:%d/mcp", mcpPort),
				"headers": map[string]string{"X-Helios-Session": sessionID},
			},
		},
	})
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// sessionArgs builds the argv for a Claude session. Shared with the resume
// path so a woken session gets the same flags as a fresh one.
func sessionArgs(spec provider.SessionSpec) []string {
	argv := []string{"claude"}
	if spec.SessionID != "" {
		argv = append(argv, "--session-id", spec.SessionID)
	}
	if config, ok := mcpConfig(spec.SessionID); ok {
		argv = append(argv, "--mcp-config", config)
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	// --dangerously-skip-permissions is not a permission mode: it bypasses the
	// hook chain the modes run through, so passing both is contradictory.
	if spec.SkipPermissions {
		argv = append(argv, "--dangerously-skip-permissions")
	} else {
		argv = append(argv, "--permission-mode", LaunchPermissionMode(spec))
	}
	// Last, and positional: anything after it would be read as more prompt.
	if spec.Prompt != "" {
		argv = append(argv, spec.Prompt)
	}
	return argv
}
