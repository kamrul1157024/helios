package hitl

import "testing"

func TestDecodeKeys(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []event
	}{
		{"down arrow", "\x1b[B", []event{{kind: keyNext}}},
		{"up arrow", "\x1b[A", []event{{kind: keyPrev}}},
		// SS3 cursor keys: what a full-screen app in application cursor keys
		// mode (DECCKM) makes the terminal send. Before this was handled they
		// decoded as a cancel, so arrows dismissed the prompt instead of moving.
		{"ss3 down arrow", "\x1bOB", []event{{kind: keyNext}}},
		{"ss3 up arrow", "\x1bOA", []event{{kind: keyPrev}}},
		{"ss3 coalesced", "\x1bOB\x1bOB\r", []event{
			{kind: keyNext}, {kind: keyNext}, {kind: keyConfirm},
		}},
		// An SS3 function key (F1 is "ESC O P") is not a cursor key: skip it
		// whole rather than reading 'P' as anything.
		{"ss3 function key is skipped", "\x1bOP", nil},
		{"vi keys", "jk", []event{{kind: keyNext}, {kind: keyPrev}}},
		{"enter", "\r", []event{{kind: keyConfirm}}},
		{"newline", "\n", []event{{kind: keyConfirm}}},
		{"escape", "\x1b", []event{{kind: keyCancel}}},
		{"ctrl-c", "\x03", []event{{kind: keyCancel}}},
		{"digits are one-based", "13", []event{
			{kind: keySelect, n: 0}, {kind: keySelect, n: 2},
		}},
		// A held arrow key arrives as one write, and every repeat has to count.
		{"coalesced", "\x1b[B\x1b[B\r", []event{
			{kind: keyNext}, {kind: keyNext}, {kind: keyConfirm},
		}},
		// "\x1b[3~" is Delete. Reading its parameter byte as a keystroke would
		// silently select the third choice.
		{"unknown csi is skipped whole", "\x1b[3~", nil},
		{"unknown csi then enter", "\x1b[3~\r", []event{{kind: keyConfirm}}},
		{"letters are ignored", "hello", nil},
		{"empty", "", nil},
		// A truncated sequence must not run off the end of the buffer.
		{"bare csi", "\x1b[", []event{{kind: keyCancel}}},
		{"space toggles", " ", []event{{kind: keyToggle}}},
		// Pasted text read as keystrokes would jump and confirm its way to an
		// answer nobody chose.
		{"paste is skipped whole", "\x1b[200~2 j\x1b[201~", nil},
		{"paste then enter", "\x1b[200~x\x1b[201~\r", []event{{kind: keyConfirm}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeKeys([]byte(c.in), false)
			if len(got) != len(c.want) {
				t.Fatalf("decodeKeys(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("key %d = %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestDecodeKeysWhileEditing pins the other vocabulary. The same bytes that
// drive the list are characters once the answer field has the keyboard.
func TestDecodeKeysWhileEditing(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []event
	}{
		{"letters are text", "hello", []event{{kind: keyText, s: "hello"}}},
		{"digits are text", "13", []event{{kind: keyText, s: "13"}}},
		{"vi keys are text", "jk", []event{{kind: keyText, s: "jk"}}},
		{"space is text", "a b", []event{{kind: keyText, s: "a b"}}},
		{"utf-8 survives", "héllo…", []event{{kind: keyText, s: "héllo…"}}},
		{"backspace", "\x7f", []event{{kind: keyErase}}},
		{"ctrl-u clears the line", "\x15", []event{{kind: keyEraseLine}}},
		{"ctrl-w deletes a word", "\x17", []event{{kind: keyEraseWord}}},
		// The first Escape closes the field. Only the second cancels, and the
		// footer says so while the field is open.
		{"escape leaves the field", "\x1b", []event{{kind: keyLeaveField}}},
		{"ctrl-c still cancels", "\x03", []event{{kind: keyCancel}}},
		{"enter sends", "\r", []event{{kind: keyConfirm}}},
		{"arrows still move", "\x1b[B", []event{{kind: keyNext}}},
		{"paste is inserted", "\x1b[200~2 j\x1b[201~", []event{{kind: keyText, s: "2 j"}}},
		{"typing then enter", "ok\r", []event{{kind: keyText, s: "ok"}, {kind: keyConfirm}}},
		// A paste whose end marker has not arrived yet still has to land as text.
		{"unterminated paste", "\x1b[200~half", []event{{kind: keyText, s: "half"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeKeys([]byte(c.in), true)
			if len(got) != len(c.want) {
				t.Fatalf("decodeKeys(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("key %d = %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}
