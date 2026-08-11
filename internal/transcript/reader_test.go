package transcript

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
)

func TestReadLineSplitsAndTrims(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("one\r\ntwo\nthree"), 16)

	for _, want := range []string{"one", "two", "three"} {
		line, oversized, err := readLine(r, 1024)
		if oversized {
			t.Fatalf("readLine(%q) reported oversized", want)
		}
		if string(line) != want {
			t.Fatalf("readLine = %q, want %q", line, want)
		}
		if err != nil && want != "three" {
			t.Fatalf("readLine(%q) error: %v", want, err)
		}
	}
}

func TestReadLineDropsOversizedAndKeepsGoing(t *testing.T) {
	long := strings.Repeat("x", 5000)
	// The bufio buffer is deliberately smaller than the line, so the read also
	// spans several ErrBufferFull chunks.
	r := bufio.NewReaderSize(strings.NewReader(long+"\nshort\n"), 16)

	line, oversized, err := readLine(r, 100)
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if !oversized {
		t.Fatal("long line not reported as oversized")
	}
	if len(line) != 100 {
		t.Fatalf("kept %d bytes, want the 100-byte cap", len(line))
	}

	line, oversized, err = readLine(r, 100)
	if err != nil {
		t.Fatalf("readLine after a dropped line: %v", err)
	}
	if oversized || string(line) != "short" {
		t.Fatalf("readLine after a dropped line = %q (oversized=%v), want \"short\"", line, oversized)
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
