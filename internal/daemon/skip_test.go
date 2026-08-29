package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// Skipping has to survive the process, or setup asks again next start —
// which is the whole thing it exists to stop.
func TestSkipPersistsAndReverses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := SkippedProviders(); len(got) != 0 {
		t.Fatalf("a fresh machine has skips: %v", got)
	}
	if err := SetProviderSkipped("codex", true); err != nil {
		t.Fatalf("skip: %v", err)
	}
	if !SkippedProviders()["codex"] {
		t.Error("skip did not persist")
	}
	if SkippedProviders()["claude"] {
		t.Error("skipping one agent skipped another")
	}

	// Reversible with the same key that set it, or a mis-press is permanent.
	if err := SetProviderSkipped("codex", false); err != nil {
		t.Fatalf("unskip: %v", err)
	}
	if SkippedProviders()["codex"] {
		t.Error("unskip did not take")
	}
}

// A corrupt or hand-edited file must not stop helios starting. Nothing here
// is important enough to fail a launch over.
func TestSkipToleratesAJunkFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(HeliosDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(HeliosDir(), "skipped-agents.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := SkippedProviders(); len(got) != 0 {
		t.Errorf("junk parsed as skips: %v", got)
	}
	if err := SetProviderSkipped("codex", true); err != nil {
		t.Errorf("could not recover from a junk file: %v", err)
	}
}
