package terminal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// userTerminal is a real PTY standing in for the user's terminal window, so
// Attach's raw mode, size probing and SIGWINCH handling run for real rather
// than being bypassed by a pipe.
type userTerminal struct {
	master *os.File
	slave  *os.File

	mu   sync.Mutex
	seen bytes.Buffer
}

func newUserTerminal(t *testing.T, cols, rows int) *userTerminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	if err := pty.Setsize(slave, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		t.Fatalf("set size: %v", err)
	}
	ut := &userTerminal{master: master, slave: slave}
	t.Cleanup(func() { master.Close(); slave.Close() })

	// Drain continuously: a full PTY buffer would block the host's writes and
	// stall the session rather than fail the test.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				ut.mu.Lock()
				ut.seen.Write(buf[:n])
				ut.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return ut
}

// typeKeys feeds bytes as if the user pressed them.
func (u *userTerminal) typeKeys(t *testing.T, s string) {
	t.Helper()
	if _, err := u.master.WriteString(s); err != nil {
		t.Fatalf("type %q: %v", s, err)
	}
}

// runEcho types `echo <word>` and waits for the word to come back as output.
//
// The word is typed in two quoted halves so that it appears whole *only* in the
// command's output. Typing it plainly, the terminal's own line-discipline echo
// puts it on screen before anything has read it — and waiting on that is
// waiting on nothing: the attach may not have switched the pty to raw yet, so
// the line sits in the canonical buffer and is delivered much later, after the
// test has moved on to set an overlay or press the detach key. That is the race
// behind two long-standing flakes in this file.
//
// Waiting for the output instead means the bytes reached the host, the shell
// ran them, and the reply came back — an ordering barrier rather than a guess.
func (u *userTerminal) runEcho(t *testing.T, word string, d time.Duration) bool {
	t.Helper()
	half := len(word) / 2
	u.typeKeys(t, fmt.Sprintf("echo '%s''%s'\r", word[:half], word[half:]))
	return u.waitForText(word, d)
}

func (u *userTerminal) waitForText(want string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		u.mu.Lock()
		got := u.seen.String()
		u.mu.Unlock()
		if bytes.Contains([]byte(got), []byte(want)) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestE2EAttachRoundTripsAndDetaches is the terminal half of the handoff
// claim: a local attach drives the same session a phone would, and detaching
// leaves it running rather than killing it.
func TestE2EAttachRoundTripsAndDetaches(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-attach"
	e.spawnHost(t, sid, "sh")

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("ptyhost socket never appeared")
	}

	ut := newUserTerminal(t, 90, 30)
	done := make(chan AttachResult, 1)
	errc := make(chan error, 1)
	go func() {
		res, err := Attach(context.Background(), AttachConfig{
			Socket: sock, Name: "e2e-attach", In: ut.slave, Out: ut.slave,
		})
		errc <- err
		done <- res
	}()

	if !ut.runEcho(t, "attach-roundtrip", 10*time.Second) {
		t.Fatal("keystrokes did not round-trip through attach")
	}

	// An observer proves the session is shared, and outlives the detach below.
	obs, err := Dial(sock, Hello{Role: RoleObserver, Cols: 90, Rows: 30, Name: "phone"})
	if err != nil {
		t.Fatalf("observer dial: %v", err)
	}
	defer obs.Close()

	ut.typeKeys(t, string(DefaultDetachKey)+"d")

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("attach returned an error: %v", err)
		}
		if res := <-done; !res.Detached {
			t.Fatalf("attach ended without detaching: %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach key did not end the attach")
	}

	// The session must still be alive and driveable from the other viewer:
	// detaching is not quitting.
	if err := obs.Send([]byte("echo still-alive\r")); err != nil {
		t.Fatalf("observer send after detach: %v", err)
	}
	if !waitForFrameContaining(t, obs, "still-alive", 10*time.Second) {
		t.Error("session did not survive the detach")
	}
}

// TestE2EAttachResizesThePty covers the interactive viewer's size vote: the
// window the user attached from is what the agent renders for.
func TestE2EAttachResizesThePty(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-attach-size"
	e.spawnHost(t, sid, "sh")

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("ptyhost socket never appeared")
	}

	const cols, rows = 113, 41
	ut := newUserTerminal(t, cols, rows)
	errc := make(chan error, 1)
	go func() {
		_, err := Attach(context.Background(), AttachConfig{
			Socket: sock, Name: "e2e-size", In: ut.slave, Out: ut.slave,
		})
		errc <- err
	}()

	// Ask the shell itself, so this measures the PTY the child sees rather
	// than any bookkeeping on our side.
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-errc:
			// Without this the test would sit out the full timeout before
			// reporting what is really a failed attach.
			t.Fatalf("attach ended early: %v", err)
		default:
		}
		ut.typeKeys(t, "stty size\r")
		if ut.waitForText("41 113", 1*time.Second) {
			return
		}
		if time.Now().After(deadline) {
			ut.mu.Lock()
			got := ut.seen.String()
			ut.mu.Unlock()
			t.Fatalf("pty never resized to %dx%d; terminal saw:\n%s", cols, rows, got)
		}
	}
}

// TestE2EOverlayOnAnAttachedTerminal is step one of
// docs/specs/36-helios-owned-hitl.md against real processes: helios paints its
// own modal over a session the user is attached to, the keys that follow go to
// the daemon rather than to the application blocked behind it, and clearing
// hands the terminal back.
func TestE2EOverlayOnAnAttachedTerminal(t *testing.T) {
	e := newE2E(t)
	const sid = "e2e-overlay"
	e.spawnHost(t, sid, "sh")

	sock := e.socket(sid)
	if !WaitForSocket(sock, 20*time.Second) {
		t.Fatal("ptyhost socket never appeared")
	}

	ut := newUserTerminal(t, 90, 30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Attach(ctx, AttachConfig{
		Socket: sock, Name: "e2e-overlay", In: ut.slave, Out: ut.slave,
	})

	if !ut.runEcho(t, "before-overlay", 10*time.Second) {
		t.Fatal("the attach never reached the shell")
	}

	ctl, err := Dial(sock, Hello{Role: RoleControl, Cols: 200, Rows: 60, Name: "daemon"})
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	defer ctl.Close()

	if err := ctl.SetOverlay(Overlay{
		Title:   "Permission",
		Body:    []string{"Claude wants to run `ls`."},
		Options: []string{"Allow once", "Deny"},
		Footer:  "↑↓ select · enter confirm",
	}); err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}
	if !ut.waitForText("Allow once", 10*time.Second) {
		t.Fatal("the overlay never reached the attached terminal")
	}

	ut.typeKeys(t, "\x1b[B")
	f, ok := nextFrameMatching(t, ctl, 10*time.Second, isType(FrameOverlayInput))
	if !ok {
		t.Fatal("the arrow key never came back to the control connection")
	}
	if got := string(f.Payload); got != "\x1b[B" {
		t.Errorf("overlay input = %q, want a down arrow", got)
	}

	if err := ctl.ClearOverlay(); err != nil {
		t.Fatalf("ClearOverlay: %v", err)
	}
	// Proof the keyboard went back to the shell, not just that the box left.
	if !ut.runEcho(t, "after-overlay", 10*time.Second) {
		t.Error("input never returned to the child after the overlay cleared")
	}
}
