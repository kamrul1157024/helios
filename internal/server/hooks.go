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

// enrichNotification adds tmux_pane to a notification SSE event by looking
// up the session associated with the notification.
func enrichNotification(shared *Shared, data interface{}) interface{} {
	// Marshal→unmarshal to get a plain map we can augment.
	b, err := json.Marshal(data)
	if err != nil {
		return data
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return data
	}
	sessionID, _ := m["source_session"].(string)
	if sessionID == "" {
		return m
	}
	sess, err := shared.DB.GetSession(sessionID)
	if err != nil || sess == nil || sess.TmuxPane == nil {
		return m
	}
	m["tmux_pane"] = *sess.TmuxPane
	return m
}

// hookContext builds a provider.HookContext from the shared state.
func (s *InternalServer) hookContext() *provider.HookContext {
	return &provider.HookContext{
		DB:   s.shared.DB,
		Mgr:  s.shared.Mgr,
		Tmux: s.shared.Tmux,
		Notify: func(eventType string, data interface{}) {
			if eventType == "notification" {
				data = enrichNotification(s.shared, data)
			}
			s.shared.SSE.Broadcast(SSEEvent{Type: eventType, Data: data})
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

