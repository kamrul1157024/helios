package terminal

import (
	"fmt"
	"io"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Screen wraps a vt emulator with the plumbing the host requires: the reply
// drain, and rendering the grid back to ANSI.
//
// The reply drain is not optional. The emulator answers cursor-position and
// device-attribute queries over an unbuffered pipe exposed by Read; if nobody
// drains it, the first query that generates a reply blocks the emulator
// forever and the session deadlocks. See the spec's "Emulator reply channel".
//
// mu guards the emulator as well as the dimensions, and every render holds it
// for its whole duration. SafeEmulator's per-call locking is not sufficient
// on its own: CellAt returns a pointer into the live cell buffer, so reading
// the cell's contents after the call has returned races a concurrent Write.
// Writes come from a PTY or socket pump while reads come from HTTP handlers
// and the trust watcher, so this is a routine collision, not a corner case.
type Screen struct {
	mu   sync.Mutex
	em   *vt.SafeEmulator
	cols int
	rows int

	drainOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

// NewScreen returns a Screen of the given size. Call StartDrain before the
// first Write.
func NewScreen(cols, rows int) *Screen {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return &Screen{
		em:   vt.NewSafeEmulator(cols, rows),
		cols: cols,
		rows: rows,
		done: make(chan struct{}),
	}
}

// StartDrain begins forwarding emulator replies to w, which must be the PTY
// master. It is safe to call more than once; only the first takes effect.
func (s *Screen) StartDrain(w io.Writer) {
	s.drainOnce.Do(func() {
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := s.em.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
				select {
				case <-s.done:
					return
				default:
				}
			}
		}()
	})
}

// Write feeds PTY output into the emulator.
//
// The drain goroutine deliberately does not take mu: the emulator's reply pipe
// must stay readable while a write is in progress, or a write that generates a
// reply would deadlock against its own drain.
func (s *Screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.em.Write(p)
}

// Resize changes the emulator grid. The caller is responsible for resizing
// the PTY itself so the child sees SIGWINCH.
func (s *Screen) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = cols, rows
	s.em.Resize(cols, rows)
}

// Size returns the current grid dimensions.
func (s *Screen) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// Paste writes text using bracketed paste when the application has enabled
// DECSET 2004, and as plain text otherwise. Hand-wrapping the markers would
// send literal escape bytes to an application that never opted in.
func (s *Screen) Paste(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.em.Paste(text)
}

// CursorPosition returns the cursor's column and row.
func (s *Screen) CursorPosition() (col, row int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursorPosition()
}

func (s *Screen) cursorPosition() (col, row int) {
	p := s.em.CursorPosition()
	return p.X, p.Y
}

// IsAltScreen reports whether the alternate screen buffer is active.
func (s *Screen) IsAltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.em.IsAltScreen()
}

// Close stops the drain goroutine.
func (s *Screen) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Text renders the grid as plain text, one line per row, trailing spaces
// trimmed. Intended for diagnostics and trust-prompt matching, not display.
func (s *Screen) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	cols, rows := s.cols, s.rows
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		var line strings.Builder
		for x := 0; x < cols; x++ {
			if c := s.em.CellAt(x, y); c != nil && c.Content != "" {
				line.WriteString(c.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ScrollbackLen returns the number of lines that have scrolled off the grid.
func (s *Screen) ScrollbackLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.em.ScrollbackLen()
}

// SetScrollbackSize bounds how many scrolled-off lines are retained.
func (s *Screen) SetScrollbackSize(lines int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.em.SetScrollbackSize(lines)
}

// RenderANSI renders the visible grid as a styled ANSI string, suitable for
// embedding in a Bubble Tea view. It does not include scrollback; use
// RenderSnapshot for a resync.
//
// Styles are emitted as deltas between adjacent cells rather than reset per
// cell, which is what keeps a full frame cheap.
func (s *Screen) RenderANSI() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	cols, rows := s.cols, s.rows
	var sb strings.Builder
	// Home the cursor and clear, so the viewer's terminal starts from a known
	// state rather than inheriting whatever was there.
	sb.WriteString("\x1b[H\x1b[2J")

	var cur uv.Style
	for y := 0; y < rows; y++ {
		if y > 0 {
			sb.WriteString("\r\n")
		}
		s.renderRow(&sb, &cur, cols, y, false)
	}
	if !cur.Equal(&uv.Style{}) {
		sb.WriteString("\x1b[m")
	}

	// Restore the cursor where the application left it.
	col, row := s.cursorPosition()
	fmt.Fprintf(&sb, "\x1b[%d;%dH", row+1, col+1)
	return sb.String()
}

// RenderSnapshot renders scrollback followed by the visible grid, for a viewer
// joining an existing session.
//
// Scrollback is not a nicety here. Claude Code renders inline rather than on
// the alternate screen, so by the time a phone connects to a running session
// most of the conversation has scrolled off the grid. A viewport-only snapshot
// would show the last few rows and nothing else.
//
// The scrolled-off lines are replayed as ordinary output, so they land in the
// receiving emulator's own scrollback exactly as they did in ours.
func (s *Screen) RenderSnapshot(scrollbackLines int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	cols, rows := s.cols, s.rows
	if scrollbackLines < 0 {
		scrollbackLines = 0
	}

	var sb strings.Builder
	// 3J clears the receiver's scrollback as well, so replaying a snapshot
	// twice does not stack two copies of the history.
	sb.WriteString("\x1b[H\x1b[3J\x1b[2J")

	var cur uv.Style
	total := s.em.ScrollbackLen()
	start := total - scrollbackLines
	if start < 0 {
		start = 0
	}
	for y := start; y < total; y++ {
		s.renderRow(&sb, &cur, cols, y, true)
		sb.WriteString("\r\n")
	}
	for y := 0; y < rows; y++ {
		if y > 0 {
			sb.WriteString("\r\n")
		}
		s.renderRow(&sb, &cur, cols, y, false)
	}
	if !cur.Equal(&uv.Style{}) {
		sb.WriteString("\x1b[m")
	}

	col, row := s.cursorPosition()
	fmt.Fprintf(&sb, "\x1b[%d;%dH", row+1, col+1)
	return sb.String()
}

// renderRow writes one row of cells, carrying the running style across calls.
// The caller must hold mu: the cells it dereferences point into the live
// emulator buffer.
// Scrollback rows are right-trimmed: padding them to the full width would both
// bloat the snapshot and risk a spurious wrap on a terminal exactly cols wide.
func (s *Screen) renderRow(sb *strings.Builder, cur *uv.Style, cols, y int, scrollback bool) {
	end := cols
	if scrollback {
		for end > 0 {
			c := s.em.ScrollbackCellAt(end-1, y)
			if c != nil && strings.TrimSpace(c.Content) != "" {
				break
			}
			end--
		}
	}
	for x := 0; x < end; x++ {
		var c *uv.Cell
		if scrollback {
			c = s.em.ScrollbackCellAt(x, y)
		} else {
			c = s.em.CellAt(x, y)
		}
		if c == nil {
			if !cur.Equal(&uv.Style{}) {
				sb.WriteString("\x1b[m")
				*cur = uv.Style{}
			}
			sb.WriteByte(' ')
			continue
		}
		if !c.Style.Equal(cur) {
			sb.WriteString(c.Style.Diff(cur))
			*cur = c.Style
		}
		if c.Content == "" {
			sb.WriteByte(' ')
		} else {
			sb.WriteString(c.Content)
		}
	}
}
