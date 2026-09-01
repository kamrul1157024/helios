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

	mu             sync.Mutex
	mirrors        map[string]*terminal.Mirror
	onUpdate       func(sessionID string)
	onOverlayInput func(sessionID string, keys []byte)

	// touched holds the last time each session's activity reached the
	// registry, as unix nanos. See markActive.
	touched sync.Map

	// evicted holds the time each session's terminal was taken by Evict, so
	// the agent's exit hook can tell a session that went cold from one that
	// ended. See Evict.
	evicted sync.Map
}

// touchInterval bounds how often screen activity reaches the registry.
// Registry.Touch takes the pool lock and a redrawing TUI produces output many
// times a second; the ordering does not need that resolution.
const touchInterval = 5 * time.Second

// NewHost returns a Host backend over a warm-pool registry. The registry owns
// process lifetime; the backend owns the daemon's view of each warm session.
func NewHost(reg *terminal.Registry) *Host {
	h := &Host{
		reg:     reg,
		mirrors: make(map[string]*terminal.Mirror),
	}
	return h
}

// markActive records session activity in the registry, throttled to one
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
	m.OnOverlayInput(func(keys []byte) {
		h.mu.Lock()
		fn := h.onOverlayInput
		h.mu.Unlock()
		if fn != nil {
			fn(id, keys)
		}
	})
}

// OnOverlayInput registers a callback for keystrokes a session's overlay
// captured from a viewer. Like OnUpdate it is set once and covers every
// session, existing and future: the dispatcher installed by bind reads the
// callback each time rather than capturing it.
func (h *Host) OnOverlayInput(fn func(sessionID string, keys []byte)) {
	h.mu.Lock()
	h.onOverlayInput = fn
	h.mu.Unlock()
}

// SetOverlay paints helios's own modal over a session's terminal, on every
// viewer at once. It does not wake a cold session: there is nobody watching one
// to show a prompt to.
func (h *Host) SetOverlay(sessionID string, o terminal.Overlay) error {
	m, err := h.Mirror(sessionID)
	if err != nil {
		return err
	}
	return m.SetOverlay(o)
}

// OverlayProtocol reports what the host behind a session can draw. A session
// with no host reports 0, the oldest protocol: a prompt that needs more than
// that belongs on the phone rather than half-painted here.
func (h *Host) OverlayProtocol(sessionID string) int {
	m, err := h.Mirror(sessionID)
	if err != nil {
		return 0
	}
	return m.Protocol()
}

// ClearOverlay takes the modal down and hands the keyboard back to the agent.
func (h *Host) ClearOverlay(sessionID string) error {
	m, err := h.Mirror(sessionID)
	if err != nil {
		return err
	}
	return m.ClearOverlay()
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

// Handle returns the socket, and only while something is listening on it.
//
// The registry keeps an entry until the reaper sweeps it, twenty minutes after
// the host died, and clients read this field as "there is a terminal here": the
// desktop dials the path it is given and retries the ENOENT forever. Probing
// costs a connect to a local socket, which for a dead one fails immediately.
func (h *Host) Handle(sessionID string) (string, bool) {
	socket, ok := h.reg.Socket(sessionID)
	if !ok || !h.reg.IsWarm(sessionID) {
		return "", false
	}
	return socket, true
}

func (h *Host) Alive(sessionID string) bool { return h.reg.IsWarm(sessionID) }

func (h *Host) Forget(sessionID string) {
	h.dropMirror(sessionID)
	h.reg.Forget(sessionID)
}

// StartShell opens a login shell beside a session, in its directory.
func (h *Host) StartShell(parent, cwd string) (terminal.Terminal, error) {
	return h.reg.StartShell(parent, cwd)
}

// Terminals lists the live hosts a session owns: its agent, then its shells.
func (h *Host) Terminals(parent string) []terminal.Terminal {
	return h.reg.Terminals(parent)
}

// KillTerminal shuts one host down. Used for shells, which the user closes
// deliberately; an agent's host has its own lifecycle.
func (h *Host) KillTerminal(id string) error {
	h.dropMirror(id)
	return h.reg.Evict(id)
}

// KillShells reaps every shell a session owns, for when the session is gone.
func (h *Host) KillShells(parent string) {
	for _, t := range h.reg.Terminals(parent) {
		if t.Kind == "shell" {
			h.dropMirror(t.ID)
		}
	}
	h.reg.KillShells(parent)
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

// evictMarkTTL bounds how long an eviction mark is believed.
//
// The agent's exit hook is an HTTP call from a dying process, so it lands in a
// moment or not at all. The window only has to outlast that; holding the mark
// longer risks swallowing a genuine SessionEnd from a session the user really
// did quit soon afterwards.
const evictMarkTTL = 2 * time.Minute

// Evict takes a session's terminal, having first recorded that helios did it.
//
// Marked before the kill, not after: the agent's exit hook can reach the daemon
// while Kill is still returning.
func (h *Host) Evict(sessionID string) error {
	h.evicted.Store(sessionID, time.Now())
	if err := h.Kill(sessionID); err != nil {
		h.evicted.Delete(sessionID)
		return err
	}
	return nil
}

// EvictedRecently consumes the mark Evict left.
//
// Consumed rather than merely read: it answers one question, asked by the exit
// hook of the agent just stopped. Left set, a later and genuine end of the same
// session would be mistaken for this one.
func (h *Host) EvictedRecently(sessionID string) bool {
	at, ok := h.evicted.LoadAndDelete(sessionID)
	if !ok {
		return false
	}
	return time.Since(at.(time.Time)) < evictMarkTTL
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
	var rss int64
	for _, bytes := range h.reg.Usage() {
		rss += bytes
	}
	return Status{
		Name:      "host",
		Available: true,
		Warm:      len(h.reg.Warm()),
		WarmRSS:   rss,
	}
}

// Usage reports resident bytes per warm session.
func (h *Host) Usage() map[string]int64 { return h.reg.Usage() }

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

// StartWithEnv launches argv with extra environment. See backend.EnvStarter.
func (h *Host) StartWithEnv(sessionID, cwd string, argv []string, env map[string]string) (string, error) {
	sock, err := h.reg.StartWithEnv(sessionID, cwd, argv, env)
	if err != nil {
		return "", err
	}
	h.warmMirror(sessionID)
	return sock, nil
}
