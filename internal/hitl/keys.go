package hitl

// key is one thing a prompt understands. Terminal input is bytes; a prompt
// only ever reacts to these five.
type key int

const (
	keyIgnored key = iota
	keyPrev
	keyNext
	keyConfirm
	keyCancel
	// keySelect picks a choice by number, the way the CLI's own dialogs do.
	keySelect
)

// event is a decoded keystroke. n carries the 0-based choice for keySelect.
type event struct {
	kind key
	n    int
}

// decodeKeys turns a chunk of terminal input into prompt keys.
//
// It returns a slice because input arrives coalesced: a held arrow key or a
// paste delivers several keystrokes in one frame, and dropping all but the
// first would lose moves the user made.
//
// A lone ESC is a cancel. That is correct for a real keypress, which arrives as
// one byte, but a terminal that split an escape sequence across two writes
// would read as a cancel followed by junk. No terminal in practice does, and
// the alternative — waiting to see whether more bytes follow — would make every
// real Escape feel slow.
func decodeKeys(p []byte) []event {
	var out []event
	for i := 0; i < len(p); {
		switch b := p[i]; {
		case b == 0x1b:
			ev, width := decodeEscape(p[i:])
			if ev.kind != keyIgnored {
				out = append(out, ev)
			}
			i += width
		case b == '\r' || b == '\n':
			out = append(out, event{kind: keyConfirm})
			i++
		case b == 0x03: // Ctrl-C
			out = append(out, event{kind: keyCancel})
			i++
		case b == 'k':
			out = append(out, event{kind: keyPrev})
			i++
		case b == 'j':
			out = append(out, event{kind: keyNext})
			i++
		case b >= '1' && b <= '9':
			out = append(out, event{kind: keySelect, n: int(b - '1')})
			i++
		default:
			i++
		}
	}
	return out
}

// decodeEscape reads one escape sequence from the front of p, which begins with
// ESC, and reports how many bytes it consumed.
//
// Cursor keys arrive two ways. In the default mode a terminal sends them as CSI
// — "ESC [ A". A full-screen application switches the terminal into application
// cursor keys mode (DECCKM, "ESC [ ? 1 h"), after which the same keys arrive as
// SS3 — "ESC O A". Both have to move the highlight, or the prompt is unanswerable
// with the arrow keys the moment an alt-screen agent is on screen.
func decodeEscape(p []byte) (event, int) {
	if len(p) < 3 || (p[1] != '[' && p[1] != 'O') {
		return event{kind: keyCancel}, 1
	}
	switch p[2] {
	case 'A':
		return event{kind: keyPrev}, 3
	case 'B':
		return event{kind: keyNext}, 3
	}
	// Some other CSI/SS3 — a mouse report, a bracketed-paste marker, a function
	// key. Skip to its final byte rather than reading its parameters as
	// keystrokes: an unread "\x1b[3~" would otherwise select choice three.
	i := 2
	for i < len(p) && (p[i] < 0x40 || p[i] > 0x7e) {
		i++
	}
	if i < len(p) {
		i++
	}
	return event{kind: keyIgnored}, i
}
