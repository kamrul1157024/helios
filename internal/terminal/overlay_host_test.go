package terminal

import (
	"net"
	"strings"
	"testing"
	"time"
)

// overlaidHost starts a shell host with a control connection attached and an
// interactive viewer watching it — the shape every HITL prompt runs in.
func overlaidHost(t *testing.T, sessionID string) (h *Host, sock string, ctl, view *Client) {
	t.Helper()
	h, sock = serveHost(t, HostConfig{SessionID: sessionID, Command: "sh"})

	ctl, err := Dial(sock, Hello{Role: RoleControl, Cols: 200, Rows: 60, Name: "daemon"})
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { ctl.Close() })

	view, err = Dial(sock, Hello{Role: RoleInteractive, Cols: 80, Rows: 24, Name: "desktop"})
	if err != nil {
		t.Fatalf("dial viewer: %v", err)
	}
	t.Cleanup(func() { view.Close() })

	// The shell has to be up before an absent-output assertion means anything.
	if err := h.Write([]byte("echo shell-ready\r"), "test"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitForScreen(t, h, "shell-ready", 5*time.Second)
	return h, sock, ctl, view
}

// setOverlayAndWait installs a modal and blocks until the host has it, so a
// following keystroke cannot race the frame that captures it.
func setOverlayAndWait(t *testing.T, h *Host, c *Client, o Overlay) {
	t.Helper()
	if err := c.SetOverlay(o); err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}
	if !waitFor(3*time.Second, h.overlayActive) {
		t.Fatal("host never installed the overlay")
	}
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// nextFrameMatching reads frames until one satisfies want, or the window
// closes. It restores the connection's blocking behaviour on the way out.
func nextFrameMatching(t *testing.T, c *Client, d time.Duration, want func(Frame) bool) (Frame, bool) {
	t.Helper()
	defer c.conn.SetReadDeadline(time.Time{})
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		f, err := c.Next()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return Frame{}, false
		}
		if want(f) {
			return f, true
		}
	}
	return Frame{}, false
}

func carries(text string) func(Frame) bool {
	return func(f Frame) bool { return strings.Contains(string(f.Payload), text) }
}

func isType(typ FrameType) func(Frame) bool {
	return func(f Frame) bool { return f.Type == typ }
}

// assertScreenLacks gives the PTY time to echo, then asserts it did not.
func assertScreenLacks(t *testing.T, h *Host, text string) {
	t.Helper()
	time.Sleep(500 * time.Millisecond)
	if got := h.Screen().Text(); strings.Contains(got, text) {
		t.Errorf("%q reached the child; screen:\n%s", text, got)
	}
}

// TestHostOverlayPaintsViewersButNotTheControlConnection is the compositing
// contract. The daemon's mirror feeds Capture, which has to report what the
// agent drew rather than the box helios drew over it.
func TestHostOverlayPaintsViewersButNotTheControlConnection(t *testing.T) {
	h, _, ctl, view := overlaidHost(t, "ov-paint")

	setOverlayAndWait(t, h, ctl, Overlay{
		Title:   "Permission",
		Body:    []string{"Claude wants to run a command."},
		Options: []string{"Allow", "Deny"},
	})

	if _, ok := nextFrameMatching(t, view, 3*time.Second, carries("Permission")); !ok {
		t.Error("the interactive viewer never received the overlay")
	}
	if f, ok := nextFrameMatching(t, ctl, time.Second, carries("Permission")); ok {
		t.Errorf("the control connection was painted with the overlay: %q", f.Payload)
	}
}

// A viewer attaching to a session that is already waiting on a modal has to
// see it, or the phone shows a prompt the desktop does not.
//
// Which frame carries it depends on how the host catches the viewer up: a
// young session replays raw bytes, an older one renders a snapshot. The modal
// rides on whichever it is, so the test asks for the modal rather than for a
// particular frame type.
func TestHostOverlayReachesALateViewer(t *testing.T) {
	h, sock, ctl, _ := overlaidHost(t, "ov-snap")
	setOverlayAndWait(t, h, ctl, Overlay{Title: "Permission", Options: []string{"Allow", "Deny"}})

	late, err := Dial(sock, Hello{Role: RoleObserver, Cols: 80, Rows: 24, Name: "phone"})
	if err != nil {
		t.Fatalf("dial late viewer: %v", err)
	}
	defer late.Close()

	if _, ok := nextFrameMatching(t, late, 3*time.Second, carries("Permission")); !ok {
		t.Error("a viewer attaching mid-prompt never received the overlay")
	}
}

// The same catch-up must not paint the control connection, which is the
// connection that set the modal and reports what the agent drew.
func TestHostOverlayIsNotReplayedToTheControlConnection(t *testing.T) {
	h, sock, ctl, _ := overlaidHost(t, "ov-snap-ctl")
	setOverlayAndWait(t, h, ctl, Overlay{Title: "Permission", Options: []string{"Allow", "Deny"}})

	late, err := Dial(sock, Hello{Role: RoleControl, Cols: 80, Rows: 24, Name: "daemon-2"})
	if err != nil {
		t.Fatalf("dial late control: %v", err)
	}
	defer late.Close()

	if f, ok := nextFrameMatching(t, late, time.Second, carries("Permission")); ok {
		t.Errorf("the control connection was caught up with the overlay: %q", f.Payload)
	}
}

// TestHostOverlayDivertsInteractiveInput is the answer channel: while a modal
// is up the keys belong to helios, not to the blocked application.
func TestHostOverlayDivertsInteractiveInput(t *testing.T) {
	h, _, ctl, view := overlaidHost(t, "ov-input")
	setOverlayAndWait(t, h, ctl, Overlay{Title: "Permission", Options: []string{"Allow", "Deny"}})

	if err := view.Send([]byte("echo diverted\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	f, ok := nextFrameMatching(t, ctl, 3*time.Second, isType(FrameOverlayInput))
	if !ok {
		t.Fatal("the control connection never received the keystrokes")
	}
	if got := string(f.Payload); got != "echo diverted\r" {
		t.Errorf("overlay input = %q, want %q", got, "echo diverted\r")
	}
	assertScreenLacks(t, h, "diverted")
}

// An observer can see a modal but has no way to answer one, so its keystrokes
// go nowhere rather than typing into an application blocked behind the box.
func TestHostOverlaySwallowsObserverInput(t *testing.T) {
	h, sock, ctl, _ := overlaidHost(t, "ov-obs")

	obs, err := Dial(sock, Hello{Role: RoleObserver, Cols: 80, Rows: 24, Name: "phone"})
	if err != nil {
		t.Fatalf("dial observer: %v", err)
	}
	defer obs.Close()

	setOverlayAndWait(t, h, ctl, Overlay{Title: "Permission", Options: []string{"Allow", "Deny"}})
	if err := obs.Send([]byte("echo from-observer\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if f, ok := nextFrameMatching(t, ctl, time.Second, isType(FrameOverlayInput)); ok {
		t.Errorf("observer keystrokes were forwarded to control: %q", f.Payload)
	}
	assertScreenLacks(t, h, "from-observer")
}

// TestHostOverlayClearRepaintsAndReleasesInput covers the far side of the
// modal: the cells it covered get their real contents back and the keyboard
// goes back to the application.
func TestHostOverlayClearRepaintsAndReleasesInput(t *testing.T) {
	h, _, ctl, view := overlaidHost(t, "ov-clear")
	setOverlayAndWait(t, h, ctl, Overlay{Title: "Permission", Options: []string{"Allow", "Deny"}})

	if err := ctl.ClearOverlay(); err != nil {
		t.Fatalf("ClearOverlay: %v", err)
	}
	if !waitFor(3*time.Second, func() bool { return !h.overlayActive() }) {
		t.Fatal("host never cleared the overlay")
	}
	// The repaint carries the emulator's own view of the screen back, with the
	// cursor the overlay hid made visible again.
	if _, ok := nextFrameMatching(t, view, 3*time.Second, carries(cursorShow)); !ok {
		t.Error("clearing the overlay did not repaint the viewer")
	}

	if err := view.Send([]byte("echo released\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForScreen(t, h, "released", 5*time.Second)
}

// A daemon that dies mid-prompt must not leave a session swallowing every
// keystroke into a box nobody is listening to.
func TestHostOverlayClearedWhenControlDisconnects(t *testing.T) {
	h, _, ctl, view := overlaidHost(t, "ov-orphan")
	setOverlayAndWait(t, h, ctl, Overlay{Title: "Permission", Options: []string{"Allow", "Deny"}})

	if err := ctl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !waitFor(5*time.Second, func() bool { return !h.overlayActive() }) {
		t.Fatal("the overlay outlived its control connection")
	}

	if err := view.Send([]byte("echo orphan-released\r")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForScreen(t, h, "orphan-released", 5*time.Second)
}

// Only the control connection may paint over a session; a viewer that asks is
// ignored rather than trusted.
func TestHostIgnoresOverlayFromANonControlViewer(t *testing.T) {
	h, _, _, view := overlaidHost(t, "ov-authz")

	if err := view.SetOverlay(Overlay{Title: "Impostor"}); err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}
	// A round trip proves the frame was processed and discarded, not merely
	// still in flight.
	if err := view.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, ok := nextFrameMatching(t, view, 3*time.Second, isType(FramePong)); !ok {
		t.Fatal("no pong")
	}
	if h.overlayActive() {
		t.Error("a non-control viewer set an overlay")
	}
}

// The new fields have to survive the socket, not just the renderer: the daemon
// marshals them here and a separate ptyhost process is what draws them.
func TestHostOverlayCarriesDescriptionsCheckboxesAndTheField(t *testing.T) {
	h, _, ctl, view := overlaidHost(t, "ov-rich")

	setOverlayAndWait(t, h, ctl, Overlay{
		Title:    "Which checks to run",
		Options:  []string{"Unit tests", "Race detector"},
		Details:  []string{"go test across the daemon.", "Slow, but it finds the races."},
		Checked:  []bool{true, false},
		Input:    &OverlayInput{Label: "Other…", Value: "just the linter", Active: true},
		Selected: 2,
	})

	// One frame carries the whole box, so it is waited for once and then read.
	f, ok := nextFrameMatching(t, view, 3*time.Second, carries("Which checks to run"))
	if !ok {
		t.Fatal("the interactive viewer never received the overlay")
	}
	for _, want := range []string{
		"go test across the daemon.", // a description
		"[x] Unit tests",             // a ticked choice
		"[ ] Race detector",          // an unticked one
		"just the linter█",           // the open field, caret and all
	} {
		if !strings.Contains(string(f.Payload), want) {
			t.Errorf("the painted overlay is missing %q", want)
		}
	}
}
