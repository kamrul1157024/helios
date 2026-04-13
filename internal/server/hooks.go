package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/reporter"
)

// handleHook is the single entry point for POST /hooks/{hookType...}.
// The URL path segments after /hooks/ are joined with dots to form the type key.
// e.g. /hooks/claude/permission → "claude.permission"
func (s *InternalServer) handleHook(w http.ResponseWriter, r *http.Request) {
	rawType := r.PathValue("hookType")
	hookType := strings.ReplaceAll(rawType, "/", ".")

	log.Printf("hook: received %s from %s", hookType, r.RemoteAddr)

	handler := provider.GetHookHandler(hookType)
	if handler == nil {
		log.Printf("hook: unknown type %s", hookType)
		http.Error(w, fmt.Sprintf("unknown hook type: %s", hookType), http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("hook: failed to read body for %s: %v", hookType, err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	log.Printf("hook: dispatching %s (%d bytes)", hookType, len(body))
	ctx := s.hookContext()
	handler(ctx, w, r, json.RawMessage(body))
}

// hookContext builds a provider.HookContext from the shared state.
func (s *InternalServer) hookContext() *provider.HookContext {
	return &provider.HookContext{
		DB:   s.shared.DB,
		Mgr:  s.shared.Mgr,
		Tmux: s.shared.Tmux,
		Notify: func(eventType string, data interface{}) {
			s.shared.SSE.Broadcast(SSEEvent{Type: eventType, Data: data})
		},
		Push: func(notifType, id, title, body, sessionID, paneID string) {
			if s.shared.DesktopNotifier != nil {
				go s.shared.DesktopNotifier.Send(id, notifType, title, body, sessionID, paneID)
			}
		},
		RemovePendingPane: func(cwd string) string {
			return s.shared.PendingPanes.RemoveByCWD(cwd)
		},
		Report: func(event provider.ReportEvent) {
			s.shared.Reporter.AddEvent(reporter.Event{
				Type:      event.Type,
				SessionID: event.SessionID,
				CWD:       event.CWD,
				ToolName:  event.ToolName,
				ToolInput: event.ToolInput,
				Message:   event.Message,
				Status:    event.Status,
				AgentType: event.AgentType,
				Detail:    event.Detail,
			})
		},
	}
}

