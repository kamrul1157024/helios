package terminal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		typ     FrameType
		payload []byte
	}{
		{FrameOutput, []byte("hello world")},
		{FrameInput, []byte{0x1b, '[', 'A'}},
		{FramePing, nil},
		{FrameResize, EncodeResize(120, 40)},
	}

	var buf bytes.Buffer
	for _, c := range cases {
		if err := WriteFrame(&buf, c.typ, c.payload); err != nil {
			t.Fatalf("WriteFrame(%s): %v", c.typ, err)
		}
	}
	for _, c := range cases {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if got.Type != c.typ {
			t.Errorf("type = %s, want %s", got.Type, c.typ)
		}
		if !bytes.Equal(got.Payload, c.payload) {
			t.Errorf("payload = %q, want %q", got.Payload, c.payload)
		}
	}
}

func TestFrameTruncated(t *testing.T) {
	var buf bytes.Buffer
	WriteFrame(&buf, FrameOutput, []byte("abcdefgh"))
	truncated := buf.Bytes()[:7]

	if _, err := ReadFrame(bytes.NewReader(truncated)); err == nil {
		t.Error("expected an error reading a truncated frame")
	}
}

func TestFrameOversizedRejected(t *testing.T) {
	// A hostile length prefix must be refused, not allocated.
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[0:4], MaxFrameSize+1)
	hdr[4] = byte(FrameOutput)

	_, err := ReadFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err = %v, want ErrFrameTooLarge", err)
	}
}

func TestWriteFrameRejectsOversizedPayload(t *testing.T) {
	big := make([]byte, MaxFrameSize)
	if err := WriteFrame(io.Discard, FrameOutput, big); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameEOF(t *testing.T) {
	if _, err := ReadFrame(strings.NewReader("")); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestZeroLengthFrameRejected(t *testing.T) {
	var hdr [5]byte // length 0
	if _, err := ReadFrame(bytes.NewReader(hdr[:])); err == nil {
		t.Error("expected an error for a zero-length frame")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	ansi := []byte("\x1b[H\x1b[2Jhello")
	enc := EncodeSnapshot(4242, ansi)
	seq, got, err := DecodeSnapshot(enc)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	if seq != 4242 {
		t.Errorf("seq = %d, want 4242", seq)
	}
	if !bytes.Equal(got, ansi) {
		t.Errorf("ansi = %q, want %q", got, ansi)
	}
	if _, _, err := DecodeSnapshot([]byte{1, 2}); err == nil {
		t.Error("expected an error for a short snapshot payload")
	}
}

func TestResizeRoundTrip(t *testing.T) {
	cols, rows, err := DecodeResize(EncodeResize(200, 60))
	if err != nil {
		t.Fatalf("DecodeResize: %v", err)
	}
	if cols != 200 || rows != 60 {
		t.Errorf("got %dx%d, want 200x60", cols, rows)
	}
	if _, _, err := DecodeResize([]byte{1}); err == nil {
		t.Error("expected an error for a short resize payload")
	}
}

func TestJSONFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := Hello{Role: RoleInteractive, Cols: 100, Rows: 30, Since: 7, Name: "tui"}
	if err := WriteJSONFrame(&buf, FrameHello, want); err != nil {
		t.Fatalf("WriteJSONFrame: %v", err)
	}
	f, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != FrameHello {
		t.Fatalf("type = %s, want hello", f.Type)
	}
	var got Hello
	if err := json.Unmarshal(f.Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("hello = %+v, want %+v", got, want)
	}
}
