package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/desktop"
)

// viewer is one tunnel, read the way guacamole-common-js reads it: whole
// instructions, with every sync acknowledged so guacd keeps sending frames.
type viewer struct {
	conn  *websocket.Conn
	queue []desktop.Instruction
	uuid  string
}

func (c *client) ticket() (string, error) {
	var t struct {
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expires_at"`
	}
	code, err := c.do("POST", "/api/desktop/ticket", nil, &t)
	if err != nil {
		return "", err
	}
	if code != 200 || t.Ticket == "" {
		return "", fmt.Errorf("ticket: HTTP %d", code)
	}
	return t.Ticket, nil
}

func (c *client) tunnelURL(query string) string {
	return "ws" + strings.TrimPrefix(c.base, "http") + "/api/desktop/tunnel?" + query
}

// dial opens a tunnel and returns it without reading anything.
func (c *client) dial(query string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, c.tunnelURL(query), &websocket.DialOptions{Subprotocols: []string{"guacamole"}})
	if err != nil {
		return nil, err
	}
	if p := resp.Header.Get("Sec-WebSocket-Protocol"); p != "guacamole" {
		conn.CloseNow()
		return nil, fmt.Errorf("subprotocol %q", p)
	}
	conn.SetReadLimit(64 << 20)
	return conn, nil
}

// openViewer opens a tunnel with a fresh ticket and reads the tunnel UUID.
// extra is appended to the query, which is how the probe checks that a
// client cannot steer the connection anywhere else.
func (c *client) openViewer(width, height, dpi int, extra string) (*viewer, error) {
	t, err := c.ticket()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("ticket", t)
	q.Set("width", fmt.Sprint(width))
	q.Set("height", fmt.Sprint(height))
	q.Set("dpi", fmt.Sprint(dpi))
	query := q.Encode()
	if extra != "" {
		query += "&" + extra
	}
	conn, err := c.dial(query)
	if err != nil {
		return nil, err
	}
	v := &viewer{conn: conn}
	first, err := v.next(10 * time.Second)
	if err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("tunnel uuid: %w", err)
	}
	if first.Opcode != "" || len(first.Args) != 1 || len(first.Args[0]) != 36 {
		conn.CloseNow()
		return nil, fmt.Errorf("first instruction %+v is not the tunnel uuid", first)
	}
	v.uuid = first.Args[0]
	return v, nil
}

// next returns the next instruction, acknowledging syncs on the way.
func (v *viewer) next(timeout time.Duration) (desktop.Instruction, error) {
	for len(v.queue) == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, msg, err := v.conn.Read(ctx)
		cancel()
		if err != nil {
			return desktop.Instruction{}, err
		}
		ins, err := desktop.ParseAll(string(msg))
		if err != nil {
			return desktop.Instruction{}, fmt.Errorf("message is not whole instructions: %w", err)
		}
		v.queue = append(v.queue, ins...)
	}
	ins := v.queue[0]
	v.queue = v.queue[1:]
	if ins.Opcode == "sync" && len(ins.Args) > 0 {
		if err := v.send("sync", ins.Args[0]); err != nil {
			return ins, err
		}
	}
	return ins, nil
}

// waitFor reads until match accepts an instruction, and counts what it saw.
func (v *viewer) waitFor(timeout time.Duration, match func(desktop.Instruction) bool) (desktop.Instruction, map[string]int, error) {
	seen := map[string]int{}
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return desktop.Instruction{}, seen, fmt.Errorf("timed out; saw %v", seen)
		}
		ins, err := v.next(left)
		if err != nil {
			return desktop.Instruction{}, seen, fmt.Errorf("%w; saw %v", err, seen)
		}
		seen[ins.Opcode]++
		if ins.Opcode == "error" {
			return ins, seen, fmt.Errorf("guacd error: %v", ins.Args)
		}
		if match(ins) {
			return ins, seen, nil
		}
	}
}

func (v *viewer) send(opcode string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return v.conn.Write(ctx, websocket.MessageText, []byte(desktop.Encode(opcode, args...)))
}

func (v *viewer) close() {
	v.send("disconnect")
	v.conn.Close(websocket.StatusNormalClosure, "")
}

// closeCodeFor dials a tunnel and returns the code it is closed with.
func (c *client) closeCodeFor(query string) (websocket.StatusCode, string, error) {
	conn, err := c.dial(query)
	if err != nil {
		return 0, "", err
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if asClose(err, &ce) {
				return ce.Code, ce.Reason, nil
			}
			return 0, "", err
		}
	}
}

// isScreenSize matches guacd's size instruction for the default layer.
func isScreenSize(w, h int) func(desktop.Instruction) bool {
	return func(ins desktop.Instruction) bool {
		return ins.Opcode == "size" && len(ins.Args) == 3 && ins.Args[0] == "0" &&
			ins.Args[1] == fmt.Sprint(w) && ins.Args[2] == fmt.Sprint(h)
	}
}
