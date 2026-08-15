package terminal

import (
	"io"
	"strings"
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

// Timings for the Enter that submits a pasted prompt. The paste itself is
// unambiguous — the host brackets it — but the application still has to finish
// ingesting it, and an Enter that arrives mid-ingest is read as part of the
// paste and becomes a newline instead of a submit.
//
// So the Enter waits for the screen to stop changing rather than for a fixed
// interval: a 10 KB prompt takes far longer to render than a one-liner, and any
// constant that covers the first is dead time for the second. Measured failure
// rates for a 10 KB prompt are in docs/specs/37-prompt-delivery-reliability.md.
const (
	// How long the screen must hold still before the paste counts as ingested.
	pasteQuiet = 250 * time.Millisecond
	// How long to wait for the paste to start rendering. A child that echoes
	// nothing must not pay the whole settle window.
	pasteRenderWait = 300 * time.Millisecond
	// Ceiling on the whole wait, for an application that never goes quiet.
	pasteSettleCap  = 3 * time.Second
	pasteSettlePoll = 20 * time.Millisecond
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
	// What the host on the far end understands, read from its sidecar. A host
	// from an older build ignores frames it has no case for, so sending one
	// loses the prompt outright.
	protocol int

	screen *Screen
	client *Client

	mu             sync.Mutex
	onUpdate       func()
	onOverlayInput func([]byte)
	lastOutput     time.Time
	lastState      State
	lastViewers    int
	exitCode       int
	exited         bool

	closeOnce sync.Once
	done      chan struct{}
}

// NewMirror connects to a host socket and starts mirroring its screen.
func NewMirror(sessionID, socket string) (*Mirror, error) {
	client, err := Dial(socket, Hello{
		// Control, which is observer plus the right to drive overlays: the
		// daemon must never shrink a PTY the user is looking at, and it is the
		// one connection allowed to paint helios's own HITL over the session.
		Role: RoleControl,
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
		protocol:  hostProtocol(socket),
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
			m.markOutput()
			m.notify()
		case FrameSnapshot:
			if _, ansi, err := DecodeSnapshot(f.Payload); err == nil {
				m.screen.Write(ansi)
				m.markOutput()
				m.notify()
			}
		case FrameOverlayInput:
			m.mu.Lock()
			fn := m.onOverlayInput
			m.mu.Unlock()
			if fn != nil {
				fn(f.Payload)
			}
		case FrameStatus:
			if st, err := ParseStatus(f.Payload); err == nil {
				m.mu.Lock()
				m.lastState = st.State
				m.lastViewers = st.Viewers
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

func (m *Mirror) markOutput() {
	m.mu.Lock()
	m.lastOutput = time.Now()
	m.mu.Unlock()
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

// OnOverlayInput registers a callback for keystrokes an overlay captured from
// an attached terminal. It runs on the mirror's read goroutine, so it must not
// block.
func (m *Mirror) OnOverlayInput(fn func([]byte)) {
	m.mu.Lock()
	m.onOverlayInput = fn
	m.mu.Unlock()
}

// SetOverlay paints a modal over this session on every viewer.
func (m *Mirror) SetOverlay(o Overlay) error { return m.client.SetOverlay(o) }

// ClearOverlay takes the modal down and hands input back to the PTY.
func (m *Mirror) ClearOverlay() error { return m.client.ClearOverlay() }

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

// SendText submits a prompt: the text as a paste, then Enter.
//
// Delivered as bytes, so shell metacharacters, quotes, and newlines arrive
// exactly as typed. This is the defect that made tmux send-keys mangle
// multi-line prompts.
//
// The host pastes rather than types, which is what keeps the Enter separable
// from the prompt: sent as plain input, a large prompt is one indivisible
// burst to the application and the Enter at its tail is read as a newline
// within it. The submit is then lost with no error anywhere — the text simply
// waits in the composer until some later Enter flushes it.
//
// A host too old for FramePaste gets the text as plain input. It is the
// weaker delivery — the application has to guess where the paste ended — but
// the settle below carries most of the benefit, and a frame the host has no
// case for is discarded without a word, which loses the prompt entirely.
func (m *Mirror) SendText(text string) error {
	send := func() error { return m.client.Send([]byte(text)) }
	if m.protocol >= 1 {
		send = func() error { return m.client.Paste(text) }
	}
	if err := send(); err != nil {
		return err
	}
	m.settleAfterPaste()
	return m.client.Send([]byte("\r"))
}

// hostProtocol reads the sidecar beside a host's socket. Anything unreadable
// is treated as the oldest protocol: guessing high loses prompts, guessing low
// only forgoes bracketing.
func hostProtocol(socket string) int {
	s, err := ReadSidecar(strings.TrimSuffix(socket, ".sock") + ".json")
	if err != nil {
		return 0
	}
	return s.Protocol
}

// settleAfterPaste waits for the screen to stop changing, so the Enter that
// follows lands on an application that has finished taking the paste in.
func (m *Mirror) settleAfterPaste() {
	start := time.Now()
	for time.Since(start) < pasteRenderWait {
		m.mu.Lock()
		rendering := m.lastOutput.After(start)
		m.mu.Unlock()
		if rendering {
			break
		}
		time.Sleep(pasteSettlePoll)
	}
	for time.Since(start) < pasteSettleCap {
		m.mu.Lock()
		idle := time.Since(m.lastOutput)
		m.mu.Unlock()
		if idle >= pasteQuiet {
			return
		}
		time.Sleep(pasteSettlePoll)
	}
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

// Watched reports whether anyone other than this mirror is attached to the
// host, according to the last Status frame.
//
// The mirror is itself a viewer, so the host's count includes it and the
// interesting threshold is two, not one. Status frames are dropped when a
// viewer's queue is full (see broadcastStatus), so a stale false is possible;
// callers must treat this as a hint and never as a lock.
func (m *Mirror) Watched() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastViewers > 1
}

// Close drops the connection and stops mirroring.
func (m *Mirror) Close() {
	m.closeOnce.Do(func() {
		close(m.done)
		m.client.Close()
	})
}
