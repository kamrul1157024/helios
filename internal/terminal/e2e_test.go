package terminal

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// e2eEnv is an isolated HELIOS_DIR plus a built helios binary, so end-to-end
// tests drive the real `helios ptyhost` subcommand rather than an in-process
// stand-in.
type e2eEnv struct {
	dir    string
	binary string
}

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// buildHelios compiles the binary once per test run.
func buildHelios(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		out := filepath.Join(os.TempDir(), "helios-e2e-bin")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/helios")
		cmd.Dir = repoRoot(t)
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("build output: %s", b)
			return
		}
		builtBin = out
	})
	if buildErr != nil {
		t.Fatalf("build helios: %v", buildErr)
	}
	return builtBin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/terminal -> repo root
	return filepath.Dir(filepath.Dir(wd))
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	// Rooted at /tmp, not TMPDIR: the macOS per-user temp dir is ~48 bytes on
	// its own and the socket path underneath must stay within sun_path's 104.
	dir, err := os.MkdirTemp("/tmp", "he2e")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return &e2eEnv{dir: dir, binary: buildHelios(t)}
}

// detachSysProcAttr is the same detach the daemon uses to outlive its parent.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// spawnHost launches a detached `helios ptyhost` exactly as the daemon would.
func (e *e2eEnv) spawnHost(t *testing.T, sessionID, command string, args ...string) {
	t.Helper()
	cmdArgs := []string{"ptyhost", sessionID, "--cwd", e.dir, "--cmd", command}
	for _, a := range args {
		cmdArgs = append(cmdArgs, "--arg", a)
	}
	cmd := exec.Command(e.binary, cmdArgs...)
	cmd.Env = append(os.Environ(), "HOME="+e.dir)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ptyhost: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
		if s := out.String(); s != "" {
			t.Logf("ptyhost output: %s", s)
		}
	})
}

// heliosDir is what the spawned host will use, given HOME override.
func (e *e2eEnv) heliosDir() string { return filepath.Join(e.dir, ".helios") }

func (e *e2eEnv) socket(sessionID string) string {
	return SocketPath(e.heliosDir(), sessionID)
}

// TestE2EPtyHostServesViewers drives the real subcommand end to end: spawn,
// socket, snapshot, input, output, sidecar.
func TestE2EPtyHostServesViewers(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-basic"
	e.spawnHost(t, sid, "sh")

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("ptyhost socket never appeared")
	}

	// The sidecar is the durable session->terminal mapping replacing the tmux
	// pane option.
	car, err := ReadSidecar(SidecarPath(e.heliosDir(), sid))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if car.SessionID != sid {
		t.Errorf("sidecar session = %q, want %q", car.SessionID, sid)
	}
	if car.PID <= 0 || car.ChildPID <= 0 {
		t.Errorf("sidecar pids = host %d child %d, want both > 0", car.PID, car.ChildPID)
	}

	c, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24, Name: "e2e"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	// Raw replay, not a snapshot: the ring still holds everything this host
	// has written, and the child's own bytes need no geometry from us.
	f, err := c.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if f.Type != FrameOutput {
		t.Fatalf("first frame = %s, want raw output", f.Type)
	}

	if err := c.Send([]byte("echo e2e-roundtrip\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !waitForFrameContaining(t, c, "e2e-roundtrip", 10*time.Second) {
		t.Error("input did not round-trip through the real ptyhost")
	}
}

// TestE2ETwoViewersSeeSameSession is the mobile-and-terminal handoff claim:
// two independent connections drive one process and both see everything.
func TestE2ETwoViewersSeeSameSession(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-handoff"
	e.spawnHost(t, sid, "sh")

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("socket never appeared")
	}

	desktop, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 100, Rows: 30, Name: "desktop"})
	if err != nil {
		t.Fatalf("dial desktop: %v", err)
	}
	defer desktop.Close()

	mobile, err := Dial(sock, Hello{Role: RoleObserver, Cols: 40, Rows: 20, Name: "mobile"})
	if err != nil {
		t.Fatalf("dial mobile: %v", err)
	}
	defer mobile.Close()
	time.Sleep(500 * time.Millisecond)

	// Mobile types; the desktop must see it. This is the handoff tmux could
	// not do, because the two surfaces had no shared live view.
	if err := mobile.Send([]byte("echo from-mobile\r")); err != nil {
		t.Fatalf("mobile send: %v", err)
	}
	if !waitForFrameContaining(t, desktop, "from-mobile", 10*time.Second) {
		t.Error("desktop never saw mobile's input")
	}

	// And the reverse.
	if err := desktop.Send([]byte("echo from-desktop\r")); err != nil {
		t.Fatalf("desktop send: %v", err)
	}
	if !waitForFrameContaining(t, mobile, "from-desktop", 10*time.Second) {
		t.Error("mobile never saw desktop's input")
	}
}

// TestE2EHostSurvivesSpawnerExit is the property that justifies a separate
// host process at all: sessions must outlive whatever launched them.
func TestE2EHostSurvivesSpawnerExit(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-survive"

	// A short-lived "daemon" that detaches a host and exits immediately.
	spawner := exec.Command(e.binary, "ptyhost", sid, "--cwd", e.dir, "--cmd", "sh")
	spawner.Env = append(os.Environ(), "HOME="+e.dir)
	spawner.SysProcAttr = detachSysProcAttr()
	if err := spawner.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := spawner.Process.Pid
	if err := spawner.Process.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	})

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("socket never appeared")
	}

	c, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.Send([]byte("echo still-alive\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !waitForFrameContaining(t, c, "still-alive", 10*time.Second) {
		t.Error("host did not survive as an independent session")
	}
}

// TestE2ERegistryRecoversAndEvicts covers the daemon-restart path: rebuild
// from the run dir by dialling sockets, then evict cleanly.
func TestE2ERegistryRecoversAndEvicts(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-registry"
	e.spawnHost(t, sid, "sh")

	if !WaitForSocket(e.socket(sid), 20*time.Second) {
		t.Fatal("socket never appeared")
	}

	// A fresh registry, as if the daemon had just restarted.
	reg := NewRegistry(e.heliosDir(), nil)
	alive, cleaned, err := reg.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if alive != 1 {
		t.Errorf("recovered %d live hosts, want 1", alive)
	}
	if cleaned != 0 {
		t.Errorf("cleaned %d, want 0", cleaned)
	}
	if !reg.IsWarm(sid) {
		t.Error("IsWarm = false for a live host")
	}

	// Eviction is a clean exit; the session returns to cold.
	if err := reg.Evict(sid); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if reg.IsWarm(sid) {
		t.Error("IsWarm = true after eviction")
	}
	if _, err := os.Stat(SidecarPath(e.heliosDir(), sid)); !os.IsNotExist(err) {
		t.Error("sidecar not cleaned up after eviction")
	}
}

// TestE2ERegistryCleansStaleSockets covers the case where a host died without
// unlinking: recovery must not resurrect a phantom session.
func TestE2ERegistryCleansStaleSockets(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-stale"

	// A sidecar with no live socket behind it.
	if err := WriteSidecar(e.heliosDir(), Sidecar{
		SessionID: sid,
		PID:       999999,
		Cwd:       e.dir,
		Socket:    e.socket(sid),
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}

	reg := NewRegistry(e.heliosDir(), nil)
	alive, cleaned, err := reg.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if alive != 0 {
		t.Errorf("alive = %d, want 0", alive)
	}
	if cleaned != 1 {
		t.Errorf("cleaned = %d, want 1", cleaned)
	}
	if _, err := os.Stat(SidecarPath(e.heliosDir(), sid)); !os.IsNotExist(err) {
		t.Error("stale sidecar was not removed")
	}
}

// TestE2ERegistryWakeIsIdempotent covers the prewarm endpoint's contract: the
// mobile app calls it on every session open and must not spawn duplicates.
func TestE2ERegistryWakeIsIdempotent(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-wake"

	var spawns int
	var mu sync.Mutex
	reg := NewRegistry(e.heliosDir(), func(sessionID, cwd string, argv []string) error {
		mu.Lock()
		spawns++
		mu.Unlock()
		cmd := exec.Command(e.binary, "ptyhost", sessionID, "--cwd", cwd, "--cmd", "sh")
		cmd.Env = append(os.Environ(), "HOME="+e.dir)
		cmd.SysProcAttr = detachSysProcAttr()
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	})
	t.Cleanup(func() { reg.Evict(sid) })

	sock1, err := reg.Wake(sid, e.dir)
	if err != nil {
		t.Fatalf("Wake 1: %v", err)
	}
	sock2, err := reg.Wake(sid, e.dir)
	if err != nil {
		t.Fatalf("Wake 2: %v", err)
	}
	if sock1 != sock2 {
		t.Errorf("Wake returned different sockets: %q vs %q", sock1, sock2)
	}

	mu.Lock()
	got := spawns
	mu.Unlock()
	if got != 1 {
		t.Errorf("spawned %d hosts, want 1 — Wake must be idempotent", got)
	}
}

// hasClaude reports whether the real CLI is installed.
func hasClaude(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed; skipping real-agent e2e")
	}
	return p
}

// TestE2EClaudeCodeBootsUnderHost is the load-bearing end-to-end claim: the
// real agent boots, renders, and responds to keystrokes through our PTY host
// and emulator, with no tmux involved. HOME is a scratch dir, so Claude shows
// its onboarding flow; that is a real TUI to drive and it submits no prompt,
// so the test spends no tokens.
func TestE2EClaudeCodeBootsUnderHost(t *testing.T) {
	claude := hasClaude(t)
	e := newE2E(t)

	project, err := os.MkdirTemp("/tmp", "hproj")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(project) })

	const sid = "e2e-claude"
	cmd := exec.Command(e.binary, "ptyhost", sid,
		"--cwd", project, "--cols", "100", "--rows", "30", "--cmd", claude)
	cmd.Env = append(os.Environ(), "HOME="+e.dir)
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
		t.Fatal("ptyhost socket never appeared for claude")
	}

	desktop, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 100, Rows: 30, Name: "desktop"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer desktop.Close()
	view := newViewerScreen(t, desktop, 100, 30)

	// Reaching the UI at all proves the emulator answered Claude's startup
	// queries (DSR, DA1, XTVERSION). Without replies it would hang here.
	if !view.waitForText(t, "Welcome to Claude Code", 45*time.Second) {
		t.Fatal("claude never rendered its UI under the host")
	}
	if !view.waitForText(t, "Choose the text style", 30*time.Second) {
		t.Fatal("claude never reached an interactive prompt")
	}

	// Drive the real TUI: arrow keys and Enter must arrive as keystrokes, not
	// as the mangled text tmux send-keys produced.
	if err := desktop.Send([]byte("\x1b[B")); err != nil {
		t.Fatalf("send down: %v", err)
	}
	if !view.waitForText(t, "Light mode", 10*time.Second) {
		t.Fatal("arrow key did not reach the TUI")
	}
	if err := desktop.Send([]byte("\r")); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	// Advancing off the theme picker is the proof that Enter registered.
	if !view.waitUntil(t, 20*time.Second, func(text string) bool {
		return !strings.Contains(text, "Choose the text style")
	}) {
		t.Error("Enter did not advance the onboarding flow")
	}
}

// TestE2EClaudeSnapshotCatchesUpLateViewer is the mobile-joins-a-running-
// desktop-session path against a real TUI: the snapshot alone must reconstruct
// the screen, with styling intact.
func TestE2EClaudeSnapshotCatchesUpLateViewer(t *testing.T) {
	claude := hasClaude(t)
	e := newE2E(t)

	project, err := os.MkdirTemp("/tmp", "hproj")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(project) })

	const sid = "e2e-claude-snap"
	cmd := exec.Command(e.binary, "ptyhost", sid,
		"--cwd", project, "--cols", "100", "--rows", "30", "--cmd", claude)
	cmd.Env = append(os.Environ(), "HOME="+e.dir)
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
		t.Fatal("socket never appeared")
	}

	desktop, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 100, Rows: 30, Name: "desktop"})
	if err != nil {
		t.Fatalf("dial desktop: %v", err)
	}
	defer desktop.Close()
	dv := newViewerScreen(t, desktop, 100, 30)
	if !dv.waitForText(t, "Welcome to Claude Code", 45*time.Second) {
		t.Fatal("claude never rendered")
	}

	// Mobile joins late. Its catch-up is the raw stream while the ring holds
	// it, and the styling has to survive that too.
	mobile, err := Dial(sock, Hello{Role: RoleObserver, Cols: 100, Rows: 30, Name: "mobile"})
	if err != nil {
		t.Fatalf("dial mobile: %v", err)
	}
	defer mobile.Close()

	f, err := mobile.Next()
	if err != nil {
		t.Fatalf("mobile Next: %v", err)
	}
	if f.Type != FrameOutput {
		t.Fatalf("mobile first frame = %s, want raw output", f.Type)
	}
	ansi := f.Payload
	if !bytes.Contains(ansi, []byte("\x1b[38;2;")) {
		t.Error("snapshot lost truecolor styling; the UI would render flat")
	}

	// The snapshot on its own must reconstruct the session. "Welcome to Claude
	// Code" is the load-bearing assertion: Claude renders inline rather than on
	// the alternate screen, so by now the banner has scrolled off the grid. A
	// viewport-only snapshot would lose it, and with it every earlier turn of a
	// real conversation.
	mv := NewScreen(100, 30)
	defer mv.Close()
	mv.StartDrain(&syncBuf{})
	mv.Write(ansi)
	if !strings.Contains(mv.Text(), "Choose the text style") {
		t.Errorf("snapshot did not reconstruct the viewport:\n%s", mv.Text())
	}
	if mv.ScrollbackLen() == 0 {
		t.Fatal("snapshot carried no scrollback")
	}
	if !strings.Contains(scrollbackText(mv), "Welcome to Claude Code") {
		t.Errorf("scrolled-off history lost in the snapshot; scrollback was:\n%s",
			scrollbackText(mv))
	}
}

// scrollbackText renders a screen's scrollback as plain text.
func scrollbackText(s *Screen) string {
	cols, _ := s.Size()
	var sb strings.Builder
	for y := 0; y < s.ScrollbackLen(); y++ {
		var line strings.Builder
		for x := 0; x < cols; x++ {
			if c := s.em.ScrollbackCellAt(x, y); c != nil && c.Content != "" {
				line.WriteString(c.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// viewerScreen is a client-side emulator: exactly what the TUI and the mobile
// app must do with the frames they receive.
//
// Grepping the raw byte stream does not work for a real TUI. Claude Code lays
// text out with cursor-column jumps, so "Welcome to Claude Code" goes over the
// wire as "Welcome\x1b[9Gto\x1b[12GClaude\x1b[19GCode" and the literal string
// never appears. Only a rendered screen can be asserted against.
type viewerScreen struct {
	screen *Screen
	client *Client
}

func newViewerScreen(t *testing.T, c *Client, cols, rows int) *viewerScreen {
	t.Helper()
	s := NewScreen(cols, rows)
	// A viewer never talks back to its local emulator; replies are discarded,
	// but the drain is still mandatory or the emulator wedges.
	s.StartDrain(&syncBuf{})
	t.Cleanup(func() { s.Close() })
	return &viewerScreen{screen: s, client: c}
}

// waitForText pumps frames into the local emulator until want appears on the
// rendered screen.
func (v *viewerScreen) waitForText(t *testing.T, want string, d time.Duration) bool {
	t.Helper()
	return v.waitUntil(t, d, func(text string) bool {
		return strings.Contains(text, want)
	})
}

// waitUntil pumps frames into the local emulator until cond holds for the
// rendered text, or the timeout elapses.
func (v *viewerScreen) waitUntil(t *testing.T, d time.Duration, cond func(text string) bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond(v.screen.Text()) {
			return true
		}
		if err := v.client.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		f, err := v.client.Next()
		if err != nil {
			// errors.As, not a type assertion: ReadFrame wraps with %w, so a
			// bare assertion misses every deadline and aborts the wait on the
			// first quiet tick.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// The host closed the connection; log why, or the caller only sees
			// "text never appeared".
			t.Logf("viewer connection dropped: %v", err)
			return false
		}
		switch f.Type {
		case FrameOutput:
			v.screen.Write(f.Payload)
		case FrameSnapshot:
			_, ansi, err := DecodeSnapshot(f.Payload)
			if err != nil {
				t.Fatalf("DecodeSnapshot: %v", err)
			}
			v.screen.Write(ansi)
		}
	}
	t.Logf("rendered screen was:\n%s", v.screen.Text())
	return false
}

// TestE2EArgvReachesTheChildWhole is the claim behind running the agent
// directly: there is no shell between the host and the command, so an
// argument keeps its spaces and metacharacters instead of being re-parsed.
// A prompt sent from the phone is exactly this — arbitrary user text.
func TestE2EArgvReachesTheChildWhole(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-argv"
	const raw = "fix `git log` and say \"hi\", it's $HOME"
	// printf renders its argument literally; a shell in the chain would have
	// expanded or split it before printf ever saw it.
	e.spawnHost(t, sid, "sh", "-c", `printf '[%s]\n' "$1"; sleep 5`, "sh", raw)

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("ptyhost socket never appeared")
	}

	c, err := Dial(sock, Hello{Role: RoleObserver, Cols: 200, Rows: 24, Name: "e2e"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if !waitForFrameContaining(t, c, "["+raw+"]", 10*time.Second) {
		t.Errorf("child never received %q intact", raw)
	}
}

// TestE2EUsagePricesALiveHost covers the number the clients show. It replaced
// the eviction the pool used to do on its own: nothing reclaims memory now, so
// the reading has to be real or the user is deciding on a fiction.
func TestE2EUsagePricesALiveHost(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-usage"

	reg := NewRegistry(e.heliosDir(), func(sessionID, cwd string, argv []string) error {
		cmd := exec.Command(e.binary, "ptyhost", sessionID, "--cwd", cwd, "--cmd", "sh")
		cmd.Env = append(os.Environ(), "HOME="+e.dir)
		cmd.SysProcAttr = detachSysProcAttr()
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	})
	t.Cleanup(func() { reg.Evict(sid) })

	if _, err := reg.Wake(sid, e.dir); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	usage := reg.Usage()
	if usage[sid] <= 0 {
		t.Fatalf("usage = %v, want resident bytes for the live host", usage)
	}

	// Cached: measuring forks ps and pgrep per process, and clients ask on
	// every poll.
	if again := reg.Usage(); len(again) != len(usage) || again[sid] != usage[sid] {
		t.Errorf("second reading = %v, want the cached %v", again, usage)
	}
}
