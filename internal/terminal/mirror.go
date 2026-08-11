package terminal

import (
	"io"
	"sync"
	"time"
)

// mirrorCols and mirrorRows size the daemon's local copy of a session's
// screen. A mirror connects as an observer, so these never influence the real
// PTY geometry — they only bound how much text the daemon can see at once.
const (
	mirrorCols = 200
	mirrorRows = 60
)

// Mirror is the daemon's live copy of one session's terminal.
//
// It replaces the 2s `capture-pane` poll: output arrives as it is produced and
// is fed straight into a local emulator, so "what is on screen right now" is a
// method call rather than a subprocess. The same connection carries input in
// the other direction, so the daemon needs exactly one socket per session.
//
// Screen content must be read through Text, never by matching the raw stream:
// Claude Code positions text with cursor-column jumps, so phrases do not
// appear contiguously in the bytes.
type Mirror struct {
	sessionID string
	socket    string

	screen *Screen
	client *Client

	mu        sync.Mutex
	onUpdate  func()
	lastState State
	exitCode  int
	exited    bool

	closeOnce sync.Once
	done      chan struct{}
}

// NewMirror connects to a host socket and starts mirroring its screen.
func NewMirror(sessionID, socket string) (*Mirror, error) {
	client, err := Dial(socket, Hello{
		// Observer, not interactive: the daemon must never shrink a PTY the
		// user is looking at.
		Role: RoleObserver,
		Cols: mirrorCols,
		Rows: mirrorRows,
		Name: "daemon",
	})
	if err != nil {
		return nil, err
	}
	m := &Mirror{
		sessionID: sessionID,
		socket:    socket,
		screen:    NewScreen(mirrorCols, mirrorRows),
		client:    client,
		done:      make(chan struct{}),
	}
	// The mirror never replies to the hosted application; the real viewers and
	// the host itself do that. The drain is still mandatory.
	m.screen.StartDrain(io.Discard)
	go m.pump()
	return m, nil
}

// pump feeds frames into the local emulator until the connection drops.
func (m *Mirror) pump() {
	defer m.screen.Close()
	for {
		select {
		case <-m.done:
			return
		default:
		}
		f, err := m.client.Next()
		if err != nil {
			m.markExited()
			return
		}
		switch f.Type {
		case FrameOutput:
			m.screen.Write(f.Payload)
			m.notify()
		case FrameSnapshot:
			if _, ansi, err := DecodeSnapshot(f.Payload); err == nil {
				m.screen.Write(ansi)
				m.notify()
			}
		case FrameStatus:
			if st, err := ParseStatus(f.Payload); err == nil {
				m.mu.Lock()
				m.lastState = st.State
				m.mu.Unlock()
			}
		case FrameExit:
			m.mu.Lock()
			m.exitCode = ParseExit(f.Payload)
			m.exited = true
			m.mu.Unlock()
			m.notify()
			return
		}
	}
}

func (m *Mirror) markExited() {
	m.mu.Lock()
	m.exited = true
	m.mu.Unlock()
	m.notify()
}

func (m *Mirror) notify() {
	m.mu.Lock()
	fn := m.onUpdate
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// OnUpdate registers a callback fired whenever the screen changes. It runs on
// the mirror's read goroutine, so it must not block.
func (m *Mirror) OnUpdate(fn func()) {
	m.mu.Lock()
	m.onUpdate = fn
	m.mu.Unlock()
}

// SessionID returns the session this mirror follows.
func (m *Mirror) SessionID() string { return m.sessionID }

// Socket returns the host socket path.
func (m *Mirror) Socket() string { return m.socket }

// Text returns the current screen as plain text.
func (m *Mirror) Text() string { return m.screen.Text() }

// Snapshot returns the current screen, with scrollback, as ANSI.
func (m *Mirror) Snapshot() string { return m.screen.RenderSnapshot(SnapshotScrollbackLines) }

// Send writes input bytes to the hosted process.
func (m *Mirror) Send(p []byte) error { return m.client.Send(p) }

// SendText submits a prompt: the text, then Enter.
//
// Written as bytes straight to the PTY, so shell metacharacters, quotes, and
// newlines arrive exactly as typed. This is the defect that made tmux
// send-keys mangle multi-line prompts.
func (m *Mirror) SendText(text string) error {
	if err := m.client.Send([]byte(text)); err != nil {
		return err
	}
	// A brief pause lets the application's input handler see the paste before
	// the submit, which matters for TUIs that debounce bracketed input.
	time.Sleep(30 * time.Millisecond)
	return m.client.Send([]byte("\r"))
}

// Exited reports whether the hosted process has ended.
func (m *Mirror) Exited() (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exited, m.exitCode
}

// State returns the last state the host reported.
func (m *Mirror) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastState
}

// Close drops the connection and stops mirroring.
func (m *Mirror) Close() {
	m.closeOnce.Do(func() {
		close(m.done)
		m.client.Close()
	})
}
