package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/desktop"
)

// idleSystem is a machine with nothing installed and no sudo, so a Manager
// built on it never touches the host.
type idleSystem struct{}

func (idleSystem) Run(context.Context, ...string) (string, error) { return "", errors.New("no") }
func (idleSystem) Root(context.Context, ...string) (string, error) {
	return "", errors.New("no root in tests")
}
func (idleSystem) ReadFile(string) ([]byte, error)             { return nil, os.ErrNotExist }
func (idleSystem) WriteFile(string, []byte, os.FileMode) error { return errors.New("read-only") }
func (idleSystem) CanSudo(context.Context) bool                { return false }
func (idleSystem) SetOutput(func(string))                      {}
func (idleSystem) WriteRootFile(context.Context, string, []byte, os.FileMode) error {
	return errors.New("no root in tests")
}

func newDesktopTunnelServer(t *testing.T) (*httptest.Server, *desktop.Manager) {
	t.Helper()
	m := desktop.NewManager(desktop.Config{HeliosDir: t.TempDir(), Sys: idleSystem{}, ProcRoot: t.TempDir()})
	t.Cleanup(m.Close)
	s := &PublicServer{shared: &Shared{Desktop: m}}
	srv := httptest.NewServer(http.HandlerFunc(s.handleDesktopTunnel))
	t.Cleanup(srv.Close)
	return srv, m
}

func dialTunnel(t *testing.T, srv *httptest.Server, query string) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/desktop/tunnel?" + query
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"guacamole"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "guacamole" {
		t.Errorf("subprotocol = %q", got)
	}
	_, _, err = conn.Read(ctx)
	return websocket.CloseStatus(err)
}

func TestDesktopTunnelRefusesBadTicket(t *testing.T) {
	srv, _ := newDesktopTunnelServer(t)
	if code := dialTunnel(t, srv, "ticket=forged&width=1280&height=800"); code != closeBadTicket {
		t.Fatalf("close code = %d, want %d", code, closeBadTicket)
	}
}

func TestDesktopTunnelTicketWorksOnce(t *testing.T) {
	srv, m := newDesktopTunnelServer(t)
	ticket, _, err := m.Tickets.Issue("test device")
	if err != nil {
		t.Fatal(err)
	}
	// The desktop is not ready, so a good ticket gets as far as 4503 — and
	// spends itself on the way.
	if code := dialTunnel(t, srv, "ticket="+ticket); code != closeNotReady {
		t.Fatalf("first use: close code = %d, want %d", code, closeNotReady)
	}
	if code := dialTunnel(t, srv, "ticket="+ticket); code != closeBadTicket {
		t.Fatalf("second use: close code = %d, want %d", code, closeBadTicket)
	}
}

// relayPair runs relayGuacamole between a real WebSocket and a pipe that
// stands in for guacd.
func relayPair(t *testing.T) (*websocket.Conn, net.Conn) {
	t.Helper()
	guacSide, relaySide := net.Pipe()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"guacamole"}})
		if err != nil {
			return
		}
		relayGuacamole(r.Context(), conn, &desktop.GuacConn{Conn: relaySide, Stream: relaySide})
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow(); guacSide.Close() })
	return ws, guacSide
}

func TestRelaySendsTunnelUUIDFirst(t *testing.T) {
	ws, _ := relayPair(t)
	_, msg, err := ws.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ins, err := desktop.ParseAll(string(msg))
	if err != nil || len(ins) != 1 || ins[0].Opcode != "" || len(ins[0].Args) != 1 || len(ins[0].Args[0]) != 36 {
		t.Fatalf("first message = %q", msg)
	}
}

func TestRelayAnswersPingsAndForwardsTheRest(t *testing.T) {
	ws, guac := relayPair(t)
	ctx := context.Background()
	if _, _, err := ws.Read(ctx); err != nil { // the UUID
		t.Fatal(err)
	}

	ping := desktop.Encode("", "ping", "1700000000000")
	size := desktop.Encode("size", "1600", "900")
	if err := ws.Write(ctx, websocket.MessageText, []byte(ping+size)); err != nil {
		t.Fatal(err)
	}
	_, echo, err := ws.Read(ctx)
	if err != nil || string(echo) != ping {
		t.Fatalf("ping answer = %q, %v", echo, err)
	}
	got := make([]byte, len(size))
	if _, err := io.ReadFull(guac, got); err != nil || string(got) != size {
		t.Fatalf("guacd got %q, %v; the ping must not reach it", got, err)
	}
}

func TestRelayFramesWholeInstructions(t *testing.T) {
	ws, guac := relayPair(t)
	ctx := context.Background()
	if _, _, err := ws.Read(ctx); err != nil {
		t.Fatal(err)
	}
	stream := desktop.Encode("blob", "1", "ünï日本") + desktop.Encode("sync", "42")
	go func() {
		// One byte at a time, so every cut — mid-length, mid-character — is
		// tried.
		for i := 0; i < len(stream); i++ {
			guac.Write([]byte{stream[i]})
		}
	}()
	var received strings.Builder
	for received.Len() < len(stream) {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := desktop.ParseAll(string(msg)); err != nil {
			t.Fatalf("message %q is not whole instructions: %v", msg, err)
		}
		received.Write(msg)
	}
	if received.String() != stream {
		t.Fatalf("received %q, want %q", received.String(), stream)
	}
}
