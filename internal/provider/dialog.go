package provider

import (
	"fmt"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
)

// cursorMarkers are the glyphs agents use to show the highlighted row.
// Claude draws ❯, Codex draws ›. Neither is standard, so both are matched.
var cursorMarkers = []string{"❯", "›", "▸", ">"}

// ConfirmChoice answers a full-screen list dialog by moving the highlight onto
// the option whose line contains want, then pressing Return.
//
// It reads the screen rather than assuming a default. The Claude trust dialog
// used to open on "Yes, proceed" and helios answered it with a bare Return;
// Claude later changed the default to "No, exit", at which point that Return
// quit the agent instead of trusting the folder. Anything that depends on
// which row an agent happens to highlight first will break the same way, so
// this looks.
//
// The move is verified before Return is sent. Sending Return at a highlight
// that did not move is how a mis-read screen becomes a destructive answer.
func ConfirmChoice(b backend.Backend, sessionID, want string) error {
	if b == nil {
		return fmt.Errorf("no terminal for session %s", sessionID)
	}
	for attempt := 0; attempt < 8; attempt++ {
		screen, err := b.Capture(sessionID)
		if err != nil {
			return fmt.Errorf("read screen for %s: %w", sessionID, err)
		}
		cursor, target, ok := locateChoice(screen, want)
		if !ok {
			return fmt.Errorf("no option matching %q on screen for %s", want, sessionID)
		}
		if cursor == target {
			return b.SendKey(sessionID, backend.KeyEnter)
		}
		key := backend.KeyDown
		if target < cursor {
			key = backend.KeyUp
		}
		if err := b.SendKey(sessionID, key); err != nil {
			return fmt.Errorf("move highlight for %s: %w", sessionID, err)
		}
		// One row at a time, re-reading between. A dialog can redraw, reorder
		// or scroll, and a burst of arrows sent blind lands anywhere.
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("could not move the highlight onto %q for %s", want, sessionID)
}

// locateChoice returns the line index of the highlight and of the wanted
// option, ignoring blank lines so the indices are comparable as row moves.
func locateChoice(screen, want string) (cursor, target int, ok bool) {
	cursor, target = -1, -1
	wantLower := strings.ToLower(want)

	row := 0
	for _, line := range strings.Split(screen, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		isCursor := false
		for _, m := range cursorMarkers {
			if strings.HasPrefix(trimmed, m) {
				isCursor = true
				break
			}
		}
		if isCursor {
			cursor = row
		}
		if strings.Contains(strings.ToLower(trimmed), wantLower) {
			// The last match wins: a dialog often names the option in its
			// prose before offering it, and the offer is lower down.
			target = row
		}
		row++
	}
	return cursor, target, cursor >= 0 && target >= 0
}
