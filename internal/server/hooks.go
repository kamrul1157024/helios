package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/push"
)

// handleHook is the single entry point for POST /hooks/{hookType...}.
// The URL path segments after /hooks/ are joined with dots to form the type key.
// e.g. /hooks/claude/permission → "claude.permission"
func (s *InternalServer) handleHook(w http.ResponseWriter, r *http.Request) {
	rawType := r.PathValue("hookType")
	hookType := strings.ReplaceAll(rawType, "/", ".")

	handler := provider.GetHookHandler(hookType)
	if handler == nil {
		http.Error(w, fmt.Sprintf("unknown hook type: %s", hookType), http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	ctx := s.hookContext()
	handler(ctx, w, r, json.RawMessage(body))
}

// hookContext builds a provider.HookContext from the shared state.
func (s *InternalServer) hookContext() *provider.HookContext {
	return &provider.HookContext{
		DB:  s.shared.DB,
		Mgr: s.shared.Mgr,
		Notify: func(eventType string, data interface{}) {
			s.shared.SSE.Broadcast(SSEEvent{Type: eventType, Data: data})
		},
		Push: func(notifType, id, title, body string) {
			go sendDesktopNotification(body)
			if s.shared.Pusher != nil {
				go s.shared.Pusher.SendToAll(push.PushPayload{
					Type:  notifType,
					ID:    id,
					Title: title,
					Body:  body,
				})
			}
		},
	}
}

func sendDesktopNotification(detail string) {
	if runtime.GOOS != "darwin" {
		return
	}
	script := fmt.Sprintf(`display notification "%s" with title "helios" subtitle "Claude needs permission"`, detail)
	exec.Command("osascript", "-e", script).Run()
}
