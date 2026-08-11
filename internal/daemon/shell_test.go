package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wrapper is what puts an agent the user starts by hand into a helios
// terminal, so every invocation has to go through it — including one started
// from inside a helios terminal, which is a session of its own.
func TestShellWrapperSnippet_AlwaysDelegatesToWrap(t *testing.T) {
	for _, syntax := range []string{"posix", "fish"} {
		snippet := ShellWrapperSnippet(syntax)
		if !strings.Contains(snippet, "helios wrap -- claude") {
			t.Errorf("%s snippet does not delegate to wrap:\n%s", syntax, snippet)
		}
		if strings.Contains(snippet, "HELIOS_SESSION_ID") {
			t.Errorf("%s snippet second-guesses wrap's own guard:\n%s", syntax, snippet)
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

// Arguments have to survive the hop through the function, or `claude --resume
// <id>` would start a fresh session instead of the one asked for.
func TestShellWrapperSnippet_PosixForwardsArguments(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "helios")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write helios stub: %v", err)
	}

	cmd := exec.Command(sh, "-c", ShellWrapperSnippet("posix")+"\nclaude --resume sess-1 --model opus")
	cmd.Env = append(os.Environ(), "PATH="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run wrapper: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "wrap -- claude --resume sess-1 --model opus" {
		t.Errorf("wrapper ran %q, want the arguments forwarded", got)
	}
}
