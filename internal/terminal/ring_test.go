package terminal

import (
	"bytes"
	"testing"
)

func TestRingSequenceContinuity(t *testing.T) {
	r := NewRing(64)
	r.Write([]byte("hello"))
	r.Write([]byte("world"))

	if got := r.Seq(); got != 10 {
		t.Errorf("Seq() = %d, want 10", got)
	}
	data, next, ok := r.Since(0)
	if !ok {
		t.Fatal("Since(0) not ok on an unevicted ring")
	}
	if string(data) != "helloworld" {
		t.Errorf("Since(0) = %q, want %q", data, "helloworld")
	}
	if next != 10 {
		t.Errorf("next = %d, want 10", next)
	}
}

func TestRingPartialSince(t *testing.T) {
	r := NewRing(64)
	r.Write([]byte("abcdef"))

	data, next, ok := r.Since(2)
	if !ok {
		t.Fatal("Since(2) not ok")
	}
	if string(data) != "cdef" {
		t.Errorf("Since(2) = %q, want %q", data, "cdef")
	}
	if next != 6 {
		t.Errorf("next = %d, want 6", next)
	}
}

func TestRingEvictionReportsGap(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("aaaaaaaa")) // fills
	r.Write([]byte("bbbb"))     // evicts the first 4

	if got := r.Start(); got != 4 {
		t.Errorf("Start() = %d, want 4", got)
	}
	if _, _, ok := r.Since(0); ok {
		t.Error("Since(0) should report a gap after eviction")
	}
	data, _, ok := r.Since(4)
	if !ok {
		t.Fatal("Since(4) should still be retained")
	}
	if string(data) != "aaaabbbb" {
		t.Errorf("Since(4) = %q, want %q", data, "aaaabbbb")
	}
}

func TestRingOversizedWriteKeepsTail(t *testing.T) {
	r := NewRing(4)
	r.Write([]byte("abcdefgh"))

	if got := r.Len(); got != 4 {
		t.Errorf("Len() = %d, want 4", got)
	}
	if got := r.Seq(); got != 8 {
		t.Errorf("Seq() = %d, want 8", got)
	}
	data, _, ok := r.Since(4)
	if !ok {
		t.Fatal("tail should be retained")
	}
	if string(data) != "efgh" {
		t.Errorf("Since(4) = %q, want %q", data, "efgh")
	}
}

func TestRingSinceAheadOfEnd(t *testing.T) {
	r := NewRing(16)
	r.Write([]byte("abc"))
	// A viewer claiming to be ahead is treated as caught up, not an error.
	data, next, ok := r.Since(99)
	if !ok || len(data) != 0 || next != 3 {
		t.Errorf("Since(99) = (%q, %d, %v), want (\"\", 3, true)", data, next, ok)
	}
}

func TestRingWraparoundIntegrity(t *testing.T) {
	r := NewRing(16)
	var all bytes.Buffer
	for i := 0; i < 100; i++ {
		chunk := []byte{byte('a' + i%26)}
		r.Write(chunk)
		all.Write(chunk)
	}
	if got := r.Seq(); got != 100 {
		t.Errorf("Seq() = %d, want 100", got)
	}
	data, _, ok := r.Since(r.Start())
	if !ok {
		t.Fatal("Since(Start()) must always be retained")
	}
	want := all.Bytes()[all.Len()-len(data):]
	if !bytes.Equal(data, want) {
		t.Errorf("ring tail = %q, want %q", data, want)
	}
}
