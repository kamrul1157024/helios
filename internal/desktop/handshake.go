package desktop

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Size is the only part of a connection a viewer chooses: how many pixels the
// remote screen has and the DPI the viewer reports. Everything else in the
// handshake is fixed by the daemon (D2).
type Size struct {
	Width, Height, DPI int
}

// The limits from D9. A request outside them is clamped, not refused, so a
// viewer on an odd screen still gets a picture.
const (
	MinWidth   = 640
	MaxWidth   = 7680
	MinHeight  = 480
	MaxHeight  = 4320
	MinDPI     = 72
	MaxDPI     = 288
	DefaultDPI = 96
)

// ClampSize brings a requested size inside the limits. A zero DPI means the
// viewer named none, which is the 96 every desktop assumes.
func ClampSize(width, height, dpi int) Size {
	if dpi == 0 {
		dpi = DefaultDPI
	}
	return Size{
		Width:  clamp(width, MinWidth, MaxWidth),
		Height: clamp(height, MinHeight, MaxHeight),
		DPI:    clamp(dpi, MinDPI, MaxDPI),
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Credentials log the daemon in to xrdp. The password is the secret from
// ~/.helios/desktop/secret, which PAM checks against root's hash (D1).
type Credentials struct {
	Username string
	Password string
}

// The RDP target is not a parameter anywhere. A tunnel that took a host or a
// port from its caller would be a proxy to any machine the daemon can reach.
const (
	rdpHost = "127.0.0.1"
	rdpPort = "3389"
)

// protocolVersion is what guacamole-common-js 1.6.0 speaks. guacd offers its
// own version as the first element of args and expects the client's back.
const protocolVersion = "VERSION_1_5_0"

// imageTypes are the formats every browser decodes. The bundle is built
// without WebP, so offering it would only let guacd pick something it cannot
// encode.
var imageTypes = []string{"image/jpeg", "image/png"}

// connectValues answers guacd's args instruction: one value for each name, in
// the order guacd listed them. The values come from the daemon alone; the
// viewer contributes the size and nothing else.
func connectValues(names []string, size Size, cred Credentials) []string {
	fixed := map[string]string{
		"hostname":         rdpHost,
		"port":             rdpPort,
		"username":         cred.Username,
		"password":         cred.Password,
		"width":            strconv.Itoa(size.Width),
		"height":           strconv.Itoa(size.Height),
		"dpi":              strconv.Itoa(size.DPI),
		"resize-method":    "display-update",
		"enable-wallpaper": "false",
		"color-depth":      "24",
		"security":         "any",
		"ignore-cert":      "true",
		// Audio, drives and printing need UI this spec does not build.
		"disable-audio": "true",
		"client-name":   "Helios",
	}
	values := make([]string, len(names))
	for i, name := range names {
		if strings.HasPrefix(name, "VERSION_") {
			values[i] = protocolVersion
			continue
		}
		values[i] = fixed[name]
	}
	return values
}

// handshakeInstructions renders everything the daemon sends after args, in
// the order guacd reads it. The size instruction carries the same DPI as the
// connect argument: guacd scales the requested size by their ratio, and a
// ratio of one is what makes remote pixels equal requested pixels (D9).
func handshakeInstructions(args []string, size Size, cred Credentials, timezone string) string {
	var b strings.Builder
	b.WriteString(Encode("size", strconv.Itoa(size.Width), strconv.Itoa(size.Height), strconv.Itoa(size.DPI)))
	b.WriteString(Encode("audio"))
	b.WriteString(Encode("video"))
	b.WriteString(Encode("image", imageTypes...))
	if timezone != "" {
		b.WriteString(Encode("timezone", timezone))
	}
	b.WriteString(Encode("connect", connectValues(args, size, cred)...))
	return b.String()
}

// handshakeTimeout bounds the whole exchange with guacd, including the RDP
// login behind "ready". A first login also starts the X session, so it is
// generous.
const handshakeTimeout = 30 * time.Second

// GuacConn is a guacd connection past its handshake. Reads go through Stream,
// which holds any bytes guacd sent right after "ready".
type GuacConn struct {
	net.Conn
	Stream io.Reader
	// ID is the connection id guacd announced in "ready".
	ID string
}

// DialGuacd opens a connection to guacd and completes the RDP handshake.
func DialGuacd(ctx context.Context, addr string, size Size, cred Credentials, timezone string) (*GuacConn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial guacd: %w", err)
	}
	deadline := time.Now().Add(handshakeTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set guacd deadline: %w", err)
	}
	gc, err := handshake(conn, size, cred, timezone)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear guacd deadline: %w", err)
	}
	return gc, nil
}

func handshake(conn net.Conn, size Size, cred Credentials, timezone string) (*GuacConn, error) {
	if _, err := io.WriteString(conn, Encode("select", "rdp")); err != nil {
		return nil, fmt.Errorf("send select: %w", err)
	}
	r := NewReader(conn)
	args, err := expect(r, "args")
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(conn, handshakeInstructions(args, size, cred, timezone)); err != nil {
		return nil, fmt.Errorf("send connect: %w", err)
	}
	ready, err := expect(r, "ready")
	if err != nil {
		return nil, err
	}
	id := ""
	if len(ready) > 0 {
		id = ready[0]
	}
	return &GuacConn{Conn: conn, Stream: r.r, ID: id}, nil
}

// expect reads the next instruction and requires the given opcode. guacd
// answers a failed handshake with an error instruction, whose text is the
// most useful thing to report.
func expect(r *Reader, opcode string) ([]string, error) {
	ins, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read %s from guacd: %w", opcode, err)
	}
	if ins.Opcode == "error" {
		return nil, fmt.Errorf("guacd refused the connection: %s", strings.Join(ins.Args, " "))
	}
	if ins.Opcode != opcode {
		return nil, fmt.Errorf("guacd sent %q, expected %q", ins.Opcode, opcode)
	}
	return ins.Args, nil
}
