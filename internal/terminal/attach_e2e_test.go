package terminal

import (
	"bytes"
	"context"
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

	ut.typeKeys(t, "echo attach-roundtrip\r")
	if !ut.waitForText("attach-roundtrip", 10*time.Second) {
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
