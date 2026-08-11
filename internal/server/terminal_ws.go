package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/backend"
)

// wsDialTimeout bounds the hop from the daemon to a session's terminal host.
// Both ends are on this machine, so anything slower means the host is wedged.
const wsDialTimeout = 3 * time.Second

// handleSessionTerminal bridges a WebSocket to a session's terminal host.
//
// The daemon is a byte relay, not a translator: the frame protocol from
// internal/terminal is passed through untouched, so a remote viewer runs the
// exact same codec as a local one and there is no second wire format to keep
// in sync. Everything policy-related — role, size, replay position — is in the
// client's own Hello frame, which crosses this bridge like any other bytes.
//
// GET /api/sessions/{id}/terminal[?wake=1]
func (s *PublicServer) handleSessionTerminal(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/terminal")
	if id == "" {
		jsonError(w, "session id required", http.StatusBadRequest)
		return
	}

	socket, err := s.shared.terminalEndpoint(id, r.URL.Query().Get("wake") == "1")
	if err != nil {
		writeTerminalError(w, err)
		return
	}

	// Dialled before the upgrade so a cold or wedged host is reported as an
	// HTTP error the client can read, rather than an immediate WS close.
	host, err := net.DialTimeout("unix", socket, wsDialTimeout)
	if err != nil {
		jsonError(w, fmt.Sprintf("terminal host unreachable: %v", err), http.StatusBadGateway)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same-origin checks protect browsers from a hostile page borrowing a
		// user's cookies. This API is bearer-authenticated and has no cookies,
		// and its clients are native apps that send no Origin at all.
		InsecureSkipVerify: true,
	})
	if err != nil {
		host.Close()
		log.Printf("terminal ws: accept %s: %v", id, err)
		return
	}
	// The stream is long-lived and entirely client-paced, so it gets no
	// timeout beyond the ones the two ends impose on each other.
	conn.SetReadLimit(-1)

	relayTerminal(r.Context(), conn, host, id)
}

// relayTerminal copies bytes in both directions until either side goes away.
func relayTerminal(ctx context.Context, conn *websocket.Conn, host net.Conn, sessionID string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// NetConn turns the message stream into a byte stream, which is what makes
	// the relay a plain copy: WebSocket message boundaries carry no meaning
	// here because the frame protocol already delimits itself.
	ws := websocket.NetConn(ctx, conn, websocket.MessageBinary)

	done := make(chan error, 2)
	go func() { _, err := io.Copy(host, ws); done <- err }()
	go func() { _, err := io.Copy(ws, host); done <- err }()

	err := <-done
	// Closing the host unblocks the other copy; without it the second
	// goroutine would linger until the process it mirrors exits.
	host.Close()
	cancel()
	<-done

	if err != nil && !isDisconnect(err) {
		log.Printf("terminal ws: relay %s: %v", sessionID, err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// isDisconnect reports whether an error is an ordinary peer hang-up rather
// than something worth logging.
func isDisconnect(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	}
	return false
}

// errTerminalCold reports a session with no live host, which a client can fix
// by retrying with wake=1.
var errTerminalCold = errors.New("session has no live terminal")

// terminalEndpoint resolves a session's viewer address, optionally warming a
// cold session first.
func (sh *Shared) terminalEndpoint(sessionID string, wake bool) (string, error) {
	viewer, ok := sh.Backend.(backend.Viewer)
	if !ok {
		return "", fmt.Errorf("backend %s cannot stream terminals", sh.Backend.Name())
	}

	if socket, ok := viewer.Endpoint(sessionID); ok && sh.Backend.Alive(sessionID) {
		return socket, nil
	}
	if !wake {
		return "", errTerminalCold
	}

	session, err := sh.DB.GetSession(sessionID)
	if err != nil || session == nil {
		return "", fmt.Errorf("session not found")
	}
	if _, err := sh.startTerminal(sessionID, session.CWD, ""); err != nil {
		return "", fmt.Errorf("wake session: %w", err)
	}
	socket, ok := viewer.Endpoint(sessionID)
	if !ok {
		return "", fmt.Errorf("session woke without an endpoint")
	}
	return socket, nil
}

// handleSessionWake brings a cold session's terminal up without attaching to
// it, so a client can pay the warm-up cost before the user opens the view.
//
// POST /api/sessions/{id}/wake
func (s *PublicServer) handleSessionWake(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/wake")
	socket, err := s.shared.terminalEndpoint(id, true)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true, "terminal": socket,
	})
}

func writeTerminalError(w http.ResponseWriter, err error) {
	if errors.Is(err, errTerminalCold) {
		// 409 rather than 404: the session exists, it just is not warm, and the
		// client's retry is different (wake=1) from the one for a bad id.
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	jsonError(w, err.Error(), http.StatusInternalServerError)
}
