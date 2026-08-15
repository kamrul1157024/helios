package terminal

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A prompt delivered as plain input is one burst to the application, and the
// Enter at its tail is read as part of it — the submit is lost and the text
// waits in the composer. Bracketing tells the application where the paste
// ends, so what follows is unambiguously a keypress.
func TestHostPasteBracketsWhenChildEnabledTheMode(t *testing.T) {
	h, sock := serveHost(t, HostConfig{
		SessionID: "paste-on",
		Command:   "sh",
		// The child enables bracketed paste, then echoes what it is given.
		Args: []string{"-c", `printf '\033[?2004h'; cat`},
	})
	c := dialViewer(t, sock, "paste-on-viewer")
	// The child has to have announced the mode before the paste, or the host
	// is right to send plain text and the test is measuring its own race.
	if !waitForFrameContaining(t, c, "\x1b[?2004h", 5*time.Second) {
		t.Fatal("child never enabled bracketed paste")
	}

	if err := h.Paste("hello-bracketed", "test"); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	// Matched without the ESC: the line discipline echoes control bytes as
	// their caret form, so what comes back is "^[[200~", not a raw escape.
	echoed := collectFrames(t, c, time.Second)
	if !strings.Contains(echoed, "[200~hello-bracketed") {
		t.Errorf("paste reached the child without bracketing: %q", echoed)
	}
	if !strings.Contains(echoed, "[201~") {
		t.Errorf("paste was never terminated, so Enter stays part of it: %q", echoed)
	}
}

// Only the child gets to decide: an application that never set ?2004 would
// show the markers as text.
func TestHostPasteStaysRawWhenChildHasNotEnabledTheMode(t *testing.T) {
	h, sock := serveHost(t, HostConfig{SessionID: "paste-off", Command: "cat"})
	time.Sleep(300 * time.Millisecond)

	c := dialViewer(t, sock, "paste-off-viewer")
	if err := h.Paste("hello-plain", "test"); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	echoed := collectFrames(t, c, time.Second)
	if !strings.Contains(echoed, "hello-plain") {
		t.Fatalf("paste never reached the child: %q", echoed)
	}
	if strings.Contains(echoed, "[200~") {
		t.Errorf("bracketing markers sent to a child that never asked for them: %q", echoed)
	}
}

// Hosts outlive the daemon that spawned them, so an upgraded daemon talks to
// hosts from the previous build. Those have no case for FramePaste and drop it
// without a word, so sending one loses the prompt: the Enter that follows
// submits an empty composer and nothing reaches the agent.
func TestSendTextTypesAtAHostThatCannotPaste(t *testing.T) {
	_, sock := serveHost(t, HostConfig{
		SessionID: "legacy",
		Command:   "sh",
		Args:      []string{"-c", `printf '\033[?2004h'; cat`},
	})
	// No sidecar beside the socket is exactly what a pre-protocol host leaves.
	m, err := NewMirror("legacy", sock)
	if err != nil {
		t.Fatalf("NewMirror: %v", err)
	}
	defer m.Close()
	c := dialViewer(t, sock, "legacy-viewer")
	if !waitForFrameContaining(t, c, "\x1b[?2004h", 5*time.Second) {
		t.Fatal("child never enabled bracketed paste")
	}

	if err := m.SendText("legacy-prompt"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	echoed := collectFrames(t, c, time.Second)
	if !strings.Contains(echoed, "legacy-prompt") {
		t.Errorf("prompt never reached a host that cannot paste: %q", echoed)
	}
}

func TestSendTextPastesWhenTheHostAdvertisesIt(t *testing.T) {
	_, sock := serveHost(t, HostConfig{
		SessionID: "modern",
		Command:   "sh",
		Args:      []string{"-c", `printf '\033[?2004h'; cat`},
	})
	writeSidecarBeside(t, sock, Sidecar{SessionID: "modern", Protocol: HostProtocol})

	m, err := NewMirror("modern", sock)
	if err != nil {
		t.Fatalf("NewMirror: %v", err)
	}
	defer m.Close()
	c := dialViewer(t, sock, "modern-viewer")
	if !waitForFrameContaining(t, c, "\x1b[?2004h", 5*time.Second) {
		t.Fatal("child never enabled bracketed paste")
	}

	if err := m.SendText("modern-prompt"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	echoed := collectFrames(t, c, time.Second)
	if !strings.Contains(echoed, "[200~modern-prompt") {
		t.Errorf("host advertised paste but got plain input: %q", echoed)
	}
}

// The Enter waits for the application to finish ingesting the paste. A fixed
// pause cannot do this: it is either too short for a large prompt or dead time
// for a small one.
func TestSendTextHoldsEnterUntilTheScreenSettles(t *testing.T) {
	_, sock := serveHost(t, HostConfig{SessionID: "settle", Command: "cat"})
	m, err := NewMirror("settle", sock)
	if err != nil {
		t.Fatalf("NewMirror: %v", err)
	}
	defer m.Close()
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	if err := m.SendText("prompt-text"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < pasteQuiet {
		t.Errorf("Enter sent after %s, before the screen could settle (%s)", elapsed, pasteQuiet)
	}
	if elapsed > pasteSettleCap {
		t.Errorf("Enter sent after %s, past the %s ceiling", elapsed, pasteSettleCap)
	}
}

// A child that echoes nothing must not pay the whole settle window.
func TestSendTextDoesNotWaitOnASilentChild(t *testing.T) {
	_, sock := serveHost(t, HostConfig{
		SessionID: "silent",
		Command:   "sh",
		Args:      []string{"-c", `stty -echo; cat > /dev/null`},
	})
	m, err := NewMirror("silent", sock)
	if err != nil {
		t.Fatalf("NewMirror: %v", err)
	}
	defer m.Close()
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	if err := m.SendText("prompt-text"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if elapsed := time.Since(start); elapsed > pasteRenderWait+pasteQuiet {
		t.Errorf("waited %s for a child that renders nothing", elapsed)
	}
}

// collectFrames accumulates output across frames. Matching one frame at a
// time misses anything the host split across two, which a paste long enough to
// matter always is.
func collectFrames(t *testing.T, c *Client, d time.Duration) string {
	t.Helper()
	var b strings.Builder
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		f, err := c.Next()
		if err != nil {
			continue
		}
		if f.Type == FrameOutput || f.Type == FrameSnapshot {
			b.Write(f.Payload)
		}
	}
	return b.String()
}

// writeSidecarBeside puts a sidecar where the mirror looks for one: next to
// the socket, same basename.
func writeSidecarBeside(t *testing.T, sock string, s Sidecar) {
	t.Helper()
	s.Socket = sock
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	path := strings.TrimSuffix(sock, ".sock") + ".json"
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func dialViewer(t *testing.T, sock, name string) *Client {
	t.Helper()
	c, err := Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24, Name: name})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
