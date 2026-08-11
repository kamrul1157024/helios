package terminal

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// serveHost starts a host on a temporary socket and returns its path.
func serveHost(t *testing.T, cfg HostConfig) (*Host, string) {
	t.Helper()
	if cfg.Cols == 0 {
		cfg.Cols, cfg.Rows = 80, 24
	}
	h, err := NewHost(cfg)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	// Deliberately short: macOS caps sun_path at 104 bytes, and t.TempDir()
	// plus a long test name blows past it. This is the same constraint that
	// makes production socket names a hash rather than the session ID.
	dir, err := os.MkdirTemp("", "ht")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Serve(ctx, ln)
	t.Cleanup(func() { cancel(); h.Close(); ln.Close() })
	return h, sock
}

// waitForScreen polls the emulator until want appears.
func waitForScreen(t *testing.T, h *Host, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		last = h.Screen().Text()
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("timeout waiting for %q; screen was:\n%s", want, last)
	return last
}

func TestHostCapturesOutput(t *testing.T) {
	h, _ := serveHost(t, HostConfig{
		SessionID: "s1",
		Command:   "sh",
		Args:      []string{"-c", "echo hello-helios; sleep 5"},
	})
	waitForScreen(t, h, "hello-helios", 5*time.Second)

	if h.Ring().Seq() == 0 {
		t.Error("ring recorded no bytes")
	}
}

func TestHostWriteReachesChild(t *testing.T) {
	h, _ := serveHost(t, HostConfig{SessionID: "s2", Command: "sh"})
	time.Sleep(300 * time.Millisecond)
	if err := h.Write([]byte("echo from-input\r"), "test"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitForScreen(t, h, "from-input", 5*time.Second)
}

func TestHostViewerReceivesSnapshotAndLiveOutput(t *testing.T) {
	h, sock := serveHost(t, HostConfig{
		SessionID: "s3",
		Command:   "sh",
		Args:      []string{"-c", "echo first; sleep 10"},
	})
	waitForScreen(t, h, "first", 5*time.Second)

	c, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24, Name: "v1"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	// A late joiner must be caught up via a snapshot containing prior output.
	f, err := c.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if f.Type != FrameSnapshot {
		t.Fatalf("first frame = %s, want snapshot", f.Type)
	}
	_, ansi, err := DecodeSnapshot(f.Payload)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	if !strings.Contains(string(ansi), "first") {
		t.Errorf("snapshot missing prior output: %q", ansi)
	}

	// Then live output.
	h.Write([]byte("echo second\r"), "test")
	if !waitForFrameContaining(t, c, "second", 5*time.Second) {
		t.Error("did not receive live output after the snapshot")
	}
}

// waitForFrameContaining reads frames until one carries want.
func waitForFrameContaining(t *testing.T, c *Client, want string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		f, err := c.Next()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return false
		}
		if (f.Type == FrameOutput || f.Type == FrameSnapshot) &&
			strings.Contains(string(f.Payload), want) {
			return true
		}
	}
	return false
}

func TestHostFanoutToMultipleViewers(t *testing.T) {
	h, sock := serveHost(t, HostConfig{SessionID: "s4", Command: "sh"})
	time.Sleep(300 * time.Millisecond)

	var clients []*Client
	for i := 0; i < 3; i++ {
		c, err := Dial(sock, Hello{Role: RoleObserver, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		defer c.Close()
		clients = append(clients, c)
	}
	// Let all three register before producing output.
	time.Sleep(300 * time.Millisecond)
	h.Write([]byte("echo broadcast-me\r"), "test")

	for i, c := range clients {
		if !waitForFrameContaining(t, c, "broadcast-me", 5*time.Second) {
			t.Errorf("viewer %d never received the broadcast", i)
		}
	}
}

func TestHostObserverDoesNotShrinkPTY(t *testing.T) {
	h, sock := serveHost(t, HostConfig{
		SessionID: "s5", Command: "sh", Cols: 100, Rows: 30,
	})
	time.Sleep(200 * time.Millisecond)

	interactive, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("dial interactive: %v", err)
	}
	defer interactive.Close()
	time.Sleep(200 * time.Millisecond)

	// A 40-column phone must not degrade the desktop's geometry.
	observer, err := Dial(sock, Hello{Role: RoleObserver, Cols: 40, Rows: 10})
	if err != nil {
		t.Fatalf("dial observer: %v", err)
	}
	defer observer.Close()
	time.Sleep(400 * time.Millisecond)

	if cols, rows := h.Screen().Size(); cols != 100 || rows != 30 {
		t.Errorf("size = %dx%d after observer joined, want 100x30", cols, rows)
	}
}

func TestHostInteractiveResizeNegotiatesMinimum(t *testing.T) {
	h, sock := serveHost(t, HostConfig{
		SessionID: "s6", Command: "sh", Cols: 100, Rows: 30,
	})
	time.Sleep(200 * time.Millisecond)

	a, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer a.Close()
	b, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 60, Rows: 20})
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer b.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cols, rows := h.Screen().Size(); cols == 60 && rows == 20 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	cols, rows := h.Screen().Size()
	t.Errorf("size = %dx%d, want the 60x20 minimum across interactive viewers", cols, rows)
}

func TestHostClientInputReachesChild(t *testing.T) {
	h, sock := serveHost(t, HostConfig{SessionID: "s7", Command: "sh"})
	time.Sleep(300 * time.Millisecond)

	c, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24, Name: "mobile"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	time.Sleep(200 * time.Millisecond)

	// Multi-line and shell-hostile text: exactly what send-keys mangled.
	if err := c.Send([]byte("echo 'a; b -c \"d\"'\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForScreen(t, h, `a; b -c "d"`, 5*time.Second)
}

func TestHostExitPropagates(t *testing.T) {
	h, sock := serveHost(t, HostConfig{
		SessionID: "s8", Command: "sh", Args: []string{"-c", "exit 3"},
	})
	_ = sock

	select {
	case <-h.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("host never reported the child exiting")
	}
	if got := h.ExitCode(); got != 3 {
		t.Errorf("ExitCode() = %d, want 3", got)
	}
	if st := h.Status(); st.State != StateExited {
		t.Errorf("state = %s, want exited", st.State)
	}
}

func TestHostStatusReportsViewers(t *testing.T) {
	h, sock := serveHost(t, HostConfig{SessionID: "s9", Command: "sh"})
	time.Sleep(200 * time.Millisecond)

	c, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Status().Viewers == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("Viewers = %d, want 1", h.Status().Viewers)
}

func TestHostResumeFromSequence(t *testing.T) {
	h, sock := serveHost(t, HostConfig{SessionID: "s10", Command: "sh"})
	time.Sleep(300 * time.Millisecond)
	h.Write([]byte("echo alpha\r"), "test")
	waitForScreen(t, h, "alpha", 5*time.Second)

	// Reconnecting with a retained sequence replays rather than resyncing.
	seq := h.Ring().Start()
	c, err := Dial(sock, Hello{Role: RoleObserver, Cols: 80, Rows: 24, Since: seq + 1})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	f, err := c.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if f.Type != FrameOutput {
		t.Errorf("first frame = %s, want output replay", f.Type)
	}
}

func TestHostPingPong(t *testing.T) {
	_, sock := serveHost(t, HostConfig{SessionID: "s11", Command: "sh"})
	time.Sleep(200 * time.Millisecond)

	c, err := Dial(sock, Hello{Role: RoleObserver, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		f, err := c.Next()
		if err != nil {
			continue
		}
		if f.Type == FramePong {
			return
		}
	}
	t.Error("no pong received")
}

// Starting a session from inside another session's terminal is ordinary, so
// the parent's ID arrives in the environment. Appending a second entry would
// not be enough: getenv answers with the first match, and the child would
// report itself as the parent.
func TestHostSessionEnvOverridesAnInheritedOne(t *testing.T) {
	h, _ := serveHost(t, HostConfig{
		SessionID: "child",
		Command:   "sh",
		Args:      []string{"-c", "echo id=[$HELIOS_SESSION_ID]; sleep 5"},
		Env:       append(os.Environ(), SessionEnv+"=parent"),
	})

	waitForScreen(t, h, "id=[child]", 5*time.Second)
}

func TestSetEnv_ReplacesInPlaceOfAppending(t *testing.T) {
	got := setEnv([]string{"A=1", "B=2", "A=3"}, "A=9", "C=4")

	for _, kv := range got {
		if kv == "A=1" || kv == "A=3" {
			t.Errorf("env kept a stale entry %q: %q", kv, got)
		}
	}
	if !slices.Contains(got, "A=9") || !slices.Contains(got, "B=2") || !slices.Contains(got, "C=4") {
		t.Errorf("env = %q, want A=9, B=2 and C=4", got)
	}
}

// An entry with no "=" is not a variable and is passed through untouched.
func TestSetEnv_KeepsMalformedEntries(t *testing.T) {
	if got := setEnv([]string{"NOTAVAR"}, "A=1"); !slices.Contains(got, "NOTAVAR") {
		t.Errorf("env = %q, want NOTAVAR kept", got)
	}
}

// A viewer that stops reading must be dropped from the host entirely.
//
// Signalling its close channel only stops the writer; the reader stays parked
// in ReadFrame until the peer hangs up, which for a wedged client is never. The
// viewer would then keep voting on the PTY size and keep the viewer count up
// for the life of the session.
func TestHostDropsAStalledViewer(t *testing.T) {
	h, sock := serveHost(t, HostConfig{
		SessionID: "stall",
		Command:   "yes",
		Args:      []string{strings.Repeat("flood", 40)},
	})

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := WriteJSONFrame(conn, FrameHello, Hello{
		Role: RoleInteractive, Name: "stalled", Cols: 40, Rows: 10,
	}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	// Read the catch-up frames so the viewer is fully registered, then stop
	// reading and let the child's output back up behind it.
	for i := 0; i < 2; i++ {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := ReadFrame(conn); err != nil {
			t.Fatalf("handshake frame %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if h.Status().Viewers == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("stalled viewer still attached after 15s: %+v", h.Status())
}
