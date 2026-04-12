package server

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/narration"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// ==================== Public Server API ====================

func (s *PublicServer) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	status := r.URL.Query().Get("status")
	nType := r.URL.Query().Get("type")

	notifs, err := s.shared.Mgr.ListNotifications(source, status, nType)
	if err != nil {
		jsonError(w, "failed to list notifications", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"notifications": notifs,
	})
}

// ==================== Unified Action Endpoint ====================

func (s *PublicServer) handleNotificationAction(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/action")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	notif, err := s.shared.Mgr.GetNotification(id)
	if err != nil || notif == nil {
		jsonError(w, "notification not found", http.StatusNotFound)
		return
	}
	if notif.Status != "pending" {
		jsonResponse(w, http.StatusGone, map[string]interface{}{
			"success": false, "error": "already_resolved",
		})
		return
	}

	handler := provider.GetActionHandler(notif.Type)
	if handler == nil {
		jsonError(w, fmt.Sprintf("no action handler for type: %s", notif.Type), http.StatusBadRequest)
		return
	}

	decision, err := handler(notif, json.RawMessage(body))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	if err := s.shared.Mgr.Resolve(id, decision, source); err != nil {
		if _, ok := err.(*store.AlreadyResolvedError); ok {
			jsonResponse(w, http.StatusGone, map[string]interface{}{
				"success": false, "error": "already_resolved",
			})
			return
		}
		jsonError(w, "failed to process action", http.StatusInternalServerError)
		return
	}

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": decision.Status, "source": source},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *PublicServer) handleDismissNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/dismiss")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	decision := notifications.Decision{Status: "dismissed"}
	if err := s.shared.Mgr.Resolve(id, decision, source); err != nil {
		jsonError(w, "failed to dismiss", http.StatusInternalServerError)
		return
	}

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": "dismissed", "source": source},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *PublicServer) handleBatchNotifications(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotificationIDs []string        `json:"notification_ids"`
		Action          json.RawMessage `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	var results []map[string]interface{}
	for _, id := range req.NotificationIDs {
		result := map[string]interface{}{"id": id}

		notif, err := s.shared.Mgr.GetNotification(id)
		if err != nil || notif == nil || notif.Status != "pending" {
			result["success"] = false
			result["error"] = "not_found_or_resolved"
			results = append(results, result)
			continue
		}

		handler := provider.GetActionHandler(notif.Type)
		if handler == nil {
			result["success"] = false
			result["error"] = "no_action_handler"
			results = append(results, result)
			continue
		}

		decision, err := handler(notif, req.Action)
		if err != nil {
			result["success"] = false
			result["error"] = err.Error()
			results = append(results, result)
			continue
		}

		if err := s.shared.Mgr.Resolve(id, decision, source); err != nil {
			result["success"] = false
			result["error"] = "already_resolved"
		} else {
			result["success"] = true
			s.shared.SSE.Broadcast(SSEEvent{
				Type: "notification_resolved",
				Data: map[string]string{"id": id, "action": decision.Status, "source": source},
			})
		}
		results = append(results, result)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
}

func (s *PublicServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"sse_clients": s.shared.SSE.ClientCount(),
		"pending":     s.shared.Mgr.PendingCount(),
		"tmux":        s.shared.Tmux.CheckStatus(),
	})
}

func (s *PublicServer) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.shared.DB.ListDevices()
	if err != nil {
		jsonError(w, "failed to list devices", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"devices": devices,
	})
}

func (s *PublicServer) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	kid := strings.TrimPrefix(r.URL.Path, "/api/auth/devices/")
	if kid == "" {
		jsonError(w, "missing device kid", http.StatusBadRequest)
		return
	}

	if err := s.shared.DB.RevokeDevice(kid); err != nil {
		jsonError(w, "failed to revoke device", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Device %s revoked", kid),
	})
}

// handlePair registers a device using a one-time pairing token.
// The device sends its self-generated kid (UUID), public key, and the pairing token from the QR.
func (s *PublicServer) handlePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token     string `json:"token"`
		KID       string `json:"kid"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" || req.KID == "" || req.PublicKey == "" {
		jsonError(w, "missing token, kid, or public_key", http.StatusBadRequest)
		return
	}

	// Validate public key format
	if _, err := auth.PublicKeyFromBase64(req.PublicKey); err != nil {
		jsonError(w, "invalid public key format", http.StatusBadRequest)
		return
	}

	// Claim the pairing token (atomic: validates + burns in one query)
	if err := s.shared.DB.ClaimPairingToken(req.Token, req.KID); err != nil {
		jsonResponse(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   "invalid_token",
			"message": "Pairing token is invalid, expired, or already used. Generate a new QR from the terminal.",
		})
		return
	}

	// Create the device with the client-generated public key
	if err := s.shared.DB.UpsertDevice(req.KID, req.PublicKey); err != nil {
		jsonError(w, "failed to register device", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"kid":     req.KID,
	})
}

// handleLogin sets the HttpOnly cookie after verifying the JWT.
func (s *PublicServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		jsonError(w, "missing token", http.StatusBadRequest)
		return
	}

	kid, err := auth.ValidateJWT(req.Token, func(kid string) (ed25519.PublicKey, error) {
		// Accept both pending and active devices for login
		device, err := s.shared.DB.GetDevice(kid)
		if err != nil {
			return nil, err
		}
		if device == nil {
			return nil, fmt.Errorf("device not found")
		}
		if device.Status == "revoked" {
			return nil, fmt.Errorf("device revoked")
		}
		return auth.PublicKeyFromBase64(device.PublicKey)
	})
	if err != nil {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Device stays pending until CLI user approves via /internal/device/activate
	s.shared.DB.UpdateDeviceLastSeen(kid)

	// Set HttpOnly cookie
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    req.Token,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"kid":     kid,
	})
}

// handleDeviceMe returns the current device's info.
func (s *PublicServer) handleDeviceMe(w http.ResponseWriter, r *http.Request) {
	kid, ok := r.Context().Value(deviceKIDKey).(string)
	if !ok {
		jsonError(w, "missing device context", http.StatusUnauthorized)
		return
	}

	device, err := s.shared.DB.GetDevice(kid)
	if err != nil || device == nil {
		jsonError(w, "device not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, http.StatusOK, device)
}

// handleUpdateDeviceMe lets a device update its own metadata.
func (s *PublicServer) handleUpdateDeviceMe(w http.ResponseWriter, r *http.Request) {
	kid, ok := r.Context().Value(deviceKIDKey).(string)
	if !ok {
		jsonError(w, "missing device context", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
		Browser  string `json:"browser"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.shared.DB.UpdateDeviceMetadata(kid, req.Name, req.Platform, req.Browser); err != nil {
		jsonError(w, "failed to update device", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// ==================== Session API ====================

// enrichSession sets computed fields (e.g. SupportsPromptQueue) using provider capabilities.
func enrichSession(sess *store.Session) {
	caps := provider.GetCapabilities(sess.Source)
	sess.ComputePromptQueue(caps.PromptQueue)
}

func (s *PublicServer) handleListSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	status := r.URL.Query().Get("status")
	filter := r.URL.Query().Get("filter")
	cwd := r.URL.Query().Get("cwd")

	sessions, err := s.shared.DB.SearchSessions(query, status, filter, cwd)
	if err != nil {
		jsonError(w, "failed to list sessions", http.StatusInternalServerError)
		return
	}
	for i := range sessions {
		enrichSession(&sessions[i])
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
	})
}

func (s *PublicServer) handleListDirectories(w http.ResponseWriter, r *http.Request) {
	dirs, err := s.shared.DB.ListDirectories()
	if err != nil {
		jsonError(w, "failed to list directories", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"directories": dirs,
	})
}

func (s *PublicServer) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	enrichSession(session)

	// Get pending permission count for this session
	pendingNotifs, _ := s.shared.DB.ListNotifications("claude", "pending", "")
	pendingCount := 0
	for _, n := range pendingNotifs {
		if n.SourceSession == id {
			pendingCount++
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"session":             session,
		"pending_permissions": pendingCount,
	})
}

func (s *PublicServer) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	// Path: /api/sessions/<id>/transcript
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	id := strings.TrimSuffix(path, "/transcript")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if session.TranscriptPath == nil || *session.TranscriptPath == "" {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"messages": []interface{}{},
			"total":    0,
			"returned": 0,
			"offset":   0,
			"has_more": false,
		})
		return
	}

	// Parse limit/offset from query
	limit := 200
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}

	result, err := transcript.ParseClaudeTranscript(*session.TranscriptPath, limit, offset)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to read transcript: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, result)
}

func (s *PublicServer) handleListSubagents(w http.ResponseWriter, r *http.Request) {
	// Path: /api/sessions/<id>/subagents
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	id := strings.TrimSuffix(path, "/subagents")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	subagents, err := s.shared.DB.ListSubagents(id)
	if err != nil {
		jsonError(w, "failed to list subagents", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"subagents": subagents,
	})
}

// ==================== Session Control ====================

func (s *PublicServer) handleSessionSend(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/send")

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		log.Printf("session-send: bad request for %s: %v", id, err)
		jsonError(w, "missing message", http.StatusBadRequest)
		return
	}

	log.Printf("session-send: session=%s message=%q", id, truncate(req.Message, 80))

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		log.Printf("session-send: session %s not found", id)
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	log.Printf("session-send: session=%s status=%s tmux_pane=%v", id, session.Status, session.TmuxPane)

	if session.Status == "active" || session.Status == "waiting_permission" {
		// Provider supports prompt queue via tmux send-keys
		caps := provider.GetCapabilities(session.Source)
		if caps.PromptQueue && session.TmuxPane != nil && *session.TmuxPane != "" {
			if err := s.shared.Tmux.SendKeys(*session.TmuxPane, req.Message); err != nil {
				log.Printf("session-send: queue SendKeys failed for pane %s: %v", *session.TmuxPane, err)
				jsonError(w, fmt.Sprintf("failed to queue: %v", err), http.StatusInternalServerError)
				return
			}
			s.shared.DB.UpdateSessionLastUserMessage(id, req.Message)
			log.Printf("session-send: queued prompt to pane %s for session %s", *session.TmuxPane, id)
			jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "queued": true})
			return
		}
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_busy",
		})
		return
	}

	if session.Status == "ended" || session.Status == "suspended" {
		// Resume in a new tmux pane with the user's prompt
		cmd := fmt.Sprintf("claude --resume %s -p %q", session.SessionID, req.Message)
		paneID, err := s.shared.Tmux.CreateWindow(session.CWD, cmd)
		if err != nil {
			log.Printf("session-send: failed to resume session %s: %v", id, err)
			jsonError(w, fmt.Sprintf("failed to resume: %v", err), http.StatusInternalServerError)
			return
		}
		s.shared.DB.UpdateSessionTmuxPane(id, paneID, 0)
		s.shared.DB.UpdateSessionStatus(id, "active", "RemotePrompt")
		s.shared.DB.UpdateSessionLastUserMessage(id, req.Message)
		log.Printf("session-send: resumed session %s in pane %s", id, paneID)
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"success": true, "resumed": true, "tmux_pane": paneID,
		})
		return
	}

	// Session is idle — send keys to existing tmux pane
	if session.TmuxPane == nil || *session.TmuxPane == "" {
		log.Printf("session-send: session %s is idle but has no tmux pane, resuming", id)
		// No tmux pane — resume the session in a new pane with the prompt
		cmd := fmt.Sprintf("claude --resume %s -p %q", session.SessionID, req.Message)
		paneID, err := s.shared.Tmux.CreateWindow(session.CWD, cmd)
		if err != nil {
			jsonError(w, fmt.Sprintf("failed to resume: %v", err), http.StatusInternalServerError)
			return
		}
		s.shared.DB.UpdateSessionTmuxPane(id, paneID, 0)
		s.shared.DB.UpdateSessionStatus(id, "active", "RemotePrompt")
		s.shared.DB.UpdateSessionLastUserMessage(id, req.Message)
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"success": true, "resumed": true, "tmux_pane": paneID,
		})
		return
	}

	if err := s.shared.Tmux.SendKeys(*session.TmuxPane, req.Message); err != nil {
		log.Printf("session-send: SendKeys failed for pane %s: %v", *session.TmuxPane, err)
		jsonError(w, fmt.Sprintf("failed to send: %v", err), http.StatusInternalServerError)
		return
	}

	s.shared.DB.UpdateSessionStatus(id, "active", "RemotePrompt")
	s.shared.DB.UpdateSessionLastUserMessage(id, req.Message)
	log.Printf("session-send: sent keys to pane %s for session %s", *session.TmuxPane, id)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *PublicServer) handleSessionStop(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/stop")

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if session.Status != "active" && session.Status != "waiting_permission" {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_not_active",
		})
		return
	}

	if session.TmuxPane == nil || *session.TmuxPane == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "no_tmux_pane",
		})
		return
	}

	if err := s.shared.Tmux.SendEscape(*session.TmuxPane); err != nil {
		jsonError(w, fmt.Sprintf("failed to stop: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *PublicServer) handleSessionSuspend(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/suspend")

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if session.Status == "ended" {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_ended",
		})
		return
	}

	if session.TmuxPane == nil || *session.TmuxPane == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "no_tmux_pane",
		})
		return
	}

	if err := s.shared.Tmux.Suspend(*session.TmuxPane); err != nil {
		jsonError(w, fmt.Sprintf("failed to suspend: %v", err), http.StatusInternalServerError)
		return
	}

	s.shared.DB.UpdateSessionStatus(id, "suspended", "RemoteSuspend")
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true, "status": "suspended",
	})
}

func (s *PublicServer) handleSessionResume(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/resume")

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if session.Status == "active" {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_active",
		})
		return
	}

	cmd := fmt.Sprintf("claude --resume %s", session.SessionID)
	paneID, err := s.shared.Tmux.CreateWindow(session.CWD, cmd)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to resume: %v", err), http.StatusInternalServerError)
		return
	}

	s.shared.DB.UpdateSessionTmuxPane(id, paneID, 0)
	s.shared.DB.UpdateSessionStatus(id, "idle", "RemoteResume")

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_status",
		Data: map[string]interface{}{
			"session_id": session.SessionID,
			"status":     "idle",
			"tmux_pane":  paneID,
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true, "status": "idle", "tmux_pane": paneID,
	})
}

func (s *PublicServer) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	var req struct {
		Pinned   *bool   `json:"pinned"`
		Archived *bool   `json:"archived"`
		Title    *string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	pinned := session.Pinned
	archived := session.Archived
	if req.Pinned != nil {
		pinned = *req.Pinned
	}
	if req.Archived != nil {
		archived = *req.Archived
	}

	if err := s.shared.DB.UpdateSessionFlags(id, pinned, archived); err != nil {
		jsonError(w, "failed to update session", http.StatusInternalServerError)
		return
	}

	if req.Title != nil {
		if err := s.shared.DB.UpdateSessionTitle(id, *req.Title); err != nil {
			jsonError(w, "failed to update session title", http.StatusInternalServerError)
			return
		}
	}

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_updated",
		Data: map[string]interface{}{
			"session_id": id,
			"pinned":     pinned,
			"archived":   archived,
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"pinned":   pinned,
		"archived": archived,
	})
}

func (s *PublicServer) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if err := s.shared.DB.DeleteSession(id); err != nil {
		jsonError(w, "failed to delete session", http.StatusInternalServerError)
		return
	}

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_deleted",
		Data: map[string]interface{}{
			"session_id": id,
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func extractSessionID(path, suffix string) string {
	path = strings.TrimPrefix(path, "/api/sessions/")
	path = strings.TrimSuffix(path, suffix)
	return path
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ==================== Internal Server API ====================

func (s *InternalServer) handleInternalListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.shared.DB.ListSessions()
	if err != nil {
		jsonError(w, "failed to list sessions", http.StatusInternalServerError)
		return
	}
	for i := range sessions {
		enrichSession(&sessions[i])
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
	})
}

func (s *InternalServer) handleInternalCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider                  string `json:"provider"`
		Prompt                    string `json:"prompt"`
		Model                    string `json:"model,omitempty"`
		CWD                      string `json:"cwd"`
		DangerouslySkipPermissions bool  `json:"dangerously_skip_permissions,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
		jsonError(w, "missing prompt", http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		req.Provider = "claude"
	}

	builder := provider.GetSessionBuilder(req.Provider)
	if builder == nil {
		jsonError(w, fmt.Sprintf("unknown provider: %s", req.Provider), http.StatusNotFound)
		return
	}

	if req.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			jsonError(w, "failed to get cwd", http.StatusInternalServerError)
			return
		}
		req.CWD = cwd
	}

	cmd := builder(req.Prompt, req.Model, req.CWD)
	if req.DangerouslySkipPermissions {
		cmd = strings.Replace(cmd, "claude", "claude --dangerously-skip-permissions", 1)
	}

	paneID, err := s.shared.Tmux.CreateWindow(req.CWD, cmd)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to create tmux window: %v", err), http.StatusInternalServerError)
		return
	}

	s.shared.PendingPanes.Add(paneID, req.CWD)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"tmux_pane": paneID,
		"cwd":       req.CWD,
	})
}

func (s *InternalServer) handleWrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaneID string `json:"pane_id"`
		CWD    string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PaneID == "" {
		jsonError(w, "missing pane_id", http.StatusBadRequest)
		return
	}
	s.shared.PendingPanes.Add(req.PaneID, req.CWD)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *InternalServer) handleInternalHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"internal_port": extractPort(s.httpServer.Addr),
		"sse_clients":   s.shared.SSE.ClientCount(),
		"pending":       s.shared.Mgr.PendingCount(),
		"tmux":          s.shared.Tmux.CheckStatus(),
	})
}

// TunnelManager is set by the daemon after creating the tunnel manager.
var TunnelManager interface {
	Status() map[string]interface{}
	Start(provider string, customURL string, localPort int) (string, error)
	Stop() error
}

// OnTunnelConfigChanged is called when tunnel config should be persisted.
// Set by daemon to save tunnel provider to config.yaml.
var OnTunnelConfigChanged func(provider, customURL string)

func (s *InternalServer) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	if TunnelManager == nil {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"active":   false,
			"provider": "",
		})
		return
	}
	jsonResponse(w, http.StatusOK, TunnelManager.Status())
}

func (s *InternalServer) handleTunnelStart(w http.ResponseWriter, r *http.Request) {
	if TunnelManager == nil {
		jsonError(w, "tunnel manager not initialized", http.StatusInternalServerError)
		return
	}

	var req struct {
		Provider  string `json:"provider"`
		CustomURL string `json:"custom_url,omitempty"`
		LocalPort int    `json:"local_port,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	url, err := TunnelManager.Start(req.Provider, req.CustomURL, req.LocalPort)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to start tunnel: %v", err), http.StatusInternalServerError)
		return
	}

	// Persist tunnel config so it auto-starts on next daemon restart
	if OnTunnelConfigChanged != nil {
		OnTunnelConfigChanged(req.Provider, req.CustomURL)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"public_url": url,
	})
}

func (s *InternalServer) handleTunnelStop(w http.ResponseWriter, r *http.Request) {
	if TunnelManager == nil {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"stopped": true})
		return
	}

	if err := TunnelManager.Stop(); err != nil {
		jsonError(w, fmt.Sprintf("failed to stop tunnel: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{"stopped": true})
}

func (s *InternalServer) handleDeviceCreate(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GeneratePairingToken()
	if err != nil {
		jsonError(w, "failed to generate pairing token", http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(2 * time.Minute)
	if err := s.shared.DB.CreatePairingToken(token, expiresAt); err != nil {
		jsonError(w, "failed to store pairing token", http.StatusInternalServerError)
		return
	}

	setupURL := ""
	if TunnelManager != nil {
		status := TunnelManager.Status()
		if url, ok := status["public_url"].(string); ok && url != "" {
			setupURL = fmt.Sprintf("helios://pair?url=%s&token=%s", url, token)
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"token":      token,
		"expires_in": 120,
		"setup_url":  setupURL,
	})
}

func (s *InternalServer) handleDeviceRekey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KID string `json:"kid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.KID == "" {
		jsonError(w, "missing kid", http.StatusBadRequest)
		return
	}

	// Check device exists
	device, err := s.shared.DB.GetDevice(req.KID)
	if err != nil || device == nil {
		jsonError(w, "device not found", http.StatusNotFound)
		return
	}

	// Reset device to pending (device will generate new keys and re-pair)
	if err := s.shared.DB.RekeyDevice(req.KID, ""); err != nil {
		jsonError(w, "failed to rekey device", http.StatusInternalServerError)
		return
	}

	// Generate pairing token for re-pairing
	token, err := auth.GeneratePairingToken()
	if err != nil {
		jsonError(w, "failed to generate pairing token", http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(2 * time.Minute)
	if err := s.shared.DB.CreatePairingToken(token, expiresAt); err != nil {
		jsonError(w, "failed to store pairing token", http.StatusInternalServerError)
		return
	}

	setupURL := ""
	if TunnelManager != nil {
		status := TunnelManager.Status()
		if url, ok := status["public_url"].(string); ok && url != "" {
			setupURL = fmt.Sprintf("helios://pair?url=%s&token=%s", url, token)
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"kid":       req.KID,
		"token":     token,
		"setup_url": setupURL,
		"rekeyed":   true,
	})
}

func (s *InternalServer) handleDeviceList(w http.ResponseWriter, r *http.Request) {
	devices, err := s.shared.DB.ListDevices()
	if err != nil {
		jsonError(w, "failed to list devices", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"devices": devices,
	})
}

func (s *InternalServer) handleDeviceActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KID string `json:"kid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.KID == "" {
		jsonError(w, "missing kid", http.StatusBadRequest)
		return
	}

	if err := s.shared.DB.ActivateDevice(req.KID); err != nil {
		jsonError(w, "failed to activate device", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"activated": true,
	})
}

func (s *InternalServer) handleDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KID string `json:"kid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.KID == "" {
		jsonError(w, "missing kid", http.StatusBadRequest)
		return
	}

	if err := s.shared.DB.RevokeDevice(req.KID); err != nil {
		jsonError(w, "failed to revoke device", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"revoked": true,
	})
}

// APKDownloadURL is the GitHub release URL for the APK (always points to latest release).
var APKDownloadURL = "https://github.com/kamrul1157024/helios/releases/latest/download/helios.apk"

// DMGDownloadURL is the GitHub release URL for the macOS DMG (always points to latest release).
var DMGDownloadURL = "https://github.com/kamrul1157024/helios/releases/latest/download/helios.dmg"

// ==================== Commands ====================

func (s *PublicServer) handleListCommands(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"commands": provider.GetCommands(),
	})
}

// ==================== Small Model Text ====================

func (s *PublicServer) handleSmallModelText(w http.ResponseWriter, r *http.Request) {
	var req narration.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Events) == 0 {
		jsonResponse(w, http.StatusOK, narration.Response{})
		return
	}

	// Log request for debugging
	for i, e := range req.Events {
		log.Printf("[small-model-text] event[%d]: type=%s tool=%s target=%s summary=%s status=%s", i, e.Type, e.Tool, e.Target, e.Summary, e.Status)
	}
	if req.SessionContext != "" {
		log.Printf("[small-model-text] session_context=%s", req.SessionContext)
	}

	resp := narration.Generate(r.Context(), req, "claude")

	log.Printf("[small-model-text] narration=%q", resp.Narration)
	jsonResponse(w, http.StatusOK, resp)
}

// ==================== Providers & Models ====================

// modelCache holds cached model lists per provider with a TTL.
var modelCache = struct {
	data      map[string][]provider.ModelInfo
	fetchedAt map[string]time.Time
}{
	data:      make(map[string][]provider.ModelInfo),
	fetchedAt: make(map[string]time.Time),
}

const modelCacheTTL = 24 * time.Hour

func getCachedModels(providerID string) ([]provider.ModelInfo, bool) {
	models, ok := modelCache.data[providerID]
	if !ok {
		return nil, false
	}
	if time.Since(modelCache.fetchedAt[providerID]) > modelCacheTTL {
		return nil, false
	}
	return models, true
}

func fetchAndCacheModels(providerID string) ([]provider.ModelInfo, error) {
	fetcher := provider.GetModelsFetcher(providerID)
	if fetcher == nil {
		return nil, fmt.Errorf("unknown provider: %s", providerID)
	}
	models, err := fetcher()
	if err != nil {
		return nil, err
	}
	modelCache.data[providerID] = models
	modelCache.fetchedAt[providerID] = time.Now()
	return models, nil
}

func (s *PublicServer) handleListProviders(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"providers": provider.GetProviders(),
	})
}

func (s *PublicServer) handleListModels(w http.ResponseWriter, r *http.Request) {
	providerID := extractPathParam(r.URL.Path, "/api/providers/", "/models")
	if providerID == "" {
		jsonError(w, "missing provider id", http.StatusBadRequest)
		return
	}

	models, ok := getCachedModels(providerID)
	if !ok {
		var err error
		models, err = fetchAndCacheModels(providerID)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"provider":          providerID,
		"models":            models,
		"cached_at":         modelCache.fetchedAt[providerID].UTC().Format(time.RFC3339),
		"cache_ttl_seconds": int(modelCacheTTL.Seconds()),
	})
}

func (s *PublicServer) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	providerID := extractPathParam(r.URL.Path, "/api/providers/", "/models/refresh")
	if providerID == "" {
		jsonError(w, "missing provider id", http.StatusBadRequest)
		return
	}

	models, err := fetchAndCacheModels(providerID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"provider":          providerID,
		"models":            models,
		"cached_at":         modelCache.fetchedAt[providerID].UTC().Format(time.RFC3339),
		"cache_ttl_seconds": int(modelCacheTTL.Seconds()),
	})
}

func (s *PublicServer) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider                  string `json:"provider"`
		Prompt                    string `json:"prompt"`
		Model                    string `json:"model,omitempty"`
		CWD                      string `json:"cwd,omitempty"`
		DangerouslySkipPermissions bool  `json:"dangerously_skip_permissions,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		jsonError(w, "missing prompt", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "claude"
	}

	builder := provider.GetSessionBuilder(req.Provider)
	if builder == nil {
		jsonError(w, fmt.Sprintf("unknown provider: %s", req.Provider), http.StatusNotFound)
		return
	}

	if req.CWD == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, "failed to determine home directory", http.StatusInternalServerError)
			return
		}
		req.CWD = home
	}

	cmd := builder(req.Prompt, req.Model, req.CWD)
	if req.DangerouslySkipPermissions {
		cmd = strings.Replace(cmd, "claude", "claude --dangerously-skip-permissions", 1)
	}

	paneID, err := s.shared.Tmux.CreateWindow(req.CWD, cmd)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to create tmux window: %v", err), http.StatusInternalServerError)
		return
	}

	// Track pane in memory — the pane watcher will detect trust prompts
	// and SessionStart hook will create the real session record.
	s.shared.PendingPanes.Add(paneID, req.CWD)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"tmux_pane": paneID,
		"cwd":       req.CWD,
	})
}

// ==================== Helpers ====================

func extractPathParam(path, prefix, suffix string) string {
	path = strings.TrimPrefix(path, prefix)
	path = strings.TrimSuffix(path, suffix)
	return path
}

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, message string, status int) {
	jsonResponse(w, status, map[string]interface{}{
		"error":   http.StatusText(status),
		"message": message,
	})
}

func extractPort(addr string) string {
	parts := strings.Split(addr, ":")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return addr
}

