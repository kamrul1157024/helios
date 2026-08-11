package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/terminal"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// heliosBinary builds the real binary once per run. The relay is only
// meaningful against a real `helios ptyhost`, so these tests drive the same
// artifact users run rather than a stub socket server.
func heliosBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binPath = "/tmp/helios-server-test-bin"
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

// newTerminalServer returns an HTTP server exposing only the terminal routes,
// backed by real ptyhosts under a scratch helios dir. Auth is covered by the
// middleware tests; this exercises the relay.
//
// Temp dirs are rooted at /tmp because macOS caps unix socket paths at 104
// bytes and the default TMPDIR alone nearly exhausts that.
func newTerminalServer(t *testing.T) (*httptest.Server, *backend.Host, string) {
	t.Helper()
	bin := heliosBinary(t)
	dir, err := os.MkdirTemp("/tmp", "twk")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	heliosDir := filepath.Join(dir, ".helios")
	if err := os.MkdirAll(heliosDir, 0o755); err != nil {
		t.Fatalf("helios dir: %v", err)
	}

	reg := terminal.NewRegistry(heliosDir, func(sessionID, cwd, command string) error {
		args := []string{"ptyhost", sessionID, "--cwd", cwd}
		if command != "" {
			args = append(args, "--login-cmd", command)
		}
		cmd := exec.Command(bin, args...)
		// Isolation is by HOME: the spawned host resolves its own helios dir
		// from it, so tests never touch the developer's real ~/.helios.
		cmd.Env = append(os.Environ(), "HOME="+dir)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	})

	h := backend.NewHost(reg)
	ps := &PublicServer{shared: &Shared{Backend: h}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/terminal"):
			ps.handleSessionTerminal(w, r)
		case strings.HasSuffix(r.URL.Path, "/wake"):
			ps.handleSessionWake(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)

	t.Cleanup(func() {
		srv.Close()
		for id := range h.Snapshot() {
			h.Kill(id)
		}
		h.Close()
		os.RemoveAll(dir)
	})
	return srv, h, dir
}

// dialTerminalWS opens the relay and returns it as a byte stream, which is how
// a real client uses it: the frame protocol delimits itself, so message
// boundaries carry no meaning.
func dialTerminalWS(t *testing.T, srv *httptest.Server, sessionID, query string) (io.ReadWriteCloser, func()) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/sessions/" + sessionID + "/terminal" + query
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, resp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		cancel()
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial terminal ws (http %d): %v", status, err)
	}
	conn.SetReadLimit(-1)
	stream := websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
	return stream, func() {
		conn.Close(websocket.StatusNormalClosure, "")
		cancel()
	}
}

// readUntil consumes frames until want appears in the rendered output, so a
// test never depends on how the host chunks its writes.
func readUntil(t *testing.T, stream io.Reader, want string, d time.Duration) string {
	t.Helper()
	return readUntilCount(t, stream, want, 1, d)
}

// readUntilCount waits for want to appear n times. Waiting for the first
// occurrence is not enough when the point is that the PTY echoed something
// back: the typed copy arrives before the echoed one.
func readUntilCount(t *testing.T, stream io.Reader, want string, n int, d time.Duration) string {
	t.Helper()
	type result struct {
		text string
		err  error
	}
	out := make(chan result, 1)
	go func() {
		var sb strings.Builder
		for {
			f, err := terminal.ReadFrame(stream)
			if err != nil {
				out <- result{sb.String(), err}
				return
			}
			switch f.Type {
			case terminal.FrameSnapshot:
				_, ansi, err := terminal.DecodeSnapshot(f.Payload)
				if err != nil {
					out <- result{sb.String(), err}
					return
				}
				sb.Write(ansi)
			case terminal.FrameOutput:
				sb.Write(f.Payload)
			}
			if strings.Count(sb.String(), want) >= n {
				out <- result{sb.String(), nil}
				return
			}
		}
	}()

	select {
	case r := <-out:
		if r.err != nil && strings.Count(r.text, want) < n {
			t.Fatalf("stream ended before %q appeared %d× (%v); got:\n%s", want, n, r.err, r.text)
		}
		return r.text
	case <-time.After(d):
		t.Fatalf("timed out waiting for %q ×%d", want, n)
		return ""
	}
}

func hello(t *testing.T, stream io.Writer, role terminal.Role) {
	t.Helper()
	if err := terminal.WriteJSONFrame(stream, terminal.FrameHello, terminal.Hello{
		Role: role, Cols: 80, Rows: 24, Name: "test",
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
}

func TestTerminalWSStreamsOutput(t *testing.T) {
	srv, h, dir := newTerminalServer(t)

	if _, err := h.Start("ws-out", dir, "echo relay-marker-one"); err != nil {
		t.Fatalf("start: %v", err)
	}

	stream, done := dialTerminalWS(t, srv, "ws-out", "")
	defer done()

	hello(t, stream, terminal.RoleObserver)
	readUntil(t, stream, "relay-marker-one", 15*time.Second)
}

func TestTerminalWSSendsInput(t *testing.T) {
	srv, h, dir := newTerminalServer(t)

	// `cat` stands in for an agent: always present, and it echoes, so a
	// delivered line is visibly distinguishable from one that never arrived.
	if _, err := h.Start("ws-in", dir, "cat"); err != nil {
		t.Fatalf("start: %v", err)
	}

	stream, done := dialTerminalWS(t, srv, "ws-in", "")
	defer done()

	hello(t, stream, terminal.RoleInteractive)
	if err := terminal.WriteFrame(stream, terminal.FrameInput, []byte("through-the-relay\r")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	// cat echoes the line, so it lands twice once the PTY has processed it:
	// once as terminal echo of the keystrokes, once as cat's own output.
	readUntilCount(t, stream, "through-the-relay", 2, 15*time.Second)
}

func TestTerminalWSLateViewerGetsSnapshot(t *testing.T) {
	srv, h, dir := newTerminalServer(t)

	if _, err := h.Start("ws-late", dir, "echo printed-before-connect"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Wait for the output to exist before connecting, so the only way to see
	// it is the snapshot the host renders for a fresh viewer.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if text, err := h.Capture("ws-late"); err == nil && strings.Contains(text, "printed-before-connect") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	stream, done := dialTerminalWS(t, srv, "ws-late", "")
	defer done()

	hello(t, stream, terminal.RoleObserver)
	readUntil(t, stream, "printed-before-connect", 15*time.Second)
}

func TestTerminalWSColdSessionConflicts(t *testing.T) {
	srv, _, _ := newTerminalServer(t)

	url := srv.URL + "/api/sessions/never-started/terminal"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	// 409, not 404: the retry a client should make is wake=1, which is a
	// different fix from correcting a bad session id.
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cold session: got %d want %d", resp.StatusCode, http.StatusConflict)
	}
}

// sizeAfterResize connects with the given role, requests a resize, then round
// trips a ping so every frame the host chose to emit has arrived. It returns
// the size the host last reported.
func sizeAfterResize(t *testing.T, srv *httptest.Server, sessionID string, role terminal.Role, cols, rows int) (int, int) {
	t.Helper()
	stream, done := dialTerminalWS(t, srv, sessionID, "")
	defer done()

	hello(t, stream, role)
	if err := terminal.WriteFrame(stream, terminal.FrameResize, terminal.EncodeResize(cols, rows)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := terminal.WriteFrame(stream, terminal.FramePing, nil); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	type size struct{ cols, rows int }
	got := make(chan size, 1)
	go func() {
		last := size{}
		for {
			f, err := terminal.ReadFrame(stream)
			if err != nil {
				got <- last
				return
			}
			switch f.Type {
			case terminal.FrameStatus:
				if st, err := terminal.ParseStatus(f.Payload); err == nil {
					last = size{st.Cols, st.Rows}
				}
			case terminal.FramePong:
				got <- last
				return
			}
		}
	}()

	select {
	case s := <-got:
		return s.cols, s.rows
	case <-time.After(15 * time.Second):
		t.Fatal("host never answered the ping")
		return 0, 0
	}
}

func TestTerminalWSObserverCannotResize(t *testing.T) {
	srv, h, dir := newTerminalServer(t)

	if _, err := h.Start("ws-observer", dir, "cat"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// An observer must never shrink the PTY out from under an interactive
	// desktop — opening a phone cannot be allowed to reflow a real terminal.
	cols, _ := sizeAfterResize(t, srv, "ws-observer", terminal.RoleObserver, 40, 10)
	if cols == 40 {
		t.Fatal("observer resize was honoured; it must be ignored")
	}
}

func TestTerminalWSInteractiveResizes(t *testing.T) {
	srv, h, dir := newTerminalServer(t)

	if _, err := h.Start("ws-interactive", dir, "cat"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The counterpart to the observer case: without this, that test would pass
	// even if resize were broken for everyone.
	cols, rows := sizeAfterResize(t, srv, "ws-interactive", terminal.RoleInteractive, 101, 37)
	if cols != 101 || rows != 37 {
		t.Fatalf("interactive resize: got %dx%d want 101x37", cols, rows)
	}
}

func TestIsDisconnect(t *testing.T) {
	if !isDisconnect(io.EOF) {
		t.Fatal("EOF is an ordinary hang-up")
	}
	if !isDisconnect(context.Canceled) {
		t.Fatal("cancellation is an ordinary hang-up")
	}
	if isDisconnect(errors.New("boom")) {
		t.Fatal("unknown errors should be logged, not swallowed")
	}
}
