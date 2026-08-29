package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
)

// The snippet is generated from the registry, so these tests need the
// compiled-in providers in it. TestMain, not a call per test: registration is
// once per process either way.
func TestMain(m *testing.M) {
	RegisterDefaultProviders()
	os.Exit(m.Run())
}

// The wrapper is what puts an agent the user starts by hand into a helios
// terminal, so every invocation has to go through it — including one started
// from inside a helios terminal, which is a session of its own.
func TestShellWrapperSnippet_AlwaysDelegatesToWrap(t *testing.T) {
	for _, syntax := range []string{"posix", "fish"} {
		snippet := ShellWrapperSnippet(syntax)
		for _, cmd := range wrappedCommands() {
			if !strings.Contains(snippet, "helios wrap -- "+cmd) {
				t.Errorf("%s snippet does not delegate %s to wrap:\n%s", syntax, cmd, snippet)
			}
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

// Every agent Helios can drive has to be wrapped, or a session started by hand
// runs outside Helios: no terminal to attach to, and waking it later starts a
// second agent on the same conversation.
func TestShellWrapperSnippet_CoversEveryProvider(t *testing.T) {
	snippet := ShellWrapperSnippet("posix")
	for _, info := range provider.Infos() {
		if info.Command == "" {
			t.Errorf("provider %q declares no command to wrap", info.ID)
			continue
		}
		if !strings.Contains(snippet, info.Command+"() {") {
			t.Errorf("%s is not wrapped:\n%s", info.Command, snippet)
		}
	}
}

// A block the user commented out defines nothing, and reporting it as
// installed left every agent on the machine running unwrapped while setup
// showed a tick.
func TestShellWrapperInstalled_IgnoresACommentedBlock(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.zshrc")
	if err := os.WriteFile(live, []byte("export FOO=1\n"+ShellWrapperSnippet("posix")+"\n"), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}
	commented := filepath.Join(dir, "commented.zshrc")
	var out strings.Builder
	for _, line := range strings.Split(ShellWrapperSnippet("posix"), "\n") {
		out.WriteString("# " + line + "\n")
	}
	if err := os.WriteFile(commented, []byte(out.String()), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}

	if !ShellWrapperInstalled(ShellInfo{Name: "zsh", RCPath: live, Syntax: "posix"}) {
		t.Error("a live wrapper was reported as not installed")
	}
	if ShellWrapperInstalled(ShellInfo{Name: "zsh", RCPath: commented, Syntax: "posix"}) {
		t.Error("a commented-out wrapper was reported as installed")
	}
}

// Removal has to take the wrapper that runs, not a dead copy of it that
// happens to appear first in the file.
func TestRemoveShellWrapper_TakesTheLiveBlock(t *testing.T) {
	snippet := ShellWrapperSnippet("posix")
	var commented strings.Builder
	for _, line := range strings.Split(snippet, "\n") {
		commented.WriteString("# " + line + "\n")
	}

	path := filepath.Join(t.TempDir(), ".zshrc")
	rc := "export FOO=1\n\n" + commented.String() + "\n" + snippet + "\n"
	if err := os.WriteFile(path, []byte(rc), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}

	info := ShellInfo{Name: "zsh", RCPath: path, Syntax: "posix"}
	if err := RemoveShellWrapper(info); err != nil {
		t.Fatalf("remove: %v", err)
	}

	left, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rc: %v", err)
	}
	if ShellWrapperInstalled(info) {
		t.Errorf("the live wrapper survived removal:\n%s", left)
	}
	if !strings.Contains(string(left), "# "+shellMarkerStart) {
		t.Errorf("the commented block was taken instead:\n%s", left)
	}
}
