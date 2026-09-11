package terminal

import (
	"strings"
	"testing"
)

// The sequence that took the daemon down, reduced: a scroll region taller than
// the grid, then a reverse index at the top margin. The emulator scrolls a
// region that does not fit its buffer and indexes past the end.
//
// A mirror runs Write on its own goroutine inside the daemon, so this panic
// killed every session at once — and printed to a stderr the daemon had pointed
// at /dev/null, which is why it looked like an external kill for hours.
//
// Write clamps the region on the way in now, so this arrives harmless. The
// recover behind it is still load-bearing — see the split write below.
const panicSequence = "\x1b[1;100r\x1b[1;1H\x1bM"

func TestScreenClampsTheSequenceThatTookTheDaemonDown(t *testing.T) {
	s := NewScreen(80, 60)

	n, err := s.Write([]byte(panicSequence))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(panicSequence) {
		t.Errorf("wrote %d of %d bytes", n, len(panicSequence))
	}
	if s.Panics() != 0 {
		t.Errorf("panics = %d, want 0 — the region should be clamped before the emulator sees it", s.Panics())
	}
}

// What the clamp cannot see: a sequence cut in half by a read boundary. Neither
// write holds a whole region, so the emulator gets it and still panics — which
// is why the recover stays, and why this asserts that it still fires.
func TestScreenSurvivesASequenceSplitAcrossWrites(t *testing.T) {
	s := NewScreen(80, 60)

	for _, half := range []string{"\x1b[1;100", "r\x1b[1;1H\x1bM"} {
		if _, err := s.Write([]byte(half)); err != nil {
			t.Fatalf("write %q: %v", half, err)
		}
	}
	if s.Panics() != 1 {
		t.Fatalf("panics = %d, want 1 — if this stops reproducing the recover guards nothing", s.Panics())
	}

	// Still usable afterwards: the daemon keeps mirroring the session rather
	// than holding a screen that refuses every write.
	if _, err := s.Write([]byte("after the panic")); err != nil {
		t.Fatalf("write after panic: %v", err)
	}
	if !strings.Contains(s.Text(), "after the panic") {
		t.Error("the screen stopped rendering after it recovered")
	}
}

// The mismatch that produces the sequence in the first place: the mirror's grid
// is a guess, and the agent draws for the terminal it actually has.
func TestMirrorGridGrowsToTheHost(t *testing.T) {
	s := NewScreen(mirrorCols, mirrorRows)
	m := &Mirror{screen: s}

	m.matchSize(220, 100)
	if cols, rows := s.Size(); cols != 220 || rows != 100 {
		t.Errorf("grid = %dx%d, want 220x100", cols, rows)
	}

	// Never shrinks: a viewer on a phone must not cost the daemon the rows the
	// agent is drawing into.
	m.matchSize(80, 24)
	if cols, rows := s.Size(); cols != 220 || rows != 100 {
		t.Errorf("grid = %dx%d after a smaller viewer, want it held at 220x100", cols, rows)
	}

	// And with the grid matching, the sequence that panicked is in range.
	if _, err := s.Write([]byte(panicSequence)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if s.Panics() != 0 {
		t.Errorf("panicked at %dx%d, where the region fits", 220, 100)
	}
}
