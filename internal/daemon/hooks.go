package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kamrul1157024/helios/internal/provider"
)

// Hook installation is per provider: each agent has its own file, its own
// format, and its own idea of when a hook counts as installed. The daemon
// iterates; it knows none of that.

// InstallHooks writes every registered provider's hook table.
//
// One provider failing does not stop the others. A machine with Claude but not
// Codex should still end up with working Claude hooks.
// InstallHooks writes hook tables. An empty only means every provider.
func InstallHooks(local bool, only ...string) error {
	scope := provider.ScopeUser
	if local {
		scope = provider.ScopeProject
	}
	wanted := map[string]bool{}
	for _, id := range only {
		wanted[id] = true
	}
	var errs []error
	for _, p := range provider.All() {
		id := p.Info().ID
		if len(wanted) > 0 && !wanted[id] {
			continue
		}
		inst := provider.InstallerFor(id)
		if inst == nil {
			continue
		}
		if err := inst.InstallHooks(scope); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		fmt.Printf("Hooks installed for %s\n", id)
	}
	return errors.Join(errs...)
}

// HooksHealth reports each provider's hook state, keyed by provider ID.
func HooksHealth() map[string]provider.HookHealth {
	out := map[string]provider.HookHealth{}
	for _, p := range provider.All() {
		id := p.Info().ID
		if inst := provider.InstallerFor(id); inst != nil {
			out[id] = inst.HookHealth()
		}
	}
	return out
}

// HooksOutdated reports whether any provider's installed hooks are present but
// stale.
//
// Missing is not outdated: a provider whose agent is not installed has no
// hooks and needs none, and nagging about it would train the user to ignore
// the warning that matters.
func HooksOutdated() bool {
	for _, h := range HooksHealth() {
		if h.Installed && !h.Current {
			return true
		}
	}
	return false
}

// HooksIneffective returns the providers whose hooks are installed and current
// but that the agent is not running.
//
// This exists because of Codex: it reads an untrusted hook table, declines to
// run it, and reports nothing. The daemon then receives no events and every
// session sits at "starting" with no error anywhere. See
// docs/specs/46-codex-provider.md.
func HooksIneffective() map[string]provider.HookHealth {
	out := map[string]provider.HookHealth{}
	for id, h := range HooksHealth() {
		if h.Installed && h.Current && !h.Effective {
			out[id] = h
		}
	}
	return out
}

// InstallHooksIfMissing installs for any provider that has no hooks at all.
func InstallHooksIfMissing() {
	for id, h := range HooksHealth() {
		if h.Installed {
			continue
		}
		if inst := provider.InstallerFor(id); inst != nil {
			if err := inst.InstallHooks(provider.ScopeUser); err != nil {
				fmt.Fprintf(os.Stderr, "hooks: install for %s: %v\n", id, err)
			}
		}
	}
}

// ShowHooks prints each provider's hook health.
func ShowHooks() {
	out, err := json.MarshalIndent(HooksHealth(), "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hooks: %v\n", err)
		return
	}
	fmt.Println(string(out))
}

// RemoveHooks removes every registered provider's hook table.
func RemoveHooks() error {
	var errs []error
	for _, p := range provider.All() {
		id := p.Info().ID
		inst := provider.InstallerFor(id)
		if inst == nil {
			continue
		}
		if err := inst.RemoveHooks(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		fmt.Printf("Hooks removed for %s\n", id)
	}
	return errors.Join(errs...)
}
