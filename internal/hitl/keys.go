package hitl

import "bytes"

// key is one thing a prompt understands. Terminal input is bytes; a prompt only
// ever reacts to these.
type key int

const (
	keyIgnored key = iota
	keyPrev
	keyNext
	keyConfirm
	keyCancel
	// keySelect picks a choice by number, the way the CLI's own dialogs do.
	keySelect
	// keyToggle ticks a choice without answering, on a multi-select prompt.
	keyToggle
	// keyText carries characters typed into the answer field, in s.
	keyText
	// keyErase, keyEraseWord and keyEraseLine edit that field backwards from
	// the caret: one rune, one word, the whole line.
	keyErase
	keyEraseWord
	keyEraseLine
	// keyLeaveField closes the answer field and leaves the prompt standing.
	keyLeaveField
)

// event is a decoded keystroke. n carries the 0-based choice for keySelect, and
// s the characters for keyText.
type event struct {
	kind key
	n    int
	s    string
}

// A terminal brackets a paste so an application can tell it from typing.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// decodeKeys turns a chunk of terminal input into prompt keys.
//
// It returns a slice because input arrives coalesced: a held arrow key or a
// paste delivers several keystrokes in one frame, and dropping all but the
// first would lose moves the user made.
//
// The same bytes mean different things depending on whether the answer field
// has the keyboard, so editing selects between two vocabularies. In the list
// the digits jump the highlight and "j" moves down; in the field both are
// characters the user meant to type.
//
// A lone ESC is a cancel in the list, and only closes the field while editing.
// That is correct for a real keypress, which arrives as one byte, but a
// terminal that split an escape sequence across two writes would read as a
// cancel followed by junk. No terminal in practice does, and the alternative —
// waiting to see whether more bytes follow — would make every real Escape feel
// slow.
func decodeKeys(p []byte, editing bool) []event {
	var out []event
	for i := 0; i < len(p); {
		if text, width, ok := decodePaste(p[i:]); ok {
			// Pasted text is only ever text. Read as keystrokes it would arrive
			// as a burst of jumps and confirms and answer the prompt by itself.
			if editing && text != "" {
				out = append(out, event{kind: keyText, s: text})
			}
			i += width
			continue
		}

		switch b := p[i]; {
		case b == 0x1b:
			ev, width := decodeEscape(p[i:], editing)
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
		case editing:
			ev, width := decodeEdit(p[i:])
			if ev.kind != keyIgnored {
				out = append(out, ev)
			}
			i += width
		case b == 'k':
			out = append(out, event{kind: keyPrev})
			i++
		case b == 'j':
			out = append(out, event{kind: keyNext})
			i++
		case b == ' ':
			out = append(out, event{kind: keyToggle})
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

// decodeEdit reads one editing keystroke from the front of p, which is neither
// an escape sequence, a newline nor a Ctrl-C.
func decodeEdit(p []byte) (event, int) {
	switch b := p[0]; {
	case b == 0x7f || b == 0x08:
		return event{kind: keyErase}, 1
	case b == 0x15: // Ctrl-U
		return event{kind: keyEraseLine}, 1
	case b == 0x17: // Ctrl-W
		return event{kind: keyEraseWord}, 1
	case b >= 0x20:
		// Take the whole printable run, so a fast typist or a bracket-less
		// paste becomes one insert rather than one event per byte. Runs stop at
		// any control byte, which keeps multi-byte UTF-8 intact.
		i := 0
		for i < len(p) && p[i] >= 0x20 && p[i] != 0x7f {
			i++
		}
		return event{kind: keyText, s: string(p[:i])}, i
	default:
		return event{kind: keyIgnored}, 1
	}
}

// decodePaste reads a bracketed paste from the front of p and reports whether
// one was there.
func decodePaste(p []byte) (string, int, bool) {
	if !bytes.HasPrefix(p, []byte(pasteStart)) {
		return "", 0, false
	}
	body := p[len(pasteStart):]
	if end := bytes.Index(body, []byte(pasteEnd)); end >= 0 {
		return string(body[:end]), len(pasteStart) + end + len(pasteEnd), true
	}
	// A paste whose end marker has not arrived yet. Take what is here: the rest
	// lands as ordinary input, which is what an unbracketed paste does anyway.
	return string(body), len(p), true
}

// decodeEscape reads one escape sequence from the front of p, which begins with
// ESC, and reports how many bytes it consumed.
//
// Cursor keys arrive two ways. In the default mode a terminal sends them as CSI
// — "ESC [ A". A full-screen application switches the terminal into application
// cursor keys mode (DECCKM, "ESC [ ? 1 h"), after which the same keys arrive as
// SS3 — "ESC O A". Both have to move the highlight, or the prompt is unanswerable
// with the arrow keys the moment an alt-screen agent is on screen.
func decodeEscape(p []byte, editing bool) (event, int) {
	lone := event{kind: keyCancel}
	if editing {
		lone = event{kind: keyLeaveField}
	}
	if len(p) < 3 || (p[1] != '[' && p[1] != 'O') {
		return lone, 1
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
