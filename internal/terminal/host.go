package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// viewerQueueDepth bounds how far a single viewer may fall behind before it is
// resynced from a snapshot. A slow viewer must never block the PTY reader.
const viewerQueueDepth = 64

// DefaultCols and DefaultRows are used when no interactive viewer has ever
// declared a size.
const (
	DefaultCols = 120
	DefaultRows = 40
)

// SessionEnv names the environment variable a host exports into its PTY,
// holding the session ID. Its presence marks a process as already living in a
// helios terminal.
const SessionEnv = "HELIOS_SESSION_ID"

// SnapshotScrollbackLines is how much history a resync carries. Claude Code
// renders inline rather than on the alternate screen, so a viewport-only
// snapshot would show a phone the last 40 rows of a long conversation and
// nothing before it.
const SnapshotScrollbackLines = 1000

// maxRawAttach caps the history a fresh viewer is given verbatim. Past this a
// rendered snapshot is both smaller and faster to draw, and the phone is the
// client that would feel the difference.
const maxRawAttach = 256 << 10

// HostConfig describes the process a Host supervises.
type HostConfig struct {
	SessionID string
	Command   string
	Args      []string
	Dir       string
	Env       []string
	Cols      int
	Rows      int
	RingSize  int
}

// Host owns one PTY-backed process and fans its output out to viewers.
type Host struct {
	cfg HostConfig

	ptmx *os.File
	cmd  *exec.Cmd

	ring   *Ring
	screen *Screen

	mu      sync.RWMutex
	viewers map[*viewer]struct{}
	state   State
	writer  string
	// overlay is the modal helios is painting over this session, if any, and
	// control is the viewer entitled to set it. See
	// docs/specs/36-helios-owned-hitl.md.
	overlay *Overlay
	control *viewer

	exited   chan struct{}
	exitCode atomic.Int32
	closeOne sync.Once
}

// outMsg is one queued frame. Every frame a host sends goes through this
// queue, including replies to pings: a connection has exactly one writer, so
// two goroutines can never interleave bytes on the wire.
//
// A nil payload on a Status frame means "render the current status when you
// get there", so a queued status is never stale by the time it is written.
type outMsg struct {
	typ     FrameType
	payload []byte
}

type viewer struct {
	id     uint64
	role   Role
	name   string
	cols   int
	rows   int
	conn   net.Conn
	out    chan outMsg
	closed chan struct{}
	once   sync.Once
}

func (v *viewer) close() { v.once.Do(func() { close(v.closed) }) }

// drop forces the connection shut so serveConn can unwind.
//
// close alone only stops the writer: the reader stays parked in ReadFrame until
// the peer hangs up, so a viewer the host gave up on would linger in the viewer
// set, keep voting on the PTY size and keep inflating the viewer count. Closing
// the socket is also what tells the client to reconnect and resync.
func (v *viewer) drop() {
	v.close()
	if v.conn != nil {
		v.conn.Close()
	}
}

// send enqueues a frame, reporting false if the viewer is too far behind. The
// caller drops such viewers back to a snapshot resync.
func (v *viewer) send(m outMsg) bool {
	select {
	case v.out <- m:
		return true
	case <-v.closed:
		return false
	default:
		return false
	}
}

var viewerIDs atomic.Uint64

// setEnv returns env with each KEY=VALUE applied, replacing any existing entry
// for that key.
//
// Appending a duplicate would not do: execve passes the list verbatim and
// getenv answers with the first match, so a stale inherited value would win
// over the one set here.
func setEnv(env []string, kvs ...string) []string {
	out := make([]string, 0, len(env)+len(kvs))
	for _, kv := range env {
		if !anyHasKeyOf(kvs, kv) {
			out = append(out, kv)
		}
	}
	return append(out, kvs...)
}

func anyHasKeyOf(kvs []string, kv string) bool {
	key, _, ok := strings.Cut(kv, "=")
	if !ok {
		return false
	}
	for _, candidate := range kvs {
		if ck, _, ok := strings.Cut(candidate, "="); ok && ck == key {
			return true
		}
	}
	return false
}

// NewHost starts the child on a PTY and begins pumping its output. The caller
// must call Close.
func NewHost(cfg HostConfig) (*Host, error) {
	if cfg.Cols <= 0 {
		cfg.Cols = DefaultCols
	}
	if cfg.Rows <= 0 {
		cfg.Rows = DefaultRows
	}
	if cfg.Command == "" {
		return nil, errors.New("terminal: no command")
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = setEnv(cmd.Env,
		"TERM=xterm-256color",
		fmt.Sprintf("COLUMNS=%d", cfg.Cols),
		fmt.Sprintf("LINES=%d", cfg.Rows),
		// Tells anything in here which session it belongs to. Starting a
		// session from inside another session's terminal is ordinary, so this
		// is routinely inherited from the parent and has to be overwritten.
		fmt.Sprintf("%s=%s", SessionEnv, cfg.SessionID),
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cfg.Cols), Rows: uint16(cfg.Rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty for session %s: %w", cfg.SessionID, err)
	}

	h := &Host{
		cfg:     cfg,
		ptmx:    ptmx,
		cmd:     cmd,
		ring:    NewRing(cfg.RingSize),
		screen:  NewScreen(cfg.Cols, cfg.Rows),
		viewers: make(map[*viewer]struct{}),
		state:   StateWarming,
		exited:  make(chan struct{}),
	}

	// Must start before the first byte reaches the emulator.
	h.screen.StartDrain(ptmx)
	go h.pump()

	return h, nil
}

// pump is the single reader of the PTY master. It writes to the ring and the
// emulator, then fans out to viewers.
func (h *Host) pump() {
	defer close(h.exited)
	buf := make([]byte, 32*1024)
	for {
		n, err := h.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			h.ring.Write(chunk)
			h.screen.Write(chunk)
			h.broadcast(chunk)

			h.mu.Lock()
			if h.state == StateWarming {
				h.state = StateReady
			}
			h.mu.Unlock()
		}
		if err != nil {
			break
		}
	}

	code := 0
	if err := h.cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	h.exitCode.Store(int32(code))

	h.mu.Lock()
	h.state = StateExited
	vs := make([]*viewer, 0, len(h.viewers))
	for v := range h.viewers {
		vs = append(vs, v)
	}
	h.mu.Unlock()
	for _, v := range vs {
		v.close()
	}
}

func (h *Host) broadcast(chunk []byte) {
	h.mu.RLock()
	vs := make([]*viewer, 0, len(h.viewers))
	for v := range h.viewers {
		vs = append(vs, v)
	}
	overlaid := h.overlay != nil
	h.mu.RUnlock()

	// Re-stamped in the same frame as the output it covers, so the two can
	// never arrive out of order and leave the box painted under a repaint.
	var stamped []byte
	if overlaid {
		if ov := h.overlayBytes(); ov != nil {
			stamped = append(append(make([]byte, 0, len(chunk)+len(ov)), chunk...), ov...)
		}
	}

	for _, v := range vs {
		payload := chunk
		// The control viewer drives the daemon's mirror, whose Capture has to
		// show the application's screen rather than helios's box drawn over it.
		if stamped != nil && v.role != RoleControl {
			payload = stamped
		}
		if !v.send(outMsg{typ: FrameOutput, payload: payload}) {
			// Too far behind: drop it, the connection loop resyncs.
			v.drop()
		}
	}
}

// overlayBytes renders the current modal at the current geometry, or nil when
// there is nothing to draw.
func (h *Host) overlayBytes() []byte {
	h.mu.RLock()
	o := h.overlay
	h.mu.RUnlock()
	if o == nil {
		return nil
	}
	cols, rows := h.screen.Size()
	return RenderOverlay(*o, cols, rows)
}

func (h *Host) overlayActive() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.overlay != nil
}

// setOverlay installs a modal and paints it on every viewer that renders one.
func (h *Host) setOverlay(o Overlay) {
	h.mu.Lock()
	h.overlay = &o
	h.mu.Unlock()
	h.stampOverlay()
}

// clearOverlay takes the modal down and repaints what it covered.
func (h *Host) clearOverlay() {
	h.mu.Lock()
	had := h.overlay != nil
	h.overlay = nil
	h.mu.Unlock()
	if had {
		h.repaint()
	}
}

// stampOverlay redraws the modal on every viewer showing one.
func (h *Host) stampOverlay() {
	if ov := h.overlayBytes(); ov != nil {
		h.sendRendered(ov)
	}
}

// repaint redraws the viewport from the emulator. This is how the cells an
// overlay covered get their real contents back: the emulator never saw the box,
// so its grid is already the truth.
func (h *Host) repaint() {
	h.sendRendered(append(ClearOverlayBytes(), []byte(h.screen.RenderANSI())...))
}

// sendRendered pushes helios-generated bytes to every viewer except the
// control connection, which must only ever see the application's own output.
func (h *Host) sendRendered(p []byte) {
	h.mu.RLock()
	vs := make([]*viewer, 0, len(h.viewers))
	for v := range h.viewers {
		if v.role != RoleControl {
			vs = append(vs, v)
		}
	}
	h.mu.RUnlock()

	for _, v := range vs {
		if !v.send(outMsg{typ: FrameOutput, payload: p}) {
			v.drop()
		}
	}
}

// captureInput diverts keystrokes to the control connection while a modal is
// up, reporting whether it consumed them.
//
// Observer input is swallowed rather than forwarded: an observer can see the
// modal but has no way to answer it, and letting those bytes through would type
// into a CLI that is blocked in a hook with its prompt hidden behind the box.
func (h *Host) captureInput(v *viewer, p []byte) bool {
	h.mu.RLock()
	active := h.overlay != nil
	ctl := h.control
	h.mu.RUnlock()

	if !active || v.role == RoleControl {
		return false
	}
	if v.role == RoleInteractive && ctl != nil {
		ctl.send(outMsg{typ: FrameOverlayInput, payload: append([]byte(nil), p...)})
	}
	return true
}

// controlViewer returns the connection currently entitled to set overlays.
func (h *Host) controlViewer() *viewer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.control
}

// Write sends input to the child. name is advisory and drives the writer
// indicator.
func (h *Host) Write(p []byte, name string) error {
	if len(p) == 0 {
		return nil
	}
	h.mu.Lock()
	h.writer = name
	h.mu.Unlock()
	if _, err := h.ptmx.Write(p); err != nil {
		return fmt.Errorf("write to pty: %w", err)
	}
	return nil
}

// Paste sends prompt text using bracketed paste if the child enabled it.
func (h *Host) Paste(text, name string) error {
	h.mu.Lock()
	h.writer = name
	h.mu.Unlock()
	h.screen.Paste(text)
	return nil
}

// Resize applies the negotiated size to both the PTY and the emulator.
func (h *Host) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	if err := pty.Setsize(h.ptmx, &pty.Winsize{
		Cols: uint16(cols), Rows: uint16(rows),
	}); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}
	h.screen.Resize(cols, rows)
	return nil
}

// negotiateSize returns the size the PTY should adopt: the smallest declared
// by any interactive viewer. Observers never shrink the PTY, so a phone
// cannot degrade a desktop terminal.
func (h *Host) negotiateSize() (cols, rows int, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cols, rows = 0, 0
	for v := range h.viewers {
		if v.role != RoleInteractive || v.cols <= 0 || v.rows <= 0 {
			continue
		}
		if cols == 0 || v.cols < cols {
			cols = v.cols
		}
		if rows == 0 || v.rows < rows {
			rows = v.rows
		}
	}
	return cols, rows, cols > 0 && rows > 0
}

func (h *Host) applyNegotiatedSize() {
	cols, rows, ok := h.negotiateSize()
	if !ok {
		return
	}
	curCols, curRows := h.screen.Size()
	if cols == curCols && rows == curRows {
		return
	}
	if err := h.Resize(cols, rows); err != nil {
		log.Printf("terminal: resize %s to %dx%d: %v", h.cfg.SessionID, cols, rows, err)
		return
	}
	// Every viewer needs the new geometry, not just the one that asked: a
	// second interactive viewer joining at a smaller size silently reflows the
	// PTY, and a client that does not hear about it renders at the wrong size.
	h.broadcastStatus()

	// And the screen at that geometry, because nothing else will produce one.
	// A resize reflows each viewer's own emulator, which for a full-screen
	// application means a grid of blanks until the application repaints — and
	// an idle agent does not repaint. On a phone the keyboard opening is a
	// resize, so opening the keyboard emptied the terminal and only leaving
	// the screen and coming back filled it in.
	//
	// After the status, so a viewer has adopted the new size before the
	// snapshot arrives: a snapshot is rows padded to this host's width and
	// ending in an absolute cursor position, and it only lands correctly on a
	// viewer of exactly these dimensions.
	h.broadcastSnapshot()

	// An application blocked in a hook will not redraw itself, so without this
	// the modal would sit at the old geometry until something else moved.
	if h.overlayActive() {
		h.repaint()
		h.stampOverlay()
	}
}

// broadcastSnapshot hands every viewer the screen as this host renders it now.
//
// Queued like output, so it lands in order with the bytes around it rather
// than racing them.
func (h *Host) broadcastSnapshot() {
	seq := h.ring.Seq()
	ansi := []byte(h.screen.RenderSnapshot(SnapshotScrollbackLines))

	// Read outside the lock: overlayBytes takes the same RLock, and taking a
	// read lock twice deadlocks the moment a writer is queued between them.
	overlay := h.overlayBytes()

	h.mu.RLock()
	vs := make([]*viewer, 0, len(h.viewers))
	for v := range h.viewers {
		vs = append(vs, v)
	}
	h.mu.RUnlock()

	for _, v := range vs {
		payload := ansi
		// The modal is composited on the way out and is not in the byte
		// stream, so a snapshot that replaces a viewer's screen has to carry
		// it — except to the control viewer, which draws it.
		if v.role != RoleControl && len(overlay) > 0 {
			payload = append(append([]byte(nil), ansi...), overlay...)
		}
		v.send(outMsg{typ: FrameSnapshot, payload: EncodeSnapshot(seq, payload)})
	}
}

// Status returns advisory state for the UI.
func (h *Host) Status() Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cols, rows := h.screen.Size()
	return Status{
		State:   h.state,
		Writer:  h.writer,
		Viewers: len(h.viewers),
		Cols:    cols,
		Rows:    rows,
	}
}

// SetState records a lifecycle transition. Turn boundaries come from hooks,
// not from scraping the screen.
func (h *Host) SetState(s State) {
	h.mu.Lock()
	if h.state != StateExited {
		h.state = s
	}
	h.mu.Unlock()
	h.broadcastStatus()
}

func (h *Host) broadcastStatus() {
	// Status rides the same queue as output so viewers see it in order.
	h.mu.RLock()
	vs := make([]*viewer, 0, len(h.viewers))
	for v := range h.viewers {
		vs = append(vs, v)
	}
	h.mu.RUnlock()
	for _, v := range vs {
		select {
		case v.out <- outMsg{typ: FrameStatus}:
		default:
		}
	}
}

// Screen exposes the emulator for trust-prompt matching and TUI rendering.
func (h *Host) Screen() *Screen { return h.screen }

// Ring exposes the raw output buffer.
func (h *Host) Ring() *Ring { return h.ring }

// Pid returns the child's process id, or 0.
func (h *Host) Pid() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// Exited returns a channel closed when the child exits.
func (h *Host) Exited() <-chan struct{} { return h.exited }

// ExitCode is valid once Exited is closed.
func (h *Host) ExitCode() int { return int(h.exitCode.Load()) }

// Close terminates the child and releases the PTY.
func (h *Host) Close() error {
	h.closeOne.Do(func() {
		if h.cmd.Process != nil {
			// Signal the whole foreground group; Claude Code spawns helpers.
			h.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-h.exited:
			case <-time.After(3 * time.Second):
				h.cmd.Process.Kill()
			}
		}
		h.screen.Close()
		h.ptmx.Close()
	})
	return nil
}

// Serve accepts viewer connections on ln until ctx is cancelled or the child
// exits.
func (h *Host) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		select {
		case <-ctx.Done():
		case <-h.exited:
		}
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-h.exited:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept viewer: %w", err)
		}
		go h.serveConn(conn)
	}
}

// serveConn runs one viewer connection: handshake, snapshot, then a writer
// pump and a reader loop.
func (h *Host) serveConn(conn net.Conn) {
	defer conn.Close()

	f, err := ReadFrame(conn)
	if err != nil || f.Type != FrameHello {
		return
	}
	var hello Hello
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &hello); err != nil {
			return
		}
	}
	switch hello.Role {
	case RoleInteractive, RoleObserver, RoleControl:
	default:
		hello.Role = RoleObserver
	}

	v := &viewer{
		id:     viewerIDs.Add(1),
		role:   hello.Role,
		name:   hello.Name,
		cols:   hello.Cols,
		rows:   hello.Rows,
		conn:   conn,
		out:    make(chan outMsg, viewerQueueDepth),
		closed: make(chan struct{}),
	}

	h.mu.Lock()
	h.viewers[v] = struct{}{}
	if v.role == RoleControl {
		// Last control connection wins. A daemon that restarts and reconnects
		// must not be locked out by the corpse of its previous connection.
		h.control = v
	}
	h.mu.Unlock()
	h.applyNegotiatedSize()

	defer func() {
		h.mu.Lock()
		delete(h.viewers, v)
		orphaned := h.control == v && h.overlay != nil
		if h.control == v {
			h.control = nil
		}
		h.mu.Unlock()
		v.close()
		// Nobody is left to answer the modal, and its keystrokes have nowhere
		// to go. Take it down so the session stays usable rather than
		// swallowing input from a terminal that looks alive.
		if orphaned {
			h.clearOverlay()
		}
		h.applyNegotiatedSize()
	}()

	// Catch the viewer up. Replay from the ring when its requested sequence is
	// still retained; otherwise send a full snapshot rather than a gap.
	// Reading the sequence before rendering means a concurrent write is
	// replayed rather than lost.
	replayed := false
	if hello.Since > 0 {
		if data, _, ok := h.ring.Since(hello.Since); ok {
			if err := WriteFrame(conn, FrameOutput, data); err != nil {
				return
			}
			replayed = true
		}
	} else if data, _, ok := h.ring.Since(0); ok && len(data) <= maxRawAttach {
		// Everything the child has written, verbatim, for a viewer joining
		// from nothing.
		//
		// A snapshot is a screen this host rendered: rows padded to its width,
		// ending in an absolute cursor position computed here. It only lands
		// correctly on a viewer of exactly these dimensions, and a shell's
		// line editor then moves relative to a cursor it did not put there.
		// Raw bytes carry no such assumption — the viewer's own emulator
		// derives the screen the way it would have had it been attached since
		// the first byte.
		//
		// Since(0) succeeds only while nothing has been evicted, so this is
		// the recently started session; anything older resyncs from a
		// snapshot as before.
		if len(data) > 0 {
			if err := WriteFrame(conn, FrameOutput, data); err != nil {
				return
			}
		}
		replayed = true
	}
	if !replayed {
		seq := h.ring.Seq()
		ansi := []byte(h.screen.RenderSnapshot(SnapshotScrollbackLines))
		// Part of the snapshot, not a separate broadcast: a viewer that
		// attaches to a session already waiting on a modal has to see it.
		if v.role != RoleControl {
			ansi = append(ansi, h.overlayBytes()...)
		}
		if err := WriteFrame(conn, FrameSnapshot, EncodeSnapshot(seq, ansi)); err != nil {
			return
		}
	}
	// A replay carries what the child wrote, and the modal is not in that
	// stream: it is composited on the way out and never reaches the emulator.
	// Paint it after the catch-up so a viewer joining mid-prompt sees it
	// whichever path caught it up, matching what the snapshot already does.
	if replayed && v.role != RoleControl {
		if ov := h.overlayBytes(); ov != nil {
			if err := WriteFrame(conn, FrameOutput, ov); err != nil {
				return
			}
		}
	}
	if err := WriteJSONFrame(conn, FrameStatus, h.Status()); err != nil {
		return
	}

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case m, ok := <-v.out:
				if !ok {
					return
				}
				if m.typ == FrameStatus && m.payload == nil {
					if err := WriteJSONFrame(conn, FrameStatus, h.Status()); err != nil {
						return
					}
					continue
				}
				if err := WriteFrame(conn, m.typ, m.payload); err != nil {
					return
				}
			case <-v.closed:
				// Flush the exit frame on a clean child exit.
				select {
				case <-h.exited:
					WriteFrame(conn, FrameExit, encodeInt32(int32(h.ExitCode())))
				default:
				}
				return
			}
		}
	}()

	for {
		fr, err := ReadFrame(conn)
		if err != nil {
			break
		}
		switch fr.Type {
		case FrameInput:
			if h.captureInput(v, fr.Payload) {
				continue
			}
			if err := h.Write(fr.Payload, v.name); err != nil {
				break
			}
		case FramePaste:
			if h.captureInput(v, fr.Payload) {
				continue
			}
			if err := h.Paste(string(fr.Payload), v.name); err != nil {
				break
			}
		case FrameOverlaySet:
			if h.controlViewer() != v {
				continue
			}
			o, err := ParseOverlay(fr.Payload)
			if err != nil {
				log.Printf("terminal: overlay for %s: %v", h.cfg.SessionID, err)
				continue
			}
			h.setOverlay(o)
		case FrameOverlayClear:
			if h.controlViewer() != v {
				continue
			}
			h.clearOverlay()
		case FrameResize:
			cols, rows, err := DecodeResize(fr.Payload)
			if err != nil {
				continue
			}
			if v.role != RoleInteractive {
				continue
			}
			h.mu.Lock()
			v.cols, v.rows = cols, rows
			h.mu.Unlock()
			h.applyNegotiatedSize()
		case FramePing:
			// Queued rather than written here, so the reply cannot overtake
			// frames already in flight and cannot race the writer goroutine.
			v.send(outMsg{typ: FramePong})
		}
	}

	v.close()
	<-writeDone
}

func encodeInt32(v int32) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func decodeInt32(b []byte) int32 {
	if len(b) < 4 {
		return 0
	}
	return int32(b[0])<<24 | int32(b[1])<<16 | int32(b[2])<<8 | int32(b[3])
}
