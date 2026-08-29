package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamrul1157024/helios/internal/terminal"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// heliosBinary builds the real binary once per test run. The host backend is
// only meaningful against a real `helios ptyhost`, so these tests drive the
// same artifact users run rather than an in-process fake.
func heliosBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binPath = "/tmp/helios-backend-test-bin"
		root, err := repoRoot()
		if err != nil {
			buildErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/helios")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("build output: %s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build helios: %v", buildErr)
	}
	return binPath
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// newHostBackend returns a Host wired to spawn real ptyhosts under a scratch
// helios dir. Temp dirs are rooted at /tmp because macOS caps unix socket
// paths at 104 bytes and the default TMPDIR alone nearly exhausts that.
func newHostBackend(t *testing.T) (*Host, string) {
	t.Helper()
	bin := heliosBinary(t)
	dir, err := os.MkdirTemp("/tmp", "hbk")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	// Isolation is by HOME: the spawned host resolves its own helios dir from
	// it, so tests never touch the developer's real ~/.helios.
	heliosDir := filepath.Join(dir, ".helios")
	if err := os.MkdirAll(heliosDir, 0o755); err != nil {
		t.Fatalf("helios dir: %v", err)
	}

	reg := terminal.NewRegistry(heliosDir, func(sessionID, cwd string, argv []string, env map[string]string) error {
		args := []string{"ptyhost", sessionID, "--cwd", cwd}
		if len(argv) > 0 {
			args = append(args, "--cmd", argv[0])
			for _, a := range argv[1:] {
				args = append(args, "--arg", a)
			}
		}
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "HOME="+dir)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	})

	h := NewHost(reg)
	t.Cleanup(func() {
		for id := range h.Snapshot() {
			h.Kill(id)
		}
		h.Close()
		os.RemoveAll(dir)
	})
	return h, dir
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestHostBackendStartCaptureKill(t *testing.T) {
	h, dir := newHostBackend(t)

	handle, err := h.Start("sess-start", dir, []string{"sh", "-c", "echo backend-marker-one; sleep 5"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if handle == "" {
		t.Fatal("expected a socket handle")
	}
	if !h.Alive("sess-start") {
		t.Fatal("session should be alive after start")
	}

	// Read through the emulator, never the raw stream: a real TUI positions
	// text with cursor-column jumps, so phrases are not contiguous in bytes.
	ok := waitFor(t, 10*time.Second, func() bool {
		text, err := h.Capture("sess-start")
		return err == nil && strings.Contains(text, "backend-marker-one")
	})
	if !ok {
		text, _ := h.Capture("sess-start")
		t.Fatalf("command output never appeared; screen was:\n%s", text)
	}

	if err := h.Kill("sess-start"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if h.Alive("sess-start") {
		t.Fatal("session should be dead after kill")
	}
	if _, err := h.Capture("sess-start"); err == nil {
		t.Fatal("capture should fail once the host is gone")
	}
}

func TestHostBackendSendText(t *testing.T) {
	h, dir := newHostBackend(t)

	// `cat` stands in for an agent: it is always present, and it echoes, so a
	// submitted line is visibly distinguishable from a typed-but-unsent one.
	if _, err := h.Start("sess-send2", dir, []string{"cat"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := h.SendText("sess-send2", "typed-into-cat"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	ok := waitFor(t, 10*time.Second, func() bool {
		text, err := h.Capture("sess-send2")
		// cat echoes the line back, so the text appears twice once submitted.
		return err == nil && strings.Count(text, "typed-into-cat") >= 2
	})
	if !ok {
		text, _ := h.Capture("sess-send2")
		t.Fatalf("text was not submitted; screen was:\n%s", text)
	}
}

func TestHostBackendSendKeyInterrupts(t *testing.T) {
	h, dir := newHostBackend(t)

	if _, err := h.Start("sess-key", dir, []string{"cat"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitFor(t, 10*time.Second, func() bool { return h.Alive("sess-key") }) {
		t.Fatal("session never came up")
	}
	if err := h.SendKey("sess-key", KeyCtrlC); err != nil {
		t.Fatalf("send key: %v", err)
	}
	// cat would sit on stdin forever, so its death is proof the key arrived as
	// a signal and not as bytes on the input stream. Nothing survives it: the
	// host runs the command directly, with no shell to fall back to.
	if !waitFor(t, 10*time.Second, func() bool { return !h.Alive("sess-key") }) {
		text, _ := h.Capture("sess-key")
		t.Fatalf("interrupt was not delivered; screen was:\n%s", text)
	}
}

func TestHostBackendMissingSessionErrors(t *testing.T) {
	h, _ := newHostBackend(t)

	if _, err := h.Capture("nope"); err != ErrNoTerminal {
		t.Fatalf("capture: want ErrNoTerminal, got %v", err)
	}
	if err := h.SendText("nope", "hi"); err != ErrNoTerminal {
		t.Fatalf("send text: want ErrNoTerminal, got %v", err)
	}
	if err := h.SendKey("nope", KeyEnter); err != ErrNoTerminal {
		t.Fatalf("send key: want ErrNoTerminal, got %v", err)
	}
	if h.Alive("nope") {
		t.Fatal("unknown session should not be alive")
	}
	if _, ok := h.Handle("nope"); ok {
		t.Fatal("unknown session should have no handle")
	}
	// Killing an unknown session is a no-op, not an error: the reaper calls it
	// on sessions that may already be gone.
	if err := h.Kill("nope"); err != nil {
		t.Fatalf("kill unknown: %v", err)
	}
}

func TestHostBackendSweepReportsDeadSessions(t *testing.T) {
	h, dir := newHostBackend(t)

	// The sleep keeps the host up long enough for Start to observe its socket;
	// without it the command can exit before the registry finishes connecting.
	if _, err := h.Start("sess-sweep", dir, []string{"sh", "-c", "sleep 1; exit 0"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The host exits with its command.
	if !waitFor(t, 10*time.Second, func() bool { return !h.Alive("sess-sweep") }) {
		t.Fatal("host never exited")
	}
	dropped := h.Sweep()
	found := false
	for _, id := range dropped {
		if id == "sess-sweep" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sweep should report the dead session, got %v", dropped)
	}
	if _, ok := h.Snapshot()["sess-sweep"]; ok {
		t.Fatal("dead session should be gone from the snapshot")
	}
}

func TestHostBackendUpdateCallbackFires(t *testing.T) {
	h, dir := newHostBackend(t)

	var mu sync.Mutex
	seen := map[string]int{}
	h.OnUpdate(func(sessionID string) {
		mu.Lock()
		seen[sessionID]++
		mu.Unlock()
	})

	if _, err := h.Start("sess-cb", dir, []string{"sh", "-c", "echo callback-marker; sleep 5"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	ok := waitFor(t, 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["sess-cb"] > 0
	})
	if !ok {
		t.Fatal("screen updates never reached the callback")
	}
}

func TestKeySequence(t *testing.T) {
	cases := map[Key]string{
		KeyEnter:  "\r",
		KeyEscape: "\x1b",
		KeyCtrlC:  "\x03",
		// Arrows move the highlight in Claude's own question UI, which is how
		// an answer from the phone reaches a dialog the CLI owns.
		KeyUp:   "\x1b[A",
		KeyDown: "\x1b[B",
	}
	for key, want := range cases {
		got, err := keySequence(key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if string(got) != want {
			t.Fatalf("%s: got %q want %q", key, got, want)
		}
	}
	if _, err := keySequence(Key("nope")); err == nil {
		t.Fatal("unknown key should error")
	}
}
