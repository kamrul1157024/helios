package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The desktop session is xrdp's: it starts the first time a login reaches
// sesman, and it outlives the connection that started it. These are the
// daemon's handles on it — starting it, finding it, and changing its DPI.

const (
	// displayTimeout is how long a new session gets to start its X server.
	displayTimeout = 45 * time.Second
	// sessionBusTimeout is how long the session gets, after its X server, to
	// bring up its bus.
	sessionBusTimeout = 20 * time.Second
)

// warmSession logs in once through guacd and stays until the session's X
// server appears, so the session exists before anyone opens the desktop and
// agents have a DISPLAY to use (D3). Leaving at the first frame is too soon:
// guacd draws its own blank frame before the RDP login has even begun. sesman
// keeps the session after the disconnect because of KillDisconnected=false.
func warmSession(ctx context.Context, addr string, cred Credentials, procRoot string, uid int) (Display, error) {
	conn, err := DialGuacd(ctx, addr, ClampSize(1280, 800, DefaultDPI), cred, hostTimezone())
	if err != nil {
		return Display{}, err
	}
	defer conn.Close()
	var writeMu sync.Mutex
	send := func(opcode string, args ...string) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err := io.WriteString(conn, Encode(opcode, args...))
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- drainFrames(conn, send) }()

	deadline := time.Now().Add(displayTimeout)
	for {
		if d, ok := findDisplay(procRoot, uid); ok {
			send("disconnect")
			return d, nil
		}
		if time.Now().After(deadline) {
			return Display{}, errors.New("no X server appeared")
		}
		select {
		case <-ctx.Done():
			return Display{}, ctx.Err()
		case err := <-ended:
			return Display{}, err
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// drainFrames reads what guacd sends during the warm-up and acknowledges
// each frame, as a viewer would; a guacd that sees no acknowledgements stops
// drawing, and xrdp with it.
func drainFrames(conn *GuacConn, send func(string, ...string) error) error {
	r := NewReader(conn.Stream)
	for {
		ins, err := r.Read()
		if err != nil {
			return fmt.Errorf("the desktop connection closed: %w", err)
		}
		switch ins.Opcode {
		case "error":
			return fmt.Errorf("the desktop session refused the login: %s", strings.Join(ins.Args, " "))
		case "sync":
			if len(ins.Args) > 0 {
				if err := send("sync", ins.Args[0]); err != nil {
					return err
				}
			}
		}
	}
}

// waitSessionBus waits for the session's D-Bus address. The X server comes
// up before the session that runs on it, and xfconf answers only once that
// session has its bus.
func waitSessionBus(ctx context.Context, procRoot string, uid int, d Display, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for sessionBus(procRoot, uid, d.Name) == "" {
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return true
}

// sessionEnv is the environment a command needs to talk to the session: its
// X server, and the session bus that xfconfd answers on.
func sessionEnv(procRoot string, uid int, d Display) []string {
	env := append(os.Environ(), "DISPLAY="+d.Name)
	if d.XAuthority != "" {
		env = append(env, "XAUTHORITY="+d.XAuthority)
	}
	if bus := sessionBus(procRoot, uid, d.Name); bus != "" {
		env = append(env, "DBUS_SESSION_BUS_ADDRESS="+bus)
	}
	return env
}

// readDPI asks the session for its Xft/DPI. Zero means it could not say,
// which is what GET /api/desktop reports for an unknown DPI.
func readDPI(ctx context.Context, procRoot string, uid int, d Display) int {
	cmd := exec.CommandContext(ctx, "xfconf-query", "-c", "xsettings", "-p", "/Xft/DPI")
	cmd.Env = sessionEnv(procRoot, uid, d)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	dpi, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return dpi
}

// applyDPI sets the session's Xft/DPI. XFCE and GTK apps redraw at the new
// size at once, with no reconnect (D9). -n creates the property on a session
// that has never had it set.
func applyDPI(ctx context.Context, procRoot string, uid int, d Display, dpi int) error {
	cmd := exec.CommandContext(ctx, "xfconf-query", "-c", "xsettings", "-p", "/Xft/DPI",
		"-n", "-t", "int", "-s", strconv.Itoa(dpi))
	cmd.Env = sessionEnv(procRoot, uid, d)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("xfconf-query: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// hostTimezone names the machine's zone for guacd's timezone instruction, or
// "" when it cannot tell, in which case the instruction is left out.
func hostTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" && !strings.HasPrefix(tz, ":") {
		return tz
	}
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(raw)); tz != "" {
			return tz
		}
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(link, "zoneinfo/"); ok {
			return filepath.Clean(zone)
		}
	}
	return ""
}
