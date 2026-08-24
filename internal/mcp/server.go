// Package mcp serves Helios's Model Context Protocol tools over streamable
// HTTP. It exposes what only Helios owns — the app's own views and its session
// inventory — and deliberately not files, grep or git, which the agent's own
// harness already does better.
//
// A hook covers whatever the agent already emits; a tool covers only what no
// hook can infer. The transport is hand-rolled against net/http and
// encoding/json, because the subset an agent uses is four methods.
// See docs/specs/41-helios-mcp-tools.md.
package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// protocolVersion is what Claude Code negotiated when this was verified.
const protocolVersion = "2025-06-18"

// sessionHeader carries the caller's own session id. Helios injects it when it
// starts a session, so the agent never learns or passes its own id.
const sessionHeader = "X-Helios-Session"

// Notifier delivers a view change to attached clients and reports how many
// received it, without this package importing the HTTP server.
type Notifier interface {
	Show(payload map[string]interface{}) (clients int)
}

type Server struct {
	sessions Sessions
	notify   Notifier
	tools    map[string]tool
}

type tool struct {
	description string
	schema      map[string]interface{}
	// call receives the caller's session id, resolved from the header. It is
	// empty for a client Helios did not start.
	call func(sessionID string, args map[string]interface{}) (string, error)
}

func New(sessions Sessions, notify Notifier) *Server {
	s := &Server{sessions: sessions, notify: notify}
	s.tools = s.registry()
	return s
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// The optional server→client stream. Declining it is fine: every tool
		// here answers in the response to its own call.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// A notification carries no id and expects no body.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := response{JSONRPC: "2.0", ID: req.ID}
	resp.Result = s.dispatch(req, r.Header.Get(sessionHeader), &resp)
	if resp.Error != nil {
		resp.Result = nil
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("mcp: write response: %v", err)
	}
}

func (s *Server) dispatch(req request, sessionID string, resp *response) interface{} {
	switch req.Method {
	case "initialize":
		return map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "helios", "version": "1"},
		}

	case "ping":
		return map[string]interface{}{}

	case "tools/list":
		listed := make([]map[string]interface{}, 0, len(s.tools))
		for _, name := range toolOrder {
			t := s.tools[name]
			listed = append(listed, map[string]interface{}{
				"name":        name,
				"description": t.description,
				"inputSchema": t.schema,
			})
		}
		return map[string]interface{}{"tools": listed}

	case "tools/call":
		var p struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return nil
		}
		t, ok := s.tools[p.Name]
		if !ok {
			return toolError(fmt.Sprintf("unknown tool %q", p.Name))
		}
		out, err := t.call(sessionID, p.Arguments)
		if err != nil {
			// Tool failures are results, not protocol errors: the agent should
			// read them and correct itself rather than see a transport fault.
			return toolError(err.Error())
		}
		return map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": out}},
		}
	}

	resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	return nil
}

func toolError(msg string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": msg}},
		"isError": true,
	}
}
