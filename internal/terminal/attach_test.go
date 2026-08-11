package terminal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestFilterDetachPassesOrdinaryInput(t *testing.T) {
	armed := false
	send, detach := filterDetach([]byte("hello world\r"), DefaultDetachKey, &armed)
	if detach {
		t.Fatal("ordinary input must not detach")
	}
	if string(send) != "hello world\r" {
		t.Fatalf("got %q, want the input unchanged", send)
	}
	if armed {
		t.Fatal("no prefix was seen; state should be clean")
	}
}

func TestFilterDetachOnPrefixThenD(t *testing.T) {
	armed := false
	send, detach := filterDetach([]byte{'a', DefaultDetachKey, 'd'}, DefaultDetachKey, &armed)
	if !detach {
		t.Fatal("prefix followed by d should detach")
	}
	// Input typed before the prefix still belongs to the PTY.
	if string(send) != "a" {
		t.Fatalf("got %q, want the bytes before the prefix", send)
	}
}

func TestFilterDetachSpansReads(t *testing.T) {
	// A prefix and its follow-up key routinely arrive in separate reads, so
	// the armed flag has to survive between calls.
	armed := false
	send, detach := filterDetach([]byte{DefaultDetachKey}, DefaultDetachKey, &armed)
	if detach || len(send) != 0 {
		t.Fatalf("a lone prefix should emit nothing; got %q detach=%v", send, detach)
	}
	if !armed {
		t.Fatal("prefix should leave the filter armed")
	}
	send, detach = filterDetach([]byte{'d'}, DefaultDetachKey, &armed)
	if !detach {
		t.Fatal("d in the next read should detach")
	}
	if len(send) != 0 {
		t.Fatalf("nothing should reach the PTY; got %q", send)
	}
}

func TestFilterDetachDoubledPrefixSendsLiteral(t *testing.T) {
	armed := false
	send, detach := filterDetach(
		[]byte{DefaultDetachKey, DefaultDetachKey}, DefaultDetachKey, &armed)
	if detach {
		t.Fatal("a doubled prefix means literal, not detach")
	}
	if !bytes.Equal(send, []byte{DefaultDetachKey}) {
		t.Fatalf("got %q, want one literal prefix byte", send)
	}
	if armed {
		t.Fatal("the doubled prefix should have consumed the armed state")
	}
}

func TestFilterDetachPrefixThenOtherKeyPassesBoth(t *testing.T) {
	armed := false
	// C-\ then C-c is not a detach, so the application must see both bytes:
	// swallowing the prefix would silently eat a keystroke.
	send, detach := filterDetach([]byte{DefaultDetachKey, 0x03}, DefaultDetachKey, &armed)
	if detach {
		t.Fatal("only d detaches")
	}
	if !bytes.Equal(send, []byte{DefaultDetachKey, 0x03}) {
		t.Fatalf("got %q, want prefix and key passed through", send)
	}
}

func TestFilterDetachAcceptsUppercase(t *testing.T) {
	armed := false
	_, detach := filterDetach([]byte{DefaultDetachKey, 'D'}, DefaultDetachKey, &armed)
	if !detach {
		t.Fatal("caps lock should not defeat detaching")
	}
}

// framePipe returns a reader carrying the given frames, as the host would.
func framePipe(t *testing.T, frames ...Frame) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	for _, f := range frames {
		if err := WriteFrame(&buf, f.Type, f.Payload); err != nil {
			t.Fatalf("write frame: %v", err)
		}
	}
	return &buf
}

func TestPumpOutputRendersSnapshotAndOutput(t *testing.T) {
	r := framePipe(t,
		Frame{Type: FrameSnapshot, Payload: EncodeSnapshot(12, []byte("SNAP"))},
		Frame{Type: FrameOutput, Payload: []byte("live")},
		Frame{Type: FrameExit, Payload: encodeInt32(7)},
	)
	client := &Client{r: bufio.NewReader(r)}

	var out bytes.Buffer
	code, err := pumpOutput(client, &out)
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code: got %d want 7", code)
	}
	// The sequence number prefix is protocol, not screen content.
	if got := out.String(); got != "SNAPlive" {
		t.Fatalf("rendered %q, want %q", got, "SNAPlive")
	}
}

func TestPumpOutputReportsUnexpectedClose(t *testing.T) {
	// EOF with no Exit frame means the host vanished rather than the process
	// ending, which the user needs told apart from a clean exit.
	r := framePipe(t, Frame{Type: FrameOutput, Payload: []byte("partial")})
	client := &Client{r: bufio.NewReader(r)}

	var out bytes.Buffer
	if _, err := pumpOutput(client, &out); err == nil {
		t.Fatal("a host that disappears is an error")
	} else if !strings.Contains(err.Error(), "closed the connection") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestPumpOutputSurfacesWriteFailure(t *testing.T) {
	r := framePipe(t, Frame{Type: FrameOutput, Payload: []byte("x")})
	client := &Client{r: bufio.NewReader(r)}

	if _, err := pumpOutput(client, failWriter{}); err == nil {
		t.Fatal("a failed terminal write must not be swallowed")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("terminal gone") }

func TestAttachRejectsNonTerminal(t *testing.T) {
	// Attach drives raw mode and SIGWINCH, so a pipe cannot stand in for a
	// terminal; failing early beats a confusing error from MakeRaw.
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer in.Close()

	_, err = Attach(context.Background(), AttachConfig{
		Socket: "/nonexistent.sock", In: in, Out: in,
	})
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("got %v, want a terminal-required error", err)
	}
}
