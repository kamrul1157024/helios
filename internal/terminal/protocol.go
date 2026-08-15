// Package terminal hosts PTY-backed sessions and serves their output from
// memory to any number of concurrent viewers. It replaces the tmux
// integration described in docs/specs/29-terminal-host-replacing-tmux.md.
package terminal

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// FrameType identifies a wire frame. Values are fixed by the spec and must
// not be renumbered.
type FrameType uint8

const (
	FrameHello    FrameType = 0x01
	FrameSnapshot FrameType = 0x02
	FrameOutput   FrameType = 0x03
	FrameInput    FrameType = 0x04
	FrameResize   FrameType = 0x05
	FrameStatus   FrameType = 0x06
	FrameExit     FrameType = 0x07
	FramePing     FrameType = 0x08
	FramePong     FrameType = 0x09

	// FrameOverlaySet and FrameOverlayClear travel from the control viewer to
	// the host; FrameOverlayInput carries keystrokes back while an overlay is
	// up. See docs/specs/36-helios-owned-hitl.md.
	FrameOverlaySet   FrameType = 0x0a
	FrameOverlayClear FrameType = 0x0b
	FrameOverlayInput FrameType = 0x0c

	// FramePaste carries prompt text the host must deliver as a paste rather
	// than as keystrokes. Sending it as input instead lets the application
	// mistake the trailing Enter for part of the burst, which loses the
	// submit. See docs/specs/37-prompt-delivery-reliability.md.
	FramePaste FrameType = 0x0d
)

// MaxFrameSize bounds a single frame so a corrupt or hostile length prefix
// cannot make the peer allocate without limit.
const MaxFrameSize = 8 << 20 // 8 MiB

// ErrFrameTooLarge is returned when a frame exceeds MaxFrameSize.
var ErrFrameTooLarge = errors.New("terminal: frame exceeds maximum size")

func (t FrameType) String() string {
	switch t {
	case FrameHello:
		return "hello"
	case FrameSnapshot:
		return "snapshot"
	case FrameOutput:
		return "output"
	case FrameInput:
		return "input"
	case FrameResize:
		return "resize"
	case FrameStatus:
		return "status"
	case FrameExit:
		return "exit"
	case FramePing:
		return "ping"
	case FramePong:
		return "pong"
	case FrameOverlaySet:
		return "overlay-set"
	case FrameOverlayClear:
		return "overlay-clear"
	case FrameOverlayInput:
		return "overlay-input"
	case FramePaste:
		return "paste"
	default:
		return fmt.Sprintf("unknown(0x%02x)", uint8(t))
	}
}

// Role determines whether a viewer participates in PTY size negotiation.
// Observers (mobile) must never shrink the PTY for an interactive desktop.
type Role string

const (
	RoleInteractive Role = "interactive"
	RoleObserver    Role = "observer"
	// RoleControl is the daemon. It has observer semantics — it never votes on
	// the PTY size — plus the right to set overlays and receive the keystrokes
	// they capture. Exactly one control viewer is honoured at a time.
	RoleControl Role = "control"
)

// Hello is the first frame a viewer sends.
type Hello struct {
	Role Role `json:"role"`
	Cols int  `json:"cols"`
	Rows int  `json:"rows"`
	// Since requests replay from a sequence number. Zero means "snapshot me".
	Since uint64 `json:"since"`
	// Name is advisory, used for the writer indicator.
	Name string `json:"name,omitempty"`
}

// State is the lifecycle state of the hosted process.
type State string

const (
	StateWarming State = "warming"
	StateReady   State = "ready"
	StateBusy    State = "busy"
	StateExited  State = "exited"
)

// Status is advisory UI state broadcast to viewers.
type Status struct {
	State   State  `json:"state"`
	Writer  string `json:"writer,omitempty"`
	Viewers int    `json:"viewers"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

// Frame is a decoded wire frame. Payload aliases the read buffer only for the
// duration of the callback in ReadFrame's streaming form; the returned Frame
// from DecodeFrame owns its bytes.
type Frame struct {
	Type    FrameType
	Payload []byte
}

// WriteFrame writes a length-prefixed frame: uint32 length (type + payload),
// uint8 type, then payload.
func WriteFrame(w io.Writer, t FrameType, payload []byte) error {
	if len(payload)+1 > MaxFrameSize {
		return ErrFrameTooLarge
	}
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)+1))
	hdr[4] = byte(t)
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// WriteJSONFrame marshals v and writes it as a frame.
func WriteJSONFrame(w io.Writer, t FrameType, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s frame: %w", t, err)
	}
	return WriteFrame(w, t, b)
}

// ReadFrame reads one frame. It returns ErrFrameTooLarge for an oversized
// length prefix rather than attempting the allocation.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Frame{}, io.EOF
		}
		return Frame{}, fmt.Errorf("read frame header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[0:4])
	if n == 0 {
		return Frame{}, errors.New("terminal: zero-length frame")
	}
	if n > MaxFrameSize {
		return Frame{}, ErrFrameTooLarge
	}
	f := Frame{Type: FrameType(hdr[4])}
	if n > 1 {
		f.Payload = make([]byte, n-1)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return Frame{}, fmt.Errorf("read frame payload: %w", err)
		}
	}
	return f, nil
}

// EncodeSnapshot prefixes ANSI resync bytes with the sequence number they
// correspond to, so a viewer knows where to resume the live stream.
func EncodeSnapshot(seq uint64, ansi []byte) []byte {
	out := make([]byte, 8+len(ansi))
	binary.BigEndian.PutUint64(out[0:8], seq)
	copy(out[8:], ansi)
	return out
}

// DecodeSnapshot is the inverse of EncodeSnapshot.
func DecodeSnapshot(payload []byte) (seq uint64, ansi []byte, err error) {
	if len(payload) < 8 {
		return 0, nil, errors.New("terminal: snapshot payload too short")
	}
	return binary.BigEndian.Uint64(payload[0:8]), payload[8:], nil
}

// EncodeResize encodes cols and rows.
func EncodeResize(cols, rows int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b[0:2], uint16(cols))
	binary.BigEndian.PutUint16(b[2:4], uint16(rows))
	return b
}

// DecodeResize is the inverse of EncodeResize.
func DecodeResize(payload []byte) (cols, rows int, err error) {
	if len(payload) < 4 {
		return 0, 0, errors.New("terminal: resize payload too short")
	}
	return int(binary.BigEndian.Uint16(payload[0:2])),
		int(binary.BigEndian.Uint16(payload[2:4])), nil
}
