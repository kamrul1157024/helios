package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/terminal"
)

// shellRunner is the part of a backend that can run terminals beside a
// session's agent. Declared here rather than in the backend package because
// this is the only thing that needs it.
type shellRunner interface {
	StartShell(parent, cwd string) (terminal.Terminal, error)
	Terminals(parent string) []terminal.Terminal
	KillTerminal(id string) error
	KillShells(parent string)
}

func (sh *Shared) shells() (shellRunner, bool) {
	runner, ok := sh.Backend.(shellRunner)
	return runner, ok
}

// handleSessionTerminals opens a shell beside a session, or lists the
// terminals it already has.
//
// A shell is not a session: it runs no agent, keeps no transcript, and fires
// no hooks. It is a second process in the same directory, which is what the
// user reaches for when the agent is busy and they want to run the tests
// themselves.
//
// POST|GET /api/sessions/{id}/terminals
func (s *PublicServer) handleSessionTerminals(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/terminals")
	if id == "" {
		jsonError(w, "session id required", http.StatusBadRequest)
		return
	}

	runner, ok := s.shared.shells()
	if !ok {
		jsonError(w, fmt.Sprintf("backend %s cannot run shells", s.shared.Backend.Name()), http.StatusNotImplemented)
		return
	}

	if r.Method == http.MethodGet {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"terminals": runner.Terminals(id)})
		return
	}

	// The running agent's directory first: it is where the user is watching
	// work happen, and it stays right when a session record is missing or its
	// stored cwd is stale.
	dir := ""
	for _, t := range runner.Terminals(id) {
		if t.Kind == "agent" {
			dir = t.Cwd
			break
		}
	}
	if dir == "" {
		session, err := s.shared.DB.GetSession(id)
		if err != nil || session == nil {
			jsonError(w, "session not found", http.StatusNotFound)
			return
		}
		dir = session.CWD
	}

	cwd, err := resolveCWD(dir)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	term, err := runner.StartShell(id, cwd)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to open shell: %v", err), http.StatusInternalServerError)
		return
	}
	// A shell belongs to the session, not to whoever asked for it: one opened
	// on the phone is one the desktop should show too.
	s.shared.SSE.Broadcast(SSEEvent{
		Type: "terminal_opened",
		Data: map[string]interface{}{
			"session_id":  id,
			"terminal_id": term.ID,
			"kind":        term.Kind,
			"cwd":         term.Cwd,
		},
	})
	jsonResponse(w, http.StatusOK, term)
}

// handleTerminal serves one terminal by id: the websocket relay on GET, and
// the kill on DELETE.
//
// GET|DELETE /api/terminals/{id}
func (s *PublicServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/terminals/")
	if id == "" || strings.Contains(id, "/") {
		jsonError(w, "terminal id required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.streamTerminal(w, r, id)
	case http.MethodDelete:
		s.killTerminal(w, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *PublicServer) killTerminal(w http.ResponseWriter, id string) {
	// An agent's host is not the user's to close: it belongs to the session,
	// which has stop, terminate and delete of its own.
	if !terminal.IsShell(id) {
		jsonError(w, "only shells can be closed; stop or terminate the session instead", http.StatusBadRequest)
		return
	}

	runner, ok := s.shared.shells()
	if !ok {
		jsonError(w, fmt.Sprintf("backend %s cannot run shells", s.shared.Backend.Name()), http.StatusNotImplemented)
		return
	}
	if err := runner.KillTerminal(id); err != nil {
		jsonError(w, fmt.Sprintf("failed to close terminal: %v", err), http.StatusInternalServerError)
		return
	}
	parent, _ := terminal.SplitID(id)
	s.shared.SSE.Broadcast(SSEEvent{
		Type: "terminal_closed",
		Data: map[string]interface{}{
			"session_id":  parent,
			"terminal_id": id,
		},
	})
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}
