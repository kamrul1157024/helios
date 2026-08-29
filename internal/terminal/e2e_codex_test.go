package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hasCodex locates the codex binary or skips.
func hasCodex(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed; skipping real-agent e2e")
	}
	return p
}

// isolatedCodexHome builds a CODEX_HOME under dir holding only a copy of the
// developer's credentials.
//
// The copy is what makes the test hermetic in the direction that matters:
// codex writes rollout files, a session index and several sqlite databases
// into CODEX_HOME, and a test must not add rows to the developer's own
// history. Auth is the one thing it has to borrow, because an unauthenticated
// codex exits before the host settles and the socket is torn down again —
// which reads as "socket never appeared" and blames the wrong component.
func isolatedCodexHome(t *testing.T, dir string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory; skipping real-agent e2e")
	}
	auth, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		t.Skip("codex is not logged in; skipping real-agent e2e")
	}
	cx := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(cx, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cx, "auth.json"), auth, 0o600); err != nil {
		t.Fatalf("copy codex auth: %v", err)
	}
	return cx
}

// startCodexHost boots a real codex under a real ptyhost and returns a viewer.
func startCodexHost(t *testing.T, sid string) (*e2eEnv, *Client, *viewerScreen) {
	t.Helper()
	codex := hasCodex(t)
	e := newE2E(t)
	codexHome := isolatedCodexHome(t, e.dir)

	project, err := os.MkdirTemp("/tmp", "cxproj")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(project) })

	cmd := exec.Command(e.binary, "ptyhost", sid,
		"--cwd", project, "--cols", "100", "--rows", "30", "--cmd", codex)
	cmd.Env = append(os.Environ(), "HOME="+e.dir, "CODEX_HOME="+codexHome)
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			p.Signal(syscall.SIGKILL)
		}
	})

	sock := e.socket(sid)
	if !WaitForSocket(sock, 30*time.Second) {
		t.Fatal("ptyhost socket never appeared for codex")
	}

	viewer, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 100, Rows: 30, Name: "desktop"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { viewer.Close() })

	return e, viewer, newViewerScreen(t, viewer, 100, 30)
}

// TestE2ECodexBootsUnderHost is the same load-bearing claim the Claude test
// makes, for the second provider: a real codex boots, renders and answers
// keystrokes through our PTY host.
//
// Reaching the UI proves the emulator answered codex's startup queries. A
// terminal that does not reply to those leaves the TUI hanging on a blank
// screen, which is the failure this test exists to catch.
func TestE2ECodexBootsUnderHost(t *testing.T) {
	_, viewer, view := startCodexHost(t, "e2e-codex")

	if !view.waitForText(t, "OpenAI Codex", 45*time.Second) {
		t.Fatal("codex never rendered its UI under the host")
	}

	// A fresh directory raises codex's own trust dialog. It is the reason
	// Helios needs a per-provider screen watcher: the daemon's patterns are
	// Claude's wording and do not match this one.
	// See docs/specs/46-codex-provider.md.
	if !view.waitForText(t, "Do you trust", 30*time.Second) {
		t.Fatal("codex never reached its trust dialog")
	}

	// Codex paints the dialog before its input loop consumes keys, and a
	// keystroke sent in that window is swallowed with no feedback. Measured:
	// CR immediately after the dialog appears does nothing; CR after a settle
	// dismisses it. Helios's trust action sends exactly this CR
	// (backend/host.go:319), so the daemon must not answer the moment it sees
	// the prompt. See docs/specs/46-codex-provider.md.
	time.Sleep(3 * time.Second)

	// Enter accepts the pre-selected "1. Yes, continue".
	if err := viewer.Send([]byte("\r")); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if !view.waitUntil(t, 20*time.Second, func(text string) bool {
		return !strings.Contains(text, "Do you trust")
	}) {
		t.Error("Enter did not dismiss the trust dialog")
	}
}

// TestE2ECodexTrustDialogEvadesTheDaemonPatterns pins the measurement behind
// the ScreenWatcher capability in docs/specs/47-provider-interface.md.
//
// The daemon's trust watcher matches Claude's wording. Codex's dialog says
// something else, so a Helios-launched codex session in a new directory stalls
// on a modal nobody is told about. If codex ever adopts Claude's phrasing this
// test fails, which is the signal to simplify the watcher rather than a bug.
func TestE2ECodexTrustDialogEvadesTheDaemonPatterns(t *testing.T) {
	_, _, view := startCodexHost(t, "e2e-codex-trust")

	if !view.waitForText(t, "Do you trust", 45*time.Second) {
		t.Fatal("codex never reached its trust dialog")
	}
	screen := strings.ToLower(view.screen.Text())

	// Kept in step with internal/server/trust_watcher.go by the assertion
	// below, not by hand.
	claudePatterns := []string{
		"yes, i trust this folder",
		"quick safety check",
		"one you trust",
		"trust the files in this",
	}
	for _, p := range claudePatterns {
		if strings.Contains(screen, p) {
			t.Errorf("codex now matches Claude's trust pattern %q; the watcher "+
				"can be shared and spec 46 should be updated", p)
		}
	}
	if !strings.Contains(screen, "trust the contents of this directory") {
		t.Errorf("codex trust wording changed; screen was:\n%s", view.screen.Text())
	}
}

// TestE2ECodexOverlayPaintsOverTheTUI is the HITL claim for the second
// provider: Helios's own modal must be readable on top of a full-screen agent
// UI, or a permission request is answerable from a phone and invisible to the
// person sitting at the terminal.
//
// The overlay is composited by the host rather than written into the PTY, so
// this should hold for any child. "Should" is why there is a test.
func TestE2ECodexOverlayPaintsOverTheTUI(t *testing.T) {
	e, _, view := startCodexHost(t, "e2e-codex-overlay")

	if !view.waitForText(t, "OpenAI Codex", 45*time.Second) {
		t.Fatal("codex never rendered its UI under the host")
	}

	// Only the control viewer may set an overlay (host.go:767); an interactive
	// one is ignored in silence. The daemon holds this role in production.
	control, err := Dial(e.socket("e2e-codex-overlay"),
		Hello{Role: RoleControl, Cols: 100, Rows: 30, Name: "daemon"})
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	defer control.Close()

	err = control.SetOverlay(Overlay{
		Title:    "apply_patch",
		Body:     []string{"*** Add File: perm-probe.txt"},
		Options:  []string{"Allow once", "Deny"},
		Selected: 0,
		Footer:   "↑↓ choose · enter accept · esc cancel",
	})
	if err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}

	if !view.waitForText(t, "apply_patch", 15*time.Second) {
		t.Fatalf("overlay title never painted over codex; screen was:\n%s", view.screen.Text())
	}
	for _, want := range []string{"Allow once", "Deny", "*** Add File: perm-probe.txt"} {
		if !strings.Contains(view.screen.Text(), want) {
			t.Errorf("overlay missing %q; screen was:\n%s", want, view.screen.Text())
		}
	}

	if err := control.ClearOverlay(); err != nil {
		t.Fatalf("ClearOverlay: %v", err)
	}
	// The agent's own UI must survive the modal coming down.
	if !view.waitUntil(t, 15*time.Second, func(text string) bool {
		return !strings.Contains(text, "Allow once") && strings.Contains(text, "Codex")
	}) {
		t.Errorf("codex UI did not return after the overlay cleared; screen was:\n%s", view.screen.Text())
	}
}
