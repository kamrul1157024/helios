package provider

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
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
	if path, found, fresh := cachedLookup(name); fresh {
		return path, found
	}
	path, found := lookAgentUncached(name)
	rememberLookup(name, path, found)
	return path, found
}

// lookupCache keeps the answer for a while.
//
// The fallback spawns a login shell, and that is not free: a zsh profile with
// a plugin manager in it takes a second or more. ReadinessFor runs on every
// /api/providers request, and a machine with several paired devices polling
// turned one lookup into a steady stream of shell startups — enough to make
// the daemon slow to answer anything, including the pairing request the setup
// screen waits on.
//
// Positive answers are held longer than negative ones: an installed agent
// rarely disappears, while an absent one may be installed at any moment and
// should be noticed soon after.
var lookupCache struct {
	sync.Mutex
	entries map[string]lookupEntry
}

type lookupEntry struct {
	path    string
	found   bool
	checked time.Time
}

const (
	lookupFoundTTL   = 5 * time.Minute
	lookupMissingTTL = 15 * time.Second
)

func cachedLookup(name string) (path string, found, fresh bool) {
	lookupCache.Lock()
	defer lookupCache.Unlock()
	e, ok := lookupCache.entries[name]
	if !ok {
		return "", false, false
	}
	ttl := lookupMissingTTL
	if e.found {
		ttl = lookupFoundTTL
	}
	if time.Since(e.checked) > ttl {
		return "", false, false
	}
	return e.path, e.found, true
}

func rememberLookup(name, path string, found bool) {
	lookupCache.Lock()
	defer lookupCache.Unlock()
	if lookupCache.entries == nil {
		lookupCache.entries = map[string]lookupEntry{}
	}
	lookupCache.entries[name] = lookupEntry{path: path, found: found, checked: time.Now()}
}

// ForgetLookups drops the cache, so the next call asks the system again.
func ForgetLookups() {
	lookupCache.Lock()
	defer lookupCache.Unlock()
	lookupCache.entries = nil
}

func lookAgentUncached(name string) (string, bool) {
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
	a, known := Get(id)
	if !known {
		// Not registered is not available. Defaulting to true here let an
		// unknown id report ready, which is exactly the wrong direction for
		// something that gates the session picker.
		return false
	}
	if av, isAv := a.(Availability); isAv {
		return av.Available()
	}
	// Registered, with no opinion about its own binary: nothing to look for.
	return true
}

// Availability reports whether the provider's agent CLI is installed.
type Availability interface {
	Available() bool
}

// Readiness is whether a provider can actually start a session, and what is
// missing when it cannot.
//
// Separate from Availability because the agent being installed is only half
// of it: without its hooks the daemon never hears from the session, which
// looks to the user like a session that starts and then does nothing.
type Readiness struct {
	// Ready is whether a session started now would work end to end.
	Ready bool `json:"ready"`
	// Blocker names what is missing, in one line, for a client to show.
	Blocker string `json:"blocker,omitempty"`
	// Hint is the command or action that fixes it.
	Hint string `json:"hint,omitempty"`
}

// ReadinessFor reports whether a provider can start a session.
//
// Derived, not declared: the agent has to be installed, and its hooks have to
// be written and current. A provider with no installer is judged on the agent
// alone — it has nothing else to get wrong.
func ReadinessFor(id string) Readiness {
	if _, known := Get(id); !known {
		return Readiness{Blocker: "unknown provider", Hint: "check the provider id"}
	}
	if !AvailableFor(id) {
		return Readiness{
			Blocker: "the agent is not installed",
			Hint:    installHint(id),
		}
	}
	inst := InstallerFor(id)
	if inst == nil {
		return Readiness{Ready: true}
	}
	h := inst.HookHealth()
	switch {
	case !h.Installed:
		return Readiness{Blocker: "hooks are not installed", Hint: "helios hooks install"}
	case !h.Current:
		return Readiness{Blocker: "hooks are out of date", Hint: "helios hooks install"}
	}
	// Deliberately ready even when the agent is not yet running the hooks.
	// That cannot be known until a session sends one, so treating it as a
	// blocker would make a correctly configured provider permanently
	// unstartable. The health check reports it separately.
	return Readiness{Ready: true, Blocker: notEffectiveBlocker(h), Hint: h.Detail}
}

// notEffectiveBlocker reports a provider that is configured but unproven, as a
// caveat rather than a blocker.
func notEffectiveBlocker(h HookHealth) string {
	if h.Effective {
		return ""
	}
	return "hooks installed but not yet seen running"
}

// installHint is how a user gets an agent they do not have. Providers do not
// declare it: it is packaging, and it changes independently of the code here.
func installHint(id string) string {
	switch id {
	case "claude":
		return "npm i -g @anthropic-ai/claude-code"
	case "codex":
		return "npm i -g @openai/codex"
	default:
		return "see the agent's own installation docs"
	}
}
