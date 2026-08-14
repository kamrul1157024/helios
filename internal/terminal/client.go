package terminal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// Client is a viewer connection to a Host.
type Client struct {
	conn net.Conn
	r    *bufio.Reader

	mu     sync.Mutex
	closed bool

	// Seq tracks how much of the stream has been consumed, so a reconnect can
	// ask to resume rather than resync.
	seq uint64
}

// Dial connects to a host socket and completes the Hello handshake.
func Dial(socket string, hello Hello) (*Client, error) {
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial terminal host %s: %w", socket, err)
	}
	c := &Client{conn: conn, r: bufio.NewReaderSize(conn, 64*1024)}
	if err := WriteJSONFrame(conn, FrameHello, hello); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Probe reports whether a host socket is alive. A successful dial is the
// liveness check, replacing the system-wide process-table scan the tmux
// integration used.
func Probe(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Next reads the next frame from the host.
func (c *Client) Next() (Frame, error) {
	f, err := ReadFrame(c.r)
	if err != nil {
		return f, err
	}
	switch f.Type {
	case FrameOutput:
		c.seq += uint64(len(f.Payload))
	case FrameSnapshot:
		if seq, _, err := DecodeSnapshot(f.Payload); err == nil {
			c.seq = seq
		}
	}
	return f, nil
}

// Seq returns the sequence number the client has consumed up to.
func (c *Client) Seq() uint64 { return c.seq }

// Send writes input bytes to the hosted process.
func (c *Client) Send(p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.conn, FrameInput, p)
}

// Resize requests a new PTY size. Only honoured for interactive viewers.
func (c *Client) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.conn, FrameResize, EncodeResize(cols, rows))
}

// SetOverlay paints a modal over the session on every viewer. Honoured only
// for the control connection.
func (c *Client) SetOverlay(o Overlay) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteJSONFrame(c.conn, FrameOverlaySet, o)
}

// ClearOverlay takes the modal down and hands input back to the PTY.
func (c *Client) ClearOverlay() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.conn, FrameOverlayClear, nil)
}

// Ping asks the host for a Pong.
func (c *Client) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.conn, FramePing, nil)
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

// ParseStatus decodes a Status frame payload.
func ParseStatus(payload []byte) (Status, error) {
	var s Status
	if err := json.Unmarshal(payload, &s); err != nil {
		return s, fmt.Errorf("parse status frame: %w", err)
	}
	return s, nil
}

// ParseExit decodes an Exit frame payload.
func ParseExit(payload []byte) int { return int(decodeInt32(payload)) }
