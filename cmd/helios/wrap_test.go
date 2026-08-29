package main

import (
	"testing"

	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/provider"
)

// The shell wrapper is generated from the registry, and wrapProvider matches
// names by hand — it runs on every `claude` a user types and cannot afford to
// build providers just to look one up. So the two lists have to be checked
// against each other somewhere, and here is cheaper than in a terminal.
func TestEveryWrappedCommandIsRecognised(t *testing.T) {
	daemon.RegisterDefaultProviders()

	for _, info := range provider.Infos() {
		if info.Command == "" {
			t.Errorf("provider %q declares no command, so the wrapper cannot cover it", info.ID)
			continue
		}
		if got := wrapProvider(info.Command); got != info.ID {
			t.Errorf("wrapProvider(%q) = %q, want %q — a wrapped session would be hosted but never registered",
				info.Command, got, info.ID)
		}
	}
}
