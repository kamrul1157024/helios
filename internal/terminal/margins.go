package terminal

import (
	"slices"
	"strconv"
)

// clampMargins rewrites any scroll region that does not fit the grid.
//
// DECSTBM (`ESC [ top ; bottom r`) names the rows a scroll is confined to. The
// emulator indexes its cell buffer with those rows and does not check them, so
// a bottom margin past the last row panics on the next scroll: `index out of
// range [98] with length 60`.
//
// Nothing exotic produces one. A pane is resized smaller while a full-screen
// program is drawing, or an agent prints a transcript captured from a taller
// terminal, and the region arrives describing a screen that no longer exists.
// Screen.Write recovers from the panic, but recovery costs the whole write —
// which is a redraw the viewer never sees — so the sequence is corrected on
// the way in instead.
//
// Only the bottom is clamped: a top margin past the grid is nonsense the
// emulator handles by ignoring the sequence, and rewriting it would invent a
// region the application did not ask for.
func clampMargins(p []byte, rows int) ([]byte, bool) {
	if rows <= 0 {
		return p, false
	}
	// The common case by far: nothing here sets a region, and a scan that
	// allocates nothing is worth having on every write from the PTY.
	if !hasCSI(p) {
		return p, false
	}

	out := p
	copied := false
	for at := 0; at+1 < len(out); at++ {
		if out[at] != 0x1b || out[at+1] != '[' {
			continue
		}
		params, end, ok := csiParams(out, at+2)
		if !ok || end >= len(out) || out[end] != 'r' {
			continue
		}
		// `ESC [ ? … r` is DECRST, a different sequence that happens to end in
		// the same letter.
		if len(params) > 0 && params[0] == '?' {
			continue
		}
		fixed, changed := clampOne(params, rows)
		if !changed {
			continue
		}
		if !copied {
			out = slices.Clone(out)
			copied = true
		}
		// The replacement is never longer than what it replaces — a clamped
		// bottom is a smaller number — but splicing rather than overwriting
		// keeps that from being load-bearing.
		tail := slices.Clone(out[end:])
		out = append(out[:at+2], append([]byte(fixed), tail...)...)
		at += 2 + len(fixed)
	}
	return out, copied
}

func hasCSI(p []byte) bool {
	for at := 0; at+1 < len(p); at++ {
		if p[at] == 0x1b && p[at+1] == '[' {
			return true
		}
	}
	return false
}

/*
csiParams reads the parameter bytes of a CSI sequence, returning them and the
index of the final byte. Parameters are digits and semicolons; anything else
ends the sequence, and a sequence that runs off the end of the buffer is not
one this write can judge.
*/
func csiParams(p []byte, from int) (string, int, bool) {
	at := from
	for at < len(p) && ((p[at] >= '0' && p[at] <= '9') || p[at] == ';' || p[at] == '?') {
		at++
	}
	if at >= len(p) {
		return "", at, false
	}
	return string(p[from:at]), at, true
}

// clampOne returns the parameters with the bottom margin brought inside the
// grid, and whether it had to change anything.
func clampOne(params string, rows int) (string, bool) {
	// `ESC [ r` with no parameters resets the region to the whole grid, which
	// always fits.
	if params == "" {
		return params, false
	}
	top, bottom, ok := twoParams(params)
	if !ok || bottom <= rows {
		return params, false
	}
	// A region has to hold at least two rows for a scroll to mean anything; a
	// top that has been pushed past the new bottom is dropped along with it,
	// which is what `ESC [ r` means.
	if top >= rows {
		return "", true
	}
	return strconv.Itoa(top) + ";" + strconv.Itoa(rows), true
}

func twoParams(params string) (top, bottom int, ok bool) {
	semi := -1
	for at := range len(params) {
		if params[at] == ';' {
			if semi != -1 {
				// Three parameters is not DECSTBM; leave it alone.
				return 0, 0, false
			}
			semi = at
		}
	}
	if semi == -1 {
		return 0, 0, false
	}
	top, err := strconv.Atoi(params[:semi])
	if err != nil {
		return 0, 0, false
	}
	bottom, err = strconv.Atoi(params[semi+1:])
	if err != nil {
		return 0, 0, false
	}
	return top, bottom, true
}
