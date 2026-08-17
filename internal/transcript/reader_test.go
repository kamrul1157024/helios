package transcript

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
)

func TestReadSegmentLineSplitsAndTrims(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("one\r\ntwo\nthree"), 16)

	for _, want := range []string{"one", "two", "three"} {
		line, oversized, _, terminated, err := readSegmentLine(r, 1024)
		if oversized {
			t.Fatalf("readSegmentLine(%q) reported oversized", want)
		}
		if string(line) != want {
			t.Fatalf("readSegmentLine = %q, want %q", line, want)
		}
		// "three" has no newline after it, which is how a line still being
		// written looks.
		if got := terminated; got != (want != "three") {
			t.Fatalf("readSegmentLine(%q) terminated = %v", want, got)
		}
		if err != nil && want != "three" {
			t.Fatalf("readSegmentLine(%q) error: %v", want, err)
		}
	}
}

func TestReadSegmentLineCountsRawBytes(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("one\r\ntwo\n"), 16)

	_, _, raw, _, err := readSegmentLine(r, 1024)
	if err != nil {
		t.Fatalf("readSegmentLine: %v", err)
	}
	// The consumed count has to include the terminator, or an incremental read
	// would re-read it and mistake it for a new line.
	if raw != 5 {
		t.Fatalf("raw = %d, want 5 (\"one\" plus CRLF)", raw)
	}
}

func TestReadSegmentLineDropsOversizedAndKeepsGoing(t *testing.T) {
	long := strings.Repeat("x", 5000)
	// The bufio buffer is deliberately smaller than the line, so the read also
	// spans several ErrBufferFull chunks.
	r := bufio.NewReaderSize(strings.NewReader(long+"\nshort\n"), 16)

	line, oversized, raw, _, err := readSegmentLine(r, 100)
	if err != nil {
		t.Fatalf("readSegmentLine: %v", err)
	}
	if !oversized {
		t.Fatal("long line not reported as oversized")
	}
	if len(line) != 100 {
		t.Fatalf("kept %d bytes, want the 100-byte cap", len(line))
	}
	// Dropping the content must not drop the bytes from the count.
	if raw != int64(len(long)+1) {
		t.Fatalf("raw = %d, want %d", raw, len(long)+1)
	}

	line, oversized, _, _, err = readSegmentLine(r, 100)
	if err != nil {
		t.Fatalf("readSegmentLine after a dropped line: %v", err)
	}
	if oversized || string(line) != "short" {
		t.Fatalf("readSegmentLine after a dropped line = %q (oversized=%v), want \"short\"", line, oversized)
	}
}

func TestParseClaudeSkipsOversizedEntry(t *testing.T) {
	entry := func(text string) string {
		return fmt.Sprintf(
			`{"type":"assistant","timestamp":"2026-08-12T10:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`,
			text,
		)
	}
	src := strings.Join([]string{
		entry("before"),
		entry(strings.Repeat("y", 4096)),
		entry("after"),
	}, "\n") + "\n"

	// A cap that admits the small entries and rejects the padded one.
	result, err := parseClaude(strings.NewReader(src), 512, 100, 0)
	if err != nil {
		t.Fatalf("parseClaude: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("Total = %d, want 2 (the oversized entry dropped)", result.Total)
	}
	if result.Messages[0].Content != "before" || result.Messages[1].Content != "after" {
		t.Fatalf("messages = %q, %q; want the entries either side of the oversized one",
			result.Messages[0].Content, result.Messages[1].Content)
	}
}
