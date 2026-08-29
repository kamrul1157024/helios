package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// skipFile records the agents a user has said they do not want set up.
//
// A file rather than a settings row, because the answer is needed by the
// setup TUI, which runs in its own process and must work whether or not the
// daemon is up. Nothing else reads it: skipping is about what setup asks, not
// about what the daemon can do.
func skipFile() string { return filepath.Join(HeliosDir(), "skipped-agents.json") }

// SkippedProviders returns the agents the user has chosen not to set up.
func SkippedProviders() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(skipFile())
	if err != nil {
		return out
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return out
	}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// SetProviderSkipped records, or clears, a user's decision to leave an agent
// alone.
//
// Skipping affects setup only. It does not hide a provider that is already
// working: someone who skips the prompt and later installs the hooks by hand
// should still be offered the agent, so the flag is not consulted anywhere a
// session is started.
func SetProviderSkipped(id string, skipped bool) error {
	current := SkippedProviders()
	if skipped {
		current[id] = true
	} else {
		delete(current, id)
	}

	ids := make([]string, 0, len(current))
	for k := range current {
		ids = append(ids, k)
	}
	sort.Strings(ids)

	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(HeliosDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(skipFile(), data, 0o644)
}
