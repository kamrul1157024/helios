package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
)

// A bare exec.LookPath answers "not installed" for an agent the user can run
// by hand, whenever the process did not inherit the interactive PATH — a
// service, or a shell that exports ~/.local/bin from an rc file this process
// never sourced. The fallback asks a login shell, which is where that PATH is.
func TestLookAgentFallsBackToALoginShell(t *testing.T) {
	provider.ForgetLookups()
	t.Cleanup(provider.ForgetLookups)

	dir := t.TempDir()
	bin := filepath.Join(dir, "pretend-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	// A PATH without the stub, so LookPath must fail...
	t.Setenv("PATH", "/usr/bin:/bin")
	if _, found := provider.LookAgent("pretend-agent"); found {
		t.Fatal("found an agent that is not on PATH")
	}

	// A different system state, so the cached "absent" from above no longer
	// describes it. Real callers get the same effect from the miss TTL.
	provider.ForgetLookups()

	// ...and a login shell that puts it back, which is the case this exists for.
	rc := filepath.Join(dir, "rc.sh")
	if err := os.WriteFile(rc, []byte("export PATH="+dir+":$PATH\n"), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}
	shell := filepath.Join(dir, "loginshell")
	script := "#!/bin/sh\n. " + rc + "\nshift 2>/dev/null\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	t.Setenv("SHELL", shell)

	path, found := provider.LookAgent("pretend-agent")
	if !found {
		t.Fatalf("login-shell fallback did not find the agent; got %q", path)
	}
	if path != bin {
		t.Errorf("path = %q, want %q", path, bin)
	}
}

// A missing agent returns the bare name, so the launch path can still try to
// resolve it later, and reports false so the UI can say it is absent.
func TestLookAgentReportsAMissingAgent(t *testing.T) {
	provider.ForgetLookups()
	t.Cleanup(provider.ForgetLookups)
	t.Setenv("PATH", "/nonexistent")
	t.Setenv("SHELL", "/bin/sh")
	path, found := provider.LookAgent("definitely-not-an-agent")
	if found {
		t.Error("reported an agent that does not exist")
	}
	if path != "definitely-not-an-agent" {
		t.Errorf("path = %q, want the bare name", path)
	}
}

// The fallback spawns a login shell, which is slow enough to matter when
// ReadinessFor runs on every API request. Repeated lookups must not repeat it.
func TestLookAgentCachesTheExpensivePath(t *testing.T) {
	provider.ForgetLookups()
	t.Cleanup(provider.ForgetLookups)

	dir := t.TempDir()
	// A "login shell" that records every time it is asked.
	calls := filepath.Join(dir, "calls")
	shell := filepath.Join(dir, "sh")
	script := "#!/bin/sh\necho x >> " + calls + "\nexit 1\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/nonexistent")

	for i := 0; i < 5; i++ {
		provider.LookAgent("some-absent-agent")
	}

	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the fallback never ran: %v", err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != 1 {
		t.Errorf("spawned %d login shells for 5 lookups, want 1", n)
	}
}
