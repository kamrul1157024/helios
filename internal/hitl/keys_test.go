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
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeKeys([]byte(c.in))
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
