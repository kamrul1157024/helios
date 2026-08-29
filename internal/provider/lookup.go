package provider

import (
	"os"
	"os/exec"
	"strings"
)

// LookAgent finds an agent's binary and reports whether it exists.
//
// A bare exec.LookPath is not enough. The daemon and the TUI both run in
// contexts that may not carry the user's interactive PATH — a launchd or
// systemd service, a login shell that exports ~/.local/bin only from an rc
// file the process never sourced — and the answer there is "not installed"
// for an agent the user can run by hand. So a failed lookup falls back to
// asking a login shell, which is where that PATH is set.
//
// Returns the resolved path, or the bare name when nothing was found. The
// bare name is deliberate for the launch path: exec resolves it again at call
// time, by which point the environment may be right. Callers that need to
// know whether it is really there read the second value.
func LookAgent(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil && p != "" {
		return p, true
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	// -l so the shell reads the profile that sets the user's PATH. `command
	// -v` rather than `which`: it is a builtin, so it works in a shell where
	// which is absent, and it does not resolve aliases to something unrunnable.
	out, err := exec.Command(shell, "-l", "-c", "command -v "+name).Output()
	if err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() {
				return p, true
			}
		}
	}
	return name, false
}

// AvailableFor reports whether a provider's agent is present on this machine.
//
// The provider answers, because it owns how its binary is found. A provider
// that does not implement Availability is assumed present: it has no CLI to
// look for, or does not care.
func AvailableFor(id string) bool {
	if a, ok := Get(id); ok {
		if av, isAv := a.(Availability); isAv {
			return av.Available()
		}
	}
	return true
}

// Availability reports whether the provider's agent CLI is installed.
type Availability interface {
	Available() bool
}
