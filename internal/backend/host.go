package backend

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamrul1157024/helios/internal/terminal"
)

// Host drives sessions as per-session `helios ptyhost` processes.
//
// Each live session has one unix socket and one Mirror: the daemon's own
// emulator, fed by the same stream every other viewer receives. Reading the
// screen is therefore a method call instead of a subprocess, and writing input
// goes down the same connection, so nothing has to shell out per keystroke.
type Host struct {
	reg *terminal.Registry

	mu       sync.Mutex
	mirrors  map[string]*terminal.Mirror
	onUpdate func(sessionID string)

	// touched holds the last time each session's activity reached the
	// registry, as unix nanos. See markActive.
	touched sync.Map
}

// touchInterval bounds how often screen activity reaches the registry.
// Registry.Touch takes the pool lock and a redrawing TUI produces output many
// times a second; the LRU does not need that resolution.
const touchInterval = 5 * time.Second

// NewHost returns a Host backend over a warm-pool registry. The registry owns
// process lifetime; the backend owns the daemon's view of each warm session.
func NewHost(reg *terminal.Registry) *Host {
	h := &Host{
		reg:     reg,
		mirrors: make(map[string]*terminal.Mirror),
	}
	// The registry decides what to evict but cannot see who is watching; only
	// the mirrors know that.
	reg.InUse = h.watched
	return h
}

// watched reports whether a session has a viewer besides the daemon's own
// mirror. A session with no mirror has no viewers by definition.
func (h *Host) watched(sessionID string) bool {
	h.mu.Lock()
	m, ok := h.mirrors[sessionID]
	h.mu.Unlock()
	return ok && m.Watched()
}

// markActive pushes session activity into the registry's LRU, throttled to one
// update per touchInterval so a busy screen does not hammer the pool lock.
func (h *Host) markActive(sessionID string) {
	v, _ := h.touched.LoadOrStore(sessionID, new(atomic.Int64))
	last, ok := v.(*atomic.Int64)
	if !ok {
		return
	}
	now := time.Now().UnixNano()
	prev := last.Load()
	if now-prev < int64(touchInterval) {
		return
	}
	// Lose the race, skip the touch: whoever won it just did the same work.
	if !last.CompareAndSwap(prev, now) {
		return
	}
	h.reg.Touch(sessionID)
}

// Registry exposes the warm pool for the endpoints that report or tune it.
func (h *Host) Registry() *terminal.Registry { return h.reg }

// OnUpdate registers a callback fired whenever any session's screen changes.
// It runs on that mirror's read goroutine, so it must not block; the daemon
// uses it to notice trust prompts without polling.
func (h *Host) OnUpdate(fn func(sessionID string)) {
	h.mu.Lock()
	h.onUpdate = fn
	existing := make([]*terminal.Mirror, 0, len(h.mirrors))
	for _, m := range h.mirrors {
		existing = append(existing, m)
	}
	h.mu.Unlock()
	for _, m := range existing {
		h.bind(m)
	}
}

func (h *Host) bind(m *terminal.Mirror) {
	id := m.SessionID()
	m.OnUpdate(func() {
		// Output is the broadest activity signal there is: it covers the agent
		// working and the user typing, without either having to report in.
		h.markActive(id)
		h.mu.Lock()
		fn := h.onUpdate
		h.mu.Unlock()
		if fn != nil {
			fn(id)
		}
	})
}

// Mirror returns the daemon's live screen for a session, connecting on first
// use. It does not wake a cold session: callers that want that call Wake.
func (h *Host) Mirror(sessionID string) (*terminal.Mirror, error) {
	h.mu.Lock()
	m, ok := h.mirrors[sessionID]
	h.mu.Unlock()
	if ok {
		if exited, _ := m.Exited(); !exited {
			return m, nil
		}
		h.dropMirror(sessionID)
	}

	sock, ok := h.reg.Socket(sessionID)
	if !ok || !terminal.Probe(sock) {
		return nil, ErrNoTerminal
	}
	m, err := terminal.NewMirror(sessionID, sock)
	if err != nil {
		return nil, fmt.Errorf("mirror session %s: %w", sessionID, err)
	}
	h.bind(m)

	h.mu.Lock()
	// Another caller may have connected while we were dialing; keep one mirror
	// per session so the screen has a single writer.
	if prev, dup := h.mirrors[sessionID]; dup {
		h.mu.Unlock()
		m.Close()
		return prev, nil
	}
	h.mirrors[sessionID] = m
	h.mu.Unlock()
	return m, nil
}

// warmMirror connects a mirror opportunistically. It is used on paths where
// the session is already live and a mirror failure should not be reported as
// a failure to start it.
func (h *Host) warmMirror(sessionID string) {
	if _, err := h.Mirror(sessionID); err != nil {
		log.Printf("backend: mirror session %s: %v", sessionID, err)
	}
}

func (h *Host) dropMirror(sessionID string) {
	h.mu.Lock()
	m, ok := h.mirrors[sessionID]
	delete(h.mirrors, sessionID)
	h.mu.Unlock()
	h.touched.Delete(sessionID)
	if ok {
		m.Close()
	}
}

func (h *Host) Name() string { return "host" }

// Available is always true: the host backend needs no external server, which
// is the main reason it exists.
func (h *Host) Available() bool { return true }

func (h *Host) Start(sessionID, cwd string, argv []string) (string, error) {
	sock, err := h.reg.Start(sessionID, cwd, argv)
	if err != nil {
		return "", err
	}
	// Connect eagerly so output from the first moments of the session is
	// captured; a mirror attached later would miss it and rely on a snapshot.
	// A failure here is recoverable — every read path retries the dial — so it
	// must not fail a session that has already started.
	h.warmMirror(sessionID)
	return sock, nil
}

func (h *Host) Adopt(sessionID, handle, cwd string) error {
	if _, err := h.reg.Adopt(sessionID, cwd); err != nil {
		return err
	}
	if _, err := h.Mirror(sessionID); err != nil {
		return err
	}
	return nil
}

// Wake brings a cold session back with `claude --resume` and reports whether
// it had to start a host.
func (h *Host) Wake(sessionID, cwd string) (bool, error) {
	already := h.reg.IsWarm(sessionID)
	if _, err := h.reg.Wake(sessionID, cwd); err != nil {
		return false, err
	}
	h.warmMirror(sessionID)
	return !already, nil
}

// Endpoint returns the socket a viewer connects to.
func (h *Host) Endpoint(sessionID string) (string, bool) { return h.reg.Socket(sessionID) }

func (h *Host) Handle(sessionID string) (string, bool) { return h.reg.Socket(sessionID) }

func (h *Host) Alive(sessionID string) bool { return h.reg.IsWarm(sessionID) }

func (h *Host) Forget(sessionID string) {
	h.dropMirror(sessionID)
	h.reg.Forget(sessionID)
}

func (h *Host) Snapshot() map[string]string {
	out := make(map[string]string)
	for _, id := range h.reg.Warm() {
		if sock, ok := h.reg.Socket(id); ok {
			out[id] = sock
		}
	}
	return out
}

func (h *Host) SendText(sessionID, text string) error {
	m, err := h.Mirror(sessionID)
	if err != nil {
		return err
	}
	// Unthrottled: a prompt is a deliberate act and there is at most one every
	// few seconds, so it should move the LRU even if the screen stays quiet.
	h.reg.Touch(sessionID)
	return m.SendText(text)
}

func (h *Host) SendKey(sessionID string, key Key) error {
	seq, err := keySequence(key)
	if err != nil {
		return err
	}
	m, err := h.Mirror(sessionID)
	if err != nil {
		return err
	}
	h.reg.Touch(sessionID)
	return m.Send(seq)
}

// keySequence maps a named key to the bytes a terminal would send. Unlike
// tmux's send-keys vocabulary these go straight to the PTY, so there is no
// quoting layer to get wrong.
func keySequence(key Key) ([]byte, error) {
	switch key {
	case KeyEnter:
		return []byte("\r"), nil
	case KeyEscape:
		return []byte("\x1b"), nil
	case KeyCtrlC:
		return []byte("\x03"), nil
	case KeyUp:
		return []byte("\x1b[A"), nil
	case KeyDown:
		return []byte("\x1b[B"), nil
	default:
		return nil, fmt.Errorf("backend: unknown key %q", key)
	}
}

func (h *Host) Interrupt(sessionID string) error { return h.SendKey(sessionID, KeyEscape) }

func (h *Host) Kill(sessionID string) error {
	h.dropMirror(sessionID)
	return h.reg.Evict(sessionID)
}

// Capture returns the visible screen as text, with trailing blank lines
// trimmed so callers see the same shape tmux's capture-pane gave them.
func (h *Host) Capture(sessionID string) (string, error) {
	m, err := h.Mirror(sessionID)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(m.Text(), "\n"), nil
}

// Rename is a no-op: a host has no window title to set. Session names live in
// the database and are rendered by the TUI.
func (h *Host) Rename(sessionID, name string) error { return nil }

func (h *Host) Sweep() []string {
	before := h.reg.Warm()
	h.reg.Sweep()
	live := make(map[string]bool, len(before))
	for _, id := range h.reg.Warm() {
		live[id] = true
	}
	var dropped []string
	for _, id := range before {
		if !live[id] {
			dropped = append(dropped, id)
			h.dropMirror(id)
		}
	}
	return dropped
}

func (h *Host) Status() Status {
	return Status{
		Name:      "host",
		Available: true,
		Warm:      len(h.reg.Warm()),
	}
}

// Close drops every mirror. The hosts keep running: that is the point of them
// being separate processes.
func (h *Host) Close() {
	h.mu.Lock()
	mirrors := h.mirrors
	h.mirrors = make(map[string]*terminal.Mirror)
	h.mu.Unlock()
	for _, m := range mirrors {
		m.Close()
	}
}

var (
	_ Backend = (*Host)(nil)
	_ Waker   = (*Host)(nil)
	_ Viewer  = (*Host)(nil)
)
