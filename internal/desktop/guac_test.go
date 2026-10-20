package desktop

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEncodeCountsCodePoints(t *testing.T) {
	got := Encode("name", "héllo", "")
	want := "4.name,5.héllo,0.;"
	if got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
}

func TestReaderRoundTrip(t *testing.T) {
	stream := Encode("size", "1280", "800", "96") + Encode("clipboard", "日本語,;.") + Encode("")
	r := NewReader(strings.NewReader(stream))

	want := []Instruction{
		{Opcode: "size", Args: []string{"1280", "800", "96"}},
		{Opcode: "clipboard", Args: []string{"日本語,;."}},
		{Opcode: "", Args: []string{}},
	}
	for _, w := range want {
		got, err := r.Read()
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Opcode != w.Opcode || strings.Join(got.Args, "|") != strings.Join(w.Args, "|") {
			t.Fatalf("Read = %+v, want %+v", got, w)
		}
	}
	if _, err := r.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("Read at end = %v, want EOF", err)
	}
}

func TestReaderRejectsMalformed(t *testing.T) {
	for _, in := range []string{"x.size;", "4.size:", "4.si"} {
		_, err := NewReader(strings.NewReader(in)).Read()
		if err == nil {
			t.Errorf("Read(%q) succeeded, want an error", in)
		}
	}
}

// The splitter must find the same boundaries however the stream is cut,
// including in the middle of a multi-byte character.
func TestSplitterFindsBoundariesAcrossReads(t *testing.T) {
	stream := Encode("img", "1", "image/png", "0", "0", "0") + Encode("blob", "1", "ünï") + Encode("sync", "123")
	for cut := 1; cut < len(stream); cut++ {
		var s Splitter
		var sent strings.Builder
		pending := ""
		for _, chunk := range []string{stream[:cut], stream[cut:]} {
			n, err := s.Feed([]byte(chunk))
			if err != nil {
				t.Fatalf("cut %d: Feed: %v", cut, err)
			}
			if n > 0 {
				msg := pending + chunk[:n]
				if !strings.HasSuffix(msg, ";") {
					t.Fatalf("cut %d: message %q does not end an instruction", cut, msg)
				}
				if _, err := ParseAll(msg); err != nil {
					t.Fatalf("cut %d: message %q is not whole instructions: %v", cut, msg, err)
				}
				sent.WriteString(msg)
				pending = chunk[n:]
			} else {
				pending += chunk
			}
		}
		if sent.String() != stream || pending != "" {
			t.Fatalf("cut %d: sent %q, pending %q", cut, sent.String(), pending)
		}
	}
}

func TestSplitterRejectsGarbage(t *testing.T) {
	var s Splitter
	if _, err := s.Feed([]byte("hello;")); err == nil {
		t.Fatal("Feed accepted bytes that are not an instruction")
	}
}

func TestParseAll(t *testing.T) {
	got, err := ParseAll(Encode("mouse", "10", "20", "1") + Encode("", "ping", "1700000000000"))
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(got) != 2 || got[0].Opcode != "mouse" || got[1].Opcode != "" || got[1].Args[0] != "ping" {
		t.Fatalf("ParseAll = %+v", got)
	}
	if _, err := ParseAll("5.mouse,2.10"); err == nil {
		t.Fatal("ParseAll accepted a truncated instruction")
	}
}
