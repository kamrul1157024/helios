package terminal

import "testing"

// The sequence CI panicked on: a scroll region describing a taller screen than
// the one being drawn, followed by a scroll into it.
func TestScreenSurvivesAScrollRegionLargerThanTheGrid(t *testing.T) {
	s := NewScreen(80, 60)
	defer s.Close()

	if _, err := s.Write([]byte("\x1b[1;98r")); err != nil {
		t.Fatalf("set margins: %v", err)
	}
	if _, err := s.Write([]byte("\x1bM hello\r\n")); err != nil {
		t.Fatalf("scroll: %v", err)
	}

	if n := s.Panics(); n != 0 {
		t.Errorf("panics = %d, want 0", n)
	}
}

// Recovery costs the write it happened on, which is why the region is clamped
// rather than caught: the text has to arrive.
func TestOutputSurvivesAnOversizedScrollRegion(t *testing.T) {
	s := NewScreen(80, 60)
	defer s.Close()

	if _, err := s.Write([]byte("\x1b[1;98r\x1bMsecond\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := s.Text(); !contains(got, "second") {
		t.Errorf("the screen lost the text: %q", got)
	}
}

func TestClampMargins(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		rows    int
		want    string
		changed bool
	}{
		{name: "a region inside the grid is left alone", in: "\x1b[1;24r", rows: 60, want: "\x1b[1;24r"},
		{name: "the bottom is brought to the last row", in: "\x1b[1;98r", rows: 60, want: "\x1b[1;60r", changed: true},
		{
			name:    "a top past the grid drops the region, as a bare reset does",
			in:      "\x1b[70;98r",
			rows:    60,
			want:    "\x1b[r",
			changed: true,
		},
		{name: "a bare reset always fits", in: "\x1b[r", rows: 60, want: "\x1b[r"},
		{name: "text with no escapes is untouched", in: "just output\r\n", rows: 60, want: "just output\r\n"},
		// `r` is the final byte of DECRST as well, and that one is about modes,
		// not rows.
		{name: "a private mode reset is not a scroll region", in: "\x1b[?1049r", rows: 60, want: "\x1b[?1049r"},
		{name: "one parameter is not DECSTBM", in: "\x1b[98r", rows: 60, want: "\x1b[98r"},
		{
			name:    "the region is corrected where it sits in a longer write",
			in:      "before\x1b[1;98rafter",
			rows:    60,
			want:    "before\x1b[1;60rafter",
			changed: true,
		},
		{
			name:    "every region in one write is corrected",
			in:      "\x1b[1;98r\x1b[2;99r",
			rows:    60,
			want:    "\x1b[1;60r\x1b[2;60r",
			changed: true,
		},
		// A sequence cut in half by the read boundary is left for the recover
		// in Write, which is still there for exactly this.
		{name: "a truncated sequence is left alone", in: "\x1b[1;98", rows: 60, want: "\x1b[1;98"},
		{name: "a grid of no rows cannot be judged", in: "\x1b[1;98r", rows: 0, want: "\x1b[1;98r"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := clampMargins([]byte(tc.in), tc.rows)
			if string(got) != tc.want {
				t.Errorf("clampMargins(%q, %d) = %q, want %q", tc.in, tc.rows, got, tc.want)
			}
			if changed != tc.changed {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

// The caller counts bytes it handed over, not bytes the emulator received: a
// rewrite makes those differ, and a short count would have the pump resend.
func TestWriteReportsTheCallersLength(t *testing.T) {
	s := NewScreen(80, 60)
	defer s.Close()

	in := []byte("\x1b[1;98rtail")
	n, err := s.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("n = %d, want %d", n, len(in))
	}
}

func contains(haystack, needle string) bool {
	for at := 0; at+len(needle) <= len(haystack); at++ {
		if haystack[at:at+len(needle)] == needle {
			return true
		}
	}
	return false
}
