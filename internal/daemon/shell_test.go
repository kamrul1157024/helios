package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wrapper is what turns `claude` into `helios wrap`, and a host types that
// command into a login shell which loads this very file — so without the guard
// the wrap re-enters the session it runs inside and loops.
func TestShellWrapperSnippet_SkipsWrapInsideAHostedSession(t *testing.T) {
	for _, syntax := range []string{"posix", "fish"} {
		snippet := ShellWrapperSnippet(syntax)
		if !strings.Contains(snippet, "HELIOS_SESSION_ID") {
			t.Errorf("%s snippet does not guard on HELIOS_SESSION_ID:\n%s", syntax, snippet)
		}
		if !strings.Contains(snippet, "command claude") {
			t.Errorf("%s snippet has no unwrapped fallback:\n%s", syntax, snippet)
		}
	}
}

func TestShellWrapperSnippet_UnknownSyntaxHasNoSnippet(t *testing.T) {
	if got := ShellWrapperSnippet("elvish"); got != "" {
		t.Errorf("snippet = %q, want empty for an unsupported shell", got)
	}
}

// The snippet is appended verbatim to a file the shell sources at startup: a
// syntax error there breaks every new shell the user opens.
func TestShellWrapperSnippet_PosixParses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	path := filepath.Join(t.TempDir(), "rc.sh")
	if err := os.WriteFile(path, []byte(ShellWrapperSnippet("posix")), 0o644); err != nil {
		t.Fatalf("write snippet: %v", err)
	}
	if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
		t.Errorf("sh -n rejected the snippet: %v\n%s", err, out)
	}
}

// The guard has to hold for the real function, not just for the text of it.
func TestShellWrapperSnippet_PosixGuardDispatches(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	dir := t.TempDir()
	// Stand-ins for the two branches: whichever runs announces itself.
	for name, body := range map[string]string{
		"claude": "#!/bin/sh\necho ran-claude\n",
		"helios": "#!/bin/sh\necho ran-wrap\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	run := func(sessionID string) string {
		cmd := exec.Command(sh, "-c", ShellWrapperSnippet("posix")+"\nclaude")
		cmd.Env = append(os.Environ(), "PATH="+dir, "HELIOS_SESSION_ID="+sessionID)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run wrapper (session %q): %v\n%s", sessionID, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if got := run(""); got != "ran-wrap" {
		t.Errorf("outside a session: got %q, want ran-wrap", got)
	}
	if got := run("sess-1"); got != "ran-claude" {
		t.Errorf("inside a session: got %q, want ran-claude", got)
	}
}
