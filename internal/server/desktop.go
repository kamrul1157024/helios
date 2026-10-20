package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/kamrul1157024/helios/internal/desktop"
)

// The remote desktop routes (docs/specs/66-remote-desktop.md). The daemon
// stands where the Guacamole web app would: it authenticates the viewer, does
// the guacd handshake itself with values only it chooses, and then relays.

// Close codes for the tunnel. guacamole-common-js reads a Guacamole status
// code from the close reason with parseInt, so each reason starts with its
// code and the client's onerror sees the same number the socket closed with.
const (
	closeBadTicket      websocket.StatusCode = 4401
	closeTooManyViewers websocket.StatusCode = 4429
	closeNotReady       websocket.StatusCode = 4503
	closeGuacdFailure   websocket.StatusCode = websocket.StatusInternalError
)

// handleDesktopStatus reports the desktop's state, looking at the machine
// again first so an install done from the CLI shows up without a restart.
//
// GET /api/desktop
func (s *PublicServer) handleDesktopStatus(w http.ResponseWriter, r *http.Request) {
	m := s.shared.Desktop
	if m == nil {
		jsonResponse(w, http.StatusOK, desktop.Status{State: desktop.StateUnsupported, Missing: []string{}})
		return
	}
	m.Refresh()
	jsonResponse(w, http.StatusOK, m.Status())
}

// handleDesktopInstall starts the install of whatever is missing (D4).
//
// POST /api/desktop/install
func (s *PublicServer) handleDesktopInstall(w http.ResponseWriter, r *http.Request) {
	m := s.shared.Desktop
	if m == nil {
		jsonErrorCode(w, "remote desktop needs a Linux host", http.StatusConflict, "unsupported")
		return
	}
	err := m.Install()
	switch {
	case err == nil:
		jsonResponse(w, http.StatusAccepted, map[string]any{"started": true})
	case errors.Is(err, desktop.ErrBusy):
		jsonErrorCode(w, err.Error(), http.StatusConflict, "busy")
	case errors.Is(err, desktop.ErrDisabled):
		jsonErrorCode(w, err.Error(), http.StatusConflict, "disabled")
	case errors.Is(err, desktop.ErrNeedsRoot):
		jsonResponse(w, http.StatusForbidden, map[string]any{
			"error":           http.StatusText(http.StatusForbidden),
			"message":         err.Error(),
			"code":            "needs_root",
			"install_command": desktop.InstallCommand,
		})
	case errors.Is(err, desktop.ErrNotReady):
		jsonErrorCode(w, "remote desktop needs a Linux host", http.StatusConflict, "unsupported")
	default:
		jsonError(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleDesktopTicket trades the bearer token for a single-use tunnel ticket.
//
// POST /api/desktop/ticket
func (s *PublicServer) handleDesktopTicket(w http.ResponseWriter, r *http.Request) {
	m := s.shared.Desktop
	if m == nil {
		jsonErrorCode(w, "remote desktop needs a Linux host", http.StatusConflict, "unsupported")
		return
	}
	ticket, expires, err := m.Tickets.Issue(s.deviceName(r))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

// deviceName names the caller for the audit log: the device's name and its
// key id, or just the id when the device has no name.
func (s *PublicServer) deviceName(r *http.Request) string {
	kid, _ := r.Context().Value(deviceKIDKey).(string)
	if kid == "" {
		return "unknown device"
	}
	if s.shared.DB != nil {
		if d, err := s.shared.DB.GetActiveDevice(kid); err == nil && d != nil && d.Name != "" {
			return fmt.Sprintf("%s (%s)", d.Name, kid)
		}
	}
	return kid
}

// handleDesktopDisplay changes the session's DPI live (D9).
//
// PUT /api/desktop/display
func (s *PublicServer) handleDesktopDisplay(w http.ResponseWriter, r *http.Request) {
	m := s.shared.Desktop
	if m == nil {
		jsonErrorCode(w, "remote desktop needs a Linux host", http.StatusConflict, "unsupported")
		return
	}
	var req struct {
		DPI int `json:"dpi"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	dpi, err := m.SetDPI(r.Context(), req.DPI)
	switch {
	case err == nil:
		jsonResponse(w, http.StatusOK, map[string]any{"dpi": dpi})
	case errors.Is(err, desktop.ErrBadDPI):
		jsonErrorCode(w, err.Error(), http.StatusBadRequest, "bad_dpi")
	case errors.Is(err, desktop.ErrNotReady):
		jsonErrorCode(w, "the desktop has no session yet", http.StatusConflict, "not_ready")
	default:
		jsonError(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleDesktopTunnel is the viewer's WebSocket (D2). It sits outside bearer
// auth, because a browser WebSocket cannot send the header; the ticket is the
// auth. Every failure is a close code rather than an HTTP error, since a
// WebSocket client never sees the body of a refused upgrade.
//
// GET /api/desktop/tunnel?ticket=<t>&width=<px>&height=<px>&dpi=<n>
func (s *PublicServer) handleDesktopTunnel(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same reasoning as the terminal relay: no cookies, so no cross-site
		// request can borrow a session, and the ticket is single-use.
		InsecureSkipVerify: true,
		Subprotocols:       []string{"guacamole"},
	})
	if err != nil {
		log.Printf("desktop tunnel: accept: %v", err)
		return
	}
	conn.SetReadLimit(16 << 20)

	m := s.shared.Desktop
	if m == nil {
		closeTunnel(conn, closeNotReady, "desktop not ready")
		return
	}
	device, ok := m.Tickets.Redeem(r.URL.Query().Get("ticket"))
	if !ok {
		closeTunnel(conn, closeBadTicket, "bad or expired ticket")
		return
	}

	// Only the size and DPI come from the query. Anything else a client adds
	// — a host, a port, a protocol — is never read.
	// Zero means "not given"; ClampSize turns it into the minimum size and
	// the default DPI.
	size := desktop.ClampSize(queryInt(r, "width", 0, desktop.MaxWidth),
		queryInt(r, "height", 0, desktop.MaxHeight), queryInt(r, "dpi", 0, desktop.MaxDPI))
	guac, release, err := m.OpenViewer(r.Context(), size)
	switch {
	case errors.Is(err, desktop.ErrNotReady):
		s.startOnDemand(m)
		closeTunnel(conn, closeNotReady, "desktop not ready")
		return
	case errors.Is(err, desktop.ErrTooManyViewers):
		closeTunnel(conn, closeTooManyViewers, "too many viewers")
		return
	case err != nil:
		log.Printf("desktop tunnel: %s: %v", device, err)
		closeTunnel(conn, closeGuacdFailure, "desktop service failed")
		return
	}
	defer release()

	log.Printf("desktop tunnel: open by %s at %dx%d@%d", device, size.Width, size.Height, size.DPI)
	started := time.Now()
	relayGuacamole(r.Context(), conn, guac)
	log.Printf("desktop tunnel: closed for %s after %s", device, time.Since(started).Round(time.Second))
}

// startOnDemand begins the install when a viewer opens a desktop that is
// missing and the daemon can get root (D4). The viewer's tunnel still closes;
// its client follows the install through GET /api/desktop and the SSE event.
func (s *PublicServer) startOnDemand(m *desktop.Manager) {
	m.Refresh()
	st := m.Status()
	if st.State != desktop.StateMissing || !st.CanInstall || !st.Enabled {
		return
	}
	if err := m.Install(); err != nil && !errors.Is(err, desktop.ErrBusy) {
		log.Printf("desktop tunnel: on-demand install: %v", err)
	}
}

func closeTunnel(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	conn.Close(code, fmt.Sprintf("%d %s", code, reason))
}

// relayGuacamole carries Guacamole instructions between the viewer and guacd
// until either side leaves.
//
// It is almost the plain copy terminal_ws.go does, with two differences the
// protocol forces. Toward the viewer, each WebSocket message carries whole
// instructions, because a text message must be valid UTF-8 and cannot end
// mid-character. From the viewer, the tunnel's own pings — instructions with
// the empty opcode — are answered here and never reach guacd, which is what
// guacamole-common-js expects of the server end of its tunnel.
func relayGuacamole(ctx context.Context, conn *websocket.Conn, guac *desktop.GuacConn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, []byte(desktop.Encode("", uuid.NewString()))); err != nil {
		guac.Close()
		return
	}

	done := make(chan error, 2)
	go func() { done <- relayFromGuacd(ctx, conn, guac) }()
	go func() { done <- relayToGuacd(ctx, conn, guac) }()

	err := <-done
	guac.Close()
	cancel()
	<-done

	if err != nil && !isDisconnect(err) {
		log.Printf("desktop tunnel: relay: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// maxPending bounds the bytes held back waiting for an instruction to end. A
// single image instruction is far smaller; this only stops a broken stream
// from growing the buffer without end.
const maxPending = 32 << 20

func relayFromGuacd(ctx context.Context, conn *websocket.Conn, guac *desktop.GuacConn) error {
	var split desktop.Splitter
	var pending []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := guac.Stream.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			whole, ferr := split.Feed(chunk)
			if ferr != nil {
				return ferr
			}
			if whole > 0 {
				msg := append(pending, chunk[:whole]...)
				if werr := conn.Write(ctx, websocket.MessageText, msg); werr != nil {
					return werr
				}
				pending = append([]byte(nil), chunk[whole:]...)
			} else {
				pending = append(pending, chunk...)
			}
			if len(pending) > maxPending {
				return fmt.Errorf("guacd instruction over %d bytes", maxPending)
			}
		}
		if err != nil {
			return err
		}
	}
}

func relayToGuacd(ctx context.Context, conn *websocket.Conn, guac *desktop.GuacConn) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		instructions, err := desktop.ParseAll(string(data))
		if err != nil {
			return fmt.Errorf("viewer sent %w", err)
		}
		for _, ins := range instructions {
			if ins.Opcode == "" {
				if len(ins.Args) > 0 && ins.Args[0] == "ping" {
					if err := conn.Write(ctx, websocket.MessageText, []byte(desktop.Encode("", ins.Args...))); err != nil {
						return err
					}
				}
				continue
			}
			if _, err := guac.Write([]byte(desktop.Encode(ins.Opcode, ins.Args...))); err != nil {
				return err
			}
		}
	}
}

// desktopPage serves the embedded WebView page. no-cache makes the WebView
// revalidate, so a daemon upgrade is never paired with yesterday's client.
func desktopPage() http.Handler {
	files := http.FileServerFS(desktop.WebFS())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}
