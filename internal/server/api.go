package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	claudeprovider "github.com/kamrul1157024/helios/internal/provider/claude"
	"github.com/kamrul1157024/helios/internal/reporter"
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

	// Mgr.Resolve broadcasts notification_resolved itself.
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
		"terminal":    s.shared.Backend.Status(),
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
		s.shared.injectTerminal(&sessions[i])
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
	s.shared.injectTerminal(session)
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

// Variables rather than constants only so tests need not wait them out.
var (
	// agentBootTimeout is how long a woken session gets to report in before
	// the send is given up on. Generous: a cold agent loads a transcript,
	// an MCP server or two, and the user's settings before it says anything.
	agentBootTimeout = 25 * time.Second
	// promptAckTimeout is how long the agent gets to acknowledge a prompt it
	// was handed. Short: the hook fires the moment the prompt is submitted,
	// so silence past a few seconds means it was not.
	promptAckTimeout = 8 * time.Second
)

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

	live := s.shared.Backend.Alive(id)
	log.Printf("session-send: session=%s status=%s live=%v", id, session.Status, live)

	if session.Status == "active" || session.Status == "waiting_permission" {
		caps := provider.GetCapabilities(session.Source)
		if caps.PromptQueue && live {
			if err := s.shared.Backend.SendText(id, req.Message); err != nil {
				log.Printf("session-send: queue failed for %s: %v", id, err)
				jsonError(w, fmt.Sprintf("failed to queue: %v", err), http.StatusInternalServerError)
				return
			}
			s.shared.DB.UpdateSessionLastUserMessage(id, req.Message)
			log.Printf("session-send: queued prompt for session %s", id)
			jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "queued": true})
			return
		}
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_busy",
		})
		return
	}

	if session.Status == "terminated" {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_terminated",
		})
		return
	}

	// Idle with no terminal: wake the agent and type into it. Deliberately not
	// `claude --resume -p`, which costs a fresh process per message and leaves
	// nothing for the user to attach to afterwards.
	resumed := false
	if !live {
		waker, ok := s.shared.Backend.(backend.Waker)
		if !ok {
			jsonError(w, "session has no terminal and this backend cannot resume", http.StatusConflict)
			return
		}

		// Subscribed before the wake, never after: the agent can report in
		// while Wake is still returning, and a signal fired with nobody
		// listening is gone.
		ready := s.shared.Signals.Await(SignalAgentReady, id)
		defer ready.Release()

		woken, err := waker.Wake(id, session.CWD)
		if err != nil {
			log.Printf("session-send: wake %s: %v", id, err)
			jsonError(w, fmt.Sprintf("failed to resume: %v", err), http.StatusInternalServerError)
			return
		}
		resumed = woken

		// The wake only waits for the host's socket, which exists seconds
		// before the agent is reading its terminal. Typing into that gap is
		// how a prompt disappears with no trace.
		if resumed && !ready.Wait(agentBootTimeout) {
			log.Printf("session-send: session %s did not report ready within %s", id, agentBootTimeout)
			jsonError(w, "the session is still starting up", http.StatusGatewayTimeout)
			return
		}
	}

	// Likewise subscribed before typing. The agent's own hook is the only
	// proof the prompt landed; anything else is this end guessing.
	ack := s.shared.Signals.Await(SignalPromptSubmitted, id)
	defer ack.Release()

	if err := s.shared.Backend.SendText(id, req.Message); err != nil {
		log.Printf("session-send: send failed for %s: %v", id, err)
		jsonError(w, fmt.Sprintf("failed to send: %v", err), http.StatusInternalServerError)
		return
	}

	if !ack.Wait(promptAckTimeout) {
		// No retype: the prompt may yet be sitting in a dialog or a composer,
		// and a second copy would be a second turn. The session stays idle,
		// which is what it looks like from here, and the caller can retry.
		log.Printf("session-send: session %s never acknowledged the prompt", id)
		jsonError(w, "the session did not accept the message", http.StatusGatewayTimeout)
		return
	}

	// Status is the prompt-submit hook's to write, and it has by now. Writing
	// it here as well is how a lost prompt used to look like a working one.
	s.shared.DB.UpdateSessionLastUserMessage(id, req.Message)
	log.Printf("session-send: session %s accepted the prompt (resumed=%v)", id, resumed)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "resumed": resumed})
}

// ── Shared session control (used by both InternalServer and PublicServer) ──

func (sh *Shared) stopSession(w http.ResponseWriter, id string) {
	session, err := sh.DB.GetSession(id)
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

	if !sh.Backend.Alive(id) {
		// No terminal — the session is already not running, so the active status
		// it still carries is stale. Settle it rather than reporting success and
		// leaving every client showing a busy session.
		sh.settleInterrupted(id)
		jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "status": "idle"})
		return
	}

	if err := sh.Backend.Interrupt(id); err != nil {
		jsonError(w, fmt.Sprintf("failed to stop: %v", err), http.StatusInternalServerError)
		return
	}

	sh.settleInterrupted(id)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "status": "idle"})
}

// setPermissionMode changes a session's permission mode by restarting its
// agent under the new one.
//
// Claude has no way to set a mode on a running session — the flag is
// spawn-only and the only live control is a blind Shift+Tab cycle — so the
// mode is stored and applied on the next launch. A warm session is restarted
// here to make the change take effect now; a cold one just picks it up when it
// next wakes.
//
// The restart costs the host's scrollback ring, which is why it is refused
// while the agent is mid-turn: interrupting work to change a setting is never
// what the user meant, and a pending permission prompt would be stranded.
func (sh *Shared) setPermissionMode(w http.ResponseWriter, id, mode string) {
	if !claudeprovider.ValidPermissionMode(mode) {
		jsonError(w, fmt.Sprintf("unknown permission mode: %q", mode), http.StatusBadRequest)
		return
	}

	session, err := sh.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	if session.Source != "claude" {
		jsonError(w, fmt.Sprintf("provider %s has no permission modes", session.Source), http.StatusBadRequest)
		return
	}
	if session.Status == "terminated" || session.Status == "ended" {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_ended",
		})
		return
	}
	if session.Status != "idle" {
		// Busy covers active, waiting_permission, compacting and starting. The
		// client is expected to disable the control rather than rely on this,
		// but the check has to live here: status can change between render and
		// tap.
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_busy", "status": session.Status,
		})
		return
	}

	if err := sh.DB.UpdateSessionPermissionMode(id, mode); err != nil {
		jsonError(w, "failed to store permission mode", http.StatusInternalServerError)
		return
	}

	// Cold sessions need no restart: the stored mode is read when they wake.
	restarted, ready := false, true
	if sh.Backend.Alive(id) {
		var err error
		if ready, err = sh.restartForPermissionMode(id, session.CWD); err != nil {
			jsonError(w, fmt.Sprintf("failed to restart session: %v", err), http.StatusInternalServerError)
			return
		}
		restarted = true
	}

	sh.SSE.Broadcast(SSEEvent{
		Type: "session_updated",
		Data: map[string]interface{}{
			"session_id":      id,
			"permission_mode": mode,
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true, "permission_mode": mode, "restarted": restarted, "ready": ready,
	})
}

// restartForPermissionMode cycles a session's terminal so the agent relaunches
// under the mode just stored, and reports whether it came back up.
//
// Kill before Wake, not Wake alone: Wake adopts a live host, so without the
// kill the old process would keep running in the old mode and report success.
//
// Waiting for the agent is not politeness. Wake returns once the host's socket
// is listening, seconds before the agent inside it is reading its terminal, and
// throughout that gap the session is idle with a live terminal — which is
// exactly what the send path takes as "type into it now". A client that changes
// the mode and immediately sends a prompt therefore loses it: the keystrokes go
// into a booting CLI, no hook ever acknowledges them, and the send fails.
// Returning only once the agent has reported in closes the gap.
//
// Subscribed before the kill, because the agent can report in while Wake is
// still returning and a signal fired with nobody listening is gone.
func (sh *Shared) restartForPermissionMode(id, cwd string) (bool, error) {
	waker, ok := sh.Backend.(backend.Waker)
	if !ok {
		return false, fmt.Errorf("backend %s cannot resume sessions", sh.Backend.Name())
	}

	ready := sh.Signals.Await(SignalAgentReady, id)
	defer ready.Release()

	if err := sh.Backend.Kill(id); err != nil {
		return false, fmt.Errorf("kill terminal: %w", err)
	}
	if _, err := waker.Wake(id, cwd); err != nil {
		return false, fmt.Errorf("restart terminal: %w", err)
	}

	// A slow boot is not a failed switch: the mode is stored and the terminal is
	// up, so the change stands and the caller is told it is not usable yet
	// rather than being handed an error for work that did happen.
	if !ready.Wait(agentBootTimeout) {
		log.Printf("permission-mode: session %s did not report ready within %s", id, agentBootTimeout)
		return false, nil
	}
	return true, nil
}

// settleInterrupted moves a stopped session back to idle and tells every client.
//
// An interrupted turn produces no Stop hook — the agent just returns to its
// prompt — so nothing else would ever clear the active status, and the session
// would sit there looking busy for the rest of its life.
func (sh *Shared) settleInterrupted(id string) {
	if err := sh.DB.UpdateSessionStatus(id, "idle", "Interrupt"); err != nil {
		log.Printf("stop: update status for %s: %v", id, err)
		return
	}
	sh.SSE.Broadcast(SSEEvent{
		Type: "session_status",
		Data: map[string]interface{}{
			"session_id": id,
			"status":     "idle",
		},
	})
}

func (sh *Shared) terminateSession(w http.ResponseWriter, id string) {
	session, err := sh.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if session.Status == "terminated" {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session_terminated",
		})
		return
	}

	if err := sh.Backend.Kill(id); err != nil {
		log.Printf("terminate: kill terminal for %s: %v", id, err)
	}
	sh.DB.UpdateSessionStatus(id, "terminated", "Terminate")
	sh.SSE.Broadcast(SSEEvent{
		Type: "session_status",
		Data: map[string]interface{}{
			"session_id": id,
			"status":     "terminated",
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true, "status": "terminated",
	})
}

func (sh *Shared) resumeSession(w http.ResponseWriter, id string) {
	session, err := sh.DB.GetSession(id)
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

	handle, err := sh.startTerminal(session.SessionID, session.CWD, nil)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to resume: %v", err), http.StatusInternalServerError)
		return
	}

	sh.DB.UpdateSessionStatus(id, "idle", "Resume")
	sh.SSE.Broadcast(SSEEvent{
		Type: "session_status",
		Data: map[string]interface{}{
			"session_id": session.SessionID,
			"status":     "idle",
			"terminal":   handle,
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true, "status": "idle", "terminal": handle,
	})
}

// startTerminal brings a session's terminal up. Empty argv resumes the
// session's agent; otherwise argv launches it.
func (sh *Shared) startTerminal(sessionID, cwd string, argv []string) (string, error) {
	if len(argv) == 0 {
		waker, ok := sh.Backend.(backend.Waker)
		if !ok {
			return "", fmt.Errorf("backend %s cannot resume sessions", sh.Backend.Name())
		}
		if _, err := waker.Wake(sessionID, cwd); err != nil {
			return "", err
		}
		handle, _ := sh.Backend.Handle(sessionID)
		return handle, nil
	}
	return sh.Backend.Start(sessionID, cwd, argv)
}

// ── Public server session control handlers (delegate to Shared) ──

func (s *PublicServer) handleSessionStop(w http.ResponseWriter, r *http.Request) {
	s.shared.stopSession(w, extractSessionID(r.URL.Path, "/stop"))
}

func (s *PublicServer) handleSessionTerminate(w http.ResponseWriter, r *http.Request) {
	s.shared.terminateSession(w, extractSessionID(r.URL.Path, "/terminate"))
}

func (s *PublicServer) handleSessionPermissionMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.shared.setPermissionMode(w, extractSessionID(r.URL.Path, "/permission-mode"), req.Mode)
}

func (s *PublicServer) handleSessionResume(w http.ResponseWriter, r *http.Request) {
	s.shared.resumeSession(w, extractSessionID(r.URL.Path, "/resume"))
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
		Status   *string `json:"status"`
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

	if req.Status != nil {
		s.shared.DB.UpdateSessionStatus(id, *req.Status, "PatchUpdate")
		s.shared.SSE.Broadcast(SSEEvent{
			Type: "session_status",
			Data: map[string]interface{}{
				"session_id": id,
				"status":     *req.Status,
			},
		})
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

func (s *PublicServer) handleGenerateSessionTitle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	id := strings.TrimSuffix(path, "/title/generate")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	if err := s.shared.DB.ResetAutoTitleAttempts(id); err != nil {
		jsonError(w, "failed to reset title attempts", http.StatusInternalServerError)
		return
	}

	transcriptPath := ""
	if session.TranscriptPath != nil {
		transcriptPath = *session.TranscriptPath
	}

	notify := func(eventType string, data interface{}) {
		s.shared.SSE.Broadcast(SSEEvent{Type: eventType, Data: data})
	}
	ctx := &provider.HookContext{
		DB:     s.shared.DB,
		Notify: notify,
	}
	claudeprovider.TriggerAutoTitle(ctx, id, session.CWD, transcriptPath, notify)

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
		s.shared.injectTerminal(&sessions[i])
		enrichSession(&sessions[i])
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
	})
}

// launchPermissionMode reports the mode a freshly built session is starting
// under, or "" for a provider that has no permission modes. Claude is the only
// one with them today, and only its session builder knows what an empty request
// resolves to.
func launchPermissionMode(providerID string, spec provider.SessionSpec) string {
	if providerID != "claude" {
		return ""
	}
	return claudeprovider.LaunchPermissionMode(spec)
}

func (s *InternalServer) handleInternalCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider                   string `json:"provider"`
		Prompt                     string `json:"prompt"`
		Model                      string `json:"model,omitempty"`
		CWD                        string `json:"cwd"`
		PermissionMode             string `json:"permission_mode,omitempty"`
		DangerouslySkipPermissions bool   `json:"dangerously_skip_permissions,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
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

	sessionID := uuid.New().String()
	spec := provider.SessionSpec{
		SessionID:       sessionID,
		Prompt:          req.Prompt,
		Model:           req.Model,
		CWD:             req.CWD,
		PermissionMode:  req.PermissionMode,
		SkipPermissions: req.DangerouslySkipPermissions,
	}
	argv := builder(spec)

	handle, err := s.shared.Backend.Start(sessionID, req.CWD, argv)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to start terminal: %v", err), http.StatusInternalServerError)
		return
	}

	// Register the session immediately so the API can report it before the
	// agent's first hook arrives.
	event := "Launch"
	sess := &store.Session{
		SessionID: sessionID,
		Source:    "claude",
		CWD:       req.CWD,
		Status:    "starting",
		LastEvent: &event,
		Managed:   true,
	}
	if err := s.shared.DB.UpsertSession(sess); err != nil {
		log.Printf("create-session: register session %s: %v", sessionID, err)
	}
	// Record the mode the agent launched under, now rather than on the first
	// hook: a session evicted before it reports in would otherwise wake with an
	// empty column, and an empty column means "whatever the CLI defaults to" —
	// which is not what a Helios-launched session was started in.
	if mode := launchPermissionMode(req.Provider, spec); mode != "" {
		if err := s.shared.DB.UpdateSessionPermissionMode(sessionID, mode); err != nil {
			log.Printf("create-session: record permission mode for %s: %v", sessionID, err)
		}
	}

	// Watch for the workspace-trust dialog until the agent reports in.
	s.shared.Pending.Add(sessionID, req.CWD)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"session_id": sessionID,
		"terminal":   handle,
		"cwd":        req.CWD,
	})
}

// handleWrap binds a terminal the user started by hand — `helios wrap` — to a
// session record.
func (s *InternalServer) handleWrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle         string `json:"handle"`
		CWD            string `json:"cwd"`
		SessionID      string `json:"session_id"`
		PermissionMode string `json:"permission_mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		jsonError(w, "missing session_id", http.StatusBadRequest)
		return
	}

	event := "Wrap"
	sess := &store.Session{
		SessionID: req.SessionID,
		Source:    "claude",
		CWD:       req.CWD,
		Status:    "starting",
		LastEvent: &event,
		Managed:   true,
	}
	if err := s.shared.DB.UpsertSession(sess); err != nil {
		log.Printf("wrap: register session %s: %v", req.SessionID, err)
	}
	// Only a mode the user typed on the wrapped command is recorded. Wrap adds
	// none of its own, and a null column is what tells a later wake to leave the
	// flag off and let the CLI apply the same default it started under.
	if claudeprovider.ValidPermissionMode(req.PermissionMode) {
		if err := s.shared.DB.UpdateSessionPermissionMode(req.SessionID, req.PermissionMode); err != nil {
			log.Printf("wrap: record permission mode for %s: %v", req.SessionID, err)
		}
	}

	if err := s.shared.Backend.Adopt(req.SessionID, req.Handle, req.CWD); err != nil {
		log.Printf("wrap: adopt terminal for %s: %v", req.SessionID, err)
	}

	// Watch for the workspace-trust dialog until the agent reports in.
	s.shared.Pending.Add(req.SessionID, req.CWD)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *InternalServer) handleInternalPatchSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	var req struct {
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status != nil {
		s.shared.DB.UpdateSessionStatus(id, *req.Status, "ProcessExited")
		s.shared.SSE.Broadcast(SSEEvent{
			Type: "session_status",
			Data: map[string]interface{}{
				"session_id": id,
				"status":     *req.Status,
			},
		})
		log.Printf("session-patch: session %s status=%s", id, *req.Status)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

// ── Internal server session control handlers (delegate to Shared) ──

func (s *InternalServer) handleInternalSessionStop(w http.ResponseWriter, r *http.Request) {
	s.shared.stopSession(w, r.PathValue("id"))
}

func (s *InternalServer) handleInternalSessionTerminate(w http.ResponseWriter, r *http.Request) {
	s.shared.terminateSession(w, r.PathValue("id"))
}

func (s *InternalServer) handleInternalSessionResume(w http.ResponseWriter, r *http.Request) {
	s.shared.resumeSession(w, r.PathValue("id"))
}

func (s *InternalServer) handleInternalGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.shared.DB.GetAllSettings()
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to read settings: %v", err), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"settings": settings})
}

func (s *InternalServer) handleInternalUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(settings) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.shared.DB.SetSettings(settings); err != nil {
		jsonError(w, fmt.Sprintf("failed to save settings: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *InternalServer) handleInternalHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"internal_port": extractPort(s.httpServer.Addr),
		"sse_clients":   s.shared.SSE.ClientCount(),
		"pending":       s.shared.Mgr.PendingCount(),
		"terminal":      s.shared.Backend.Status(),
	})
}

// TunnelManager is set by the daemon after creating the tunnel manager.
var TunnelManager interface {
	Status() map[string]interface{}
	Start(provider string, customURL string, localPort int) (string, error)
	SetTailscaleMode(mode string)
	Stop() error
}

// OnTunnelConfigChanged is called when tunnel config should be persisted.
// Set by daemon to save tunnel provider to config.yaml.
var OnTunnelConfigChanged func(provider, customURL, tailscaleMode string)

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
		Provider      string `json:"provider"`
		CustomURL     string `json:"custom_url,omitempty"`
		LocalPort     int    `json:"local_port,omitempty"`
		TailscaleMode string `json:"tailscale_mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Provider == "tailscale" && req.TailscaleMode != "" {
		TunnelManager.SetTailscaleMode(req.TailscaleMode)
	}

	url, err := TunnelManager.Start(req.Provider, req.CustomURL, req.LocalPort)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to start tunnel: %v", err), http.StatusInternalServerError)
		return
	}

	// Persist tunnel config so it auto-starts on next daemon restart
	if OnTunnelConfigChanged != nil {
		OnTunnelConfigChanged(req.Provider, req.CustomURL, req.TailscaleMode)
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

// ==================== Reporter ====================

func (s *PublicServer) handleReporter(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sessionFilter := r.URL.Query().Get("session")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	listener := s.shared.Reporter.Subscribe(sessionFilter)
	defer s.shared.Reporter.Unsubscribe(listener)

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n := <-listener.Ch:
			data, _ := json.Marshal(n)
			fmt.Fprintf(w, "event: narration\ndata: %s\n\n", data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// ==================== Settings ====================

func (s *PublicServer) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.shared.DB.GetAllSettings()
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to read settings: %v", err), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"settings":    settings,
		"personas":    reporter.Personas,
		"event_types": provider.GetAllEventTypes(),
	})
}

func (s *PublicServer) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(settings) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.shared.DB.SetSettings(settings); err != nil {
		jsonError(w, fmt.Sprintf("failed to save settings: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		Provider                   string `json:"provider"`
		Prompt                     string `json:"prompt"`
		Model                      string `json:"model,omitempty"`
		CWD                        string `json:"cwd,omitempty"`
		PermissionMode             string `json:"permission_mode,omitempty"`
		DangerouslySkipPermissions bool   `json:"dangerously_skip_permissions,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
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

	sessionID := uuid.New().String()
	spec := provider.SessionSpec{
		SessionID:       sessionID,
		Prompt:          req.Prompt,
		Model:           req.Model,
		CWD:             req.CWD,
		PermissionMode:  req.PermissionMode,
		SkipPermissions: req.DangerouslySkipPermissions,
	}
	argv := builder(spec)

	handle, err := s.shared.Backend.Start(sessionID, req.CWD, argv)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to start terminal: %v", err), http.StatusInternalServerError)
		return
	}

	// Register the session immediately so the API can report it before the
	// agent's first hook arrives.
	event := "Launch"
	sess := &store.Session{
		SessionID: sessionID,
		Source:    "claude",
		CWD:       req.CWD,
		Status:    "starting",
		LastEvent: &event,
		Managed:   true,
	}
	if err := s.shared.DB.UpsertSession(sess); err != nil {
		log.Printf("create-session: register session %s: %v", sessionID, err)
	}
	// Record the mode the agent launched under, now rather than on the first
	// hook: a session evicted before it reports in would otherwise wake with an
	// empty column, and an empty column means "whatever the CLI defaults to" —
	// which is not what a Helios-launched session was started in.
	if mode := launchPermissionMode(req.Provider, spec); mode != "" {
		if err := s.shared.DB.UpdateSessionPermissionMode(sessionID, mode); err != nil {
			log.Printf("create-session: record permission mode for %s: %v", sessionID, err)
		}
	}

	// Watch for the workspace-trust dialog until the agent reports in.
	s.shared.Pending.Add(sessionID, req.CWD)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"session_id": sessionID,
		"terminal":   handle,
		"cwd":        req.CWD,
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
