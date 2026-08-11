package terminal

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is an io.Writer safe for the drain goroutine to write to.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestScreenTextAndResize(t *testing.T) {
	s := NewScreen(20, 5)
	defer s.Close()
	s.StartDrain(&syncBuf{})

	s.Write([]byte("hello"))
	if !strings.Contains(s.Text(), "hello") {
		t.Errorf("Text() missing content:\n%s", s.Text())
	}

	s.Resize(40, 10)
	if c, r := s.Size(); c != 40 || r != 10 {
		t.Errorf("Size() = %dx%d, want 40x10", c, r)
	}
}

// TestScreenDrainPreventsDeadlock is a regression guard, not a happy path.
// Without a drain the emulator blocks forever on its first reply and the
// whole session wedges, so this must be timeout-guarded.
func TestScreenDrainPreventsDeadlock(t *testing.T) {
	s := NewScreen(80, 24)
	defer s.Close()
	out := &syncBuf{}
	s.StartDrain(out)

	// Enable bracketed paste, then paste. Both generate emulator replies.
	s.Write([]byte("\x1b[?2004h"))

	done := make(chan struct{})
	go func() {
		s.Paste("hello")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Paste deadlocked: the reply drain is not working")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "hello") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := out.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("paste did not reach the writer, got %q", got)
	}
	// With DECSET 2004 on, the paste must be bracketed.
	if !strings.Contains(got, "\x1b[200~") {
		t.Errorf("expected bracketed paste markers, got %q", got)
	}
}

func TestScreenPasteUnbracketedWhenDisabled(t *testing.T) {
	s := NewScreen(80, 24)
	defer s.Close()
	out := &syncBuf{}
	s.StartDrain(out)

	done := make(chan struct{})
	go func() { s.Paste("plain"); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Paste deadlocked with 2004 off")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "plain") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := out.String(); strings.Contains(got, "\x1b[200~") {
		t.Errorf("must not bracket a paste the app never opted into: %q", got)
	}
}

func TestScreenAnswersCursorPositionQuery(t *testing.T) {
	s := NewScreen(80, 24)
	defer s.Close()
	out := &syncBuf{}
	s.StartDrain(out)

	s.Write([]byte("\x1b[6n")) // DSR, which Claude Code sends at startup

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "R") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := out.String(); !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, "R") {
		t.Errorf("DSR reply = %q, want a CSI ... R report", got)
	}
}

func TestScreenRenderANSIRoundTrip(t *testing.T) {
	s := NewScreen(20, 3)
	defer s.Close()
	s.StartDrain(&syncBuf{})
	s.Write([]byte("A\x1b[31mR\x1b[0mB"))

	out := s.RenderANSI()
	if !strings.Contains(out, "A") || !strings.Contains(out, "R") || !strings.Contains(out, "B") {
		t.Errorf("rendered output lost content: %q", out)
	}
	// The colour must survive as an SGR sequence, not be flattened away.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected SGR sequences in rendered output: %q", out)
	}

	// Feeding the render back into a fresh emulator must reproduce the text.
	s2 := NewScreen(20, 3)
	defer s2.Close()
	s2.StartDrain(&syncBuf{})
	s2.Write([]byte(out))
	if got, want := strings.TrimSpace(s2.Text()), strings.TrimSpace(s.Text()); got != want {
		t.Errorf("snapshot round trip mismatch:\ngot  %q\nwant %q", got, want)
	}
}

// TestScreenSnapshotCarriesScrollback guards the resync path for inline TUIs.
// Claude Code does not use the alternate screen, so history scrolls off the
// grid; a snapshot that only carried the viewport would show a late-joining
// viewer the last few rows of a conversation and nothing before them.
func TestScreenSnapshotCarriesScrollback(t *testing.T) {
	s := NewScreen(40, 5)
	defer s.Close()
	s.StartDrain(&syncBuf{})

	// Twenty lines through a five-row grid: fifteen must end up in scrollback.
	for i := 0; i < 20; i++ {
		fmt.Fprintf(s, "line-%02d\r\n", i)
	}
	if strings.Contains(s.Text(), "line-00") {
		t.Fatal("precondition failed: line-00 should have scrolled off the grid")
	}
	if s.ScrollbackLen() == 0 {
		t.Fatal("precondition failed: nothing in scrollback")
	}

	// A viewport-only render loses the history...
	if strings.Contains(s.RenderANSI(), "line-00") {
		t.Error("RenderANSI should render the viewport only")
	}

	// ...but the snapshot must reconstruct it in the receiver's scrollback.
	s2 := NewScreen(40, 5)
	defer s2.Close()
	s2.StartDrain(&syncBuf{})
	s2.Write([]byte(s.RenderSnapshot(1000)))

	if got, want := strings.TrimSpace(s2.Text()), strings.TrimSpace(s.Text()); got != want {
		t.Errorf("viewport mismatch:\ngot  %q\nwant %q", got, want)
	}
	if s2.ScrollbackLen() < s.ScrollbackLen() {
		t.Errorf("scrollback lines = %d, want at least %d",
			s2.ScrollbackLen(), s.ScrollbackLen())
	}
	if !strings.Contains(scrollbackText(s2), "line-00") {
		t.Errorf("oldest line lost; scrollback was:\n%s", scrollbackText(s2))
	}
}

// TestScreenSnapshotScrollbackIsBounded checks the cap is honoured, so a very
// long session does not produce an unbounded snapshot.
func TestScreenSnapshotScrollbackIsBounded(t *testing.T) {
	s := NewScreen(40, 5)
	defer s.Close()
	s.StartDrain(&syncBuf{})
	for i := 0; i < 100; i++ {
		fmt.Fprintf(s, "line-%03d\r\n", i)
	}

	snap := s.RenderSnapshot(10)
	if strings.Contains(snap, "line-000") {
		t.Error("snapshot exceeded the requested scrollback bound")
	}
	if !strings.Contains(snap, "line-099") {
		t.Error("snapshot dropped the most recent output")
	}

	// Zero means viewport only.
	if strings.Contains(s.RenderSnapshot(0), "line-080") {
		t.Error("RenderSnapshot(0) should carry no scrollback")
	}
}

// TestScreenSnapshotDoesNotOverflowWidth guards the resync path against
// double-width characters. A wide grapheme occupies two cells, and emitting a
// space for the continuation cell makes the rendered row wider than the
// terminal, so replaying the snapshot wraps every affected row and shifts
// everything below it. CJK and emoji both hit this.
func TestScreenSnapshotDoesNotOverflowWidth(t *testing.T) {
	const cols, rows = 20, 5
	s := NewScreen(cols, rows)
	defer s.Close()
	s.StartDrain(&syncBuf{})

	s.Write([]byte("中文 ok\r\n"))
	time.Sleep(100 * time.Millisecond)

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"RenderANSI", s.RenderANSI()},
		{"RenderSnapshot", s.RenderSnapshot(0)},
	} {
		row := firstRenderedRow(tc.got)
		if w := displayWidth(row); w > cols {
			t.Errorf("%s: first row is %d columns wide, want <= %d; a %d-column terminal will wrap it\nrow = %q",
				tc.name, w, cols, cols, row)
		}
	}
}

// firstRenderedRow strips ANSI escapes and returns the first line of output.
func firstRenderedRow(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] == ';' || s[j] == '?' || (s[j] >= '0' && s[j] <= '9')) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		if s[i] == '\r' || s[i] == '\n' {
			break
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// displayWidth is the column count a terminal would give the string.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && (r <= 0x115F ||
			r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
			(r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE6F) ||
			(r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6) ||
			(r >= 0x1F300 && r <= 0x1F64F) ||
			(r >= 0x1F900 && r <= 0x1F9FF) ||
			(r >= 0x20000 && r <= 0x3FFFD)):
			w += 2
		default:
			w++
		}
	}
	return w
}
