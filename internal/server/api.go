package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
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

	handler := provider.ActionHandlerFor(notif.Type)
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

		handler := provider.ActionHandlerFor(notif.Type)
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
	caps := provider.CapabilitiesOf(sess.Source)
	sess.ComputePromptQueue(caps.PromptQueue)
}

func (s *PublicServer) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// Sessions a schedule started are left out of the ordinary list. Six agents
	// you started and forty the clock started is a sidebar that has stopped
	// being a list of your work; ?filter=jobs and ?schedule_id= put them back.
	//
	// The default lives here rather than in the store on purpose: the reaper,
	// the memory evictor and the MCP tools all call the unfiltered
	// ListSessions, and hiding scheduled sessions from them would mean agents
	// that are never reaped and never evicted.
	jobs := "exclude"
	scheduleID := r.URL.Query().Get("schedule_id")
	if r.URL.Query().Get("filter") == "jobs" || scheduleID != "" {
		jobs = "only"
	}

	sessions, err := s.shared.DB.SearchSessions(store.SessionQuery{
		Query:      r.URL.Query().Get("q"),
		Status:     r.URL.Query().Get("status"),
		Filter:     r.URL.Query().Get("filter"),
		CWD:        r.URL.Query().Get("cwd"),
		Grouped:    r.URL.Query().Get("grouped") == "1",
		GroupKey:   r.URL.Query().Get("group_key"),
		Jobs:       jobs,
		ScheduleID: scheduleID,
	})
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
		"host":     s.shared.hostStats(),
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
	pendingNotifs, _ := s.shared.DB.ListNotifications("", "pending", "")
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

// resolveTranscriptPath returns a transcript path that exists, or "" when the
// session has none to read.
//
// The recorded path can go stale: Claude Code keeps a transcript in a directory
// named after the session's cwd, so entering a git worktree moves the file out
// from under whatever was recorded when the session started. Sessions running
// under current hooks re-record the path themselves; this catches the ones that
// moved before, and writes the new path back so the walk happens once.
func (s *PublicServer) resolveTranscriptPath(session *store.Session) string {
	recorded := ""
	if session.TranscriptPath != nil {
		recorded = *session.TranscriptPath
	}
	if recorded != "" {
		if _, err := os.Stat(recorded); err == nil {
			return recorded
		}
	}
	t := provider.TranscriberFor(session.Source)
	if t == nil {
		return ""
	}
	found := t.LocateTranscript(session.SessionID)
	if found == "" {
		return ""
	}
	if err := s.shared.DB.UpdateSessionTranscriptPath(session.SessionID, found); err != nil {
		log.Printf("api: record relocated transcript for %s: %v", session.SessionID, err)
	}
	return found
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

	// The transcript is the agent's own log, in the agent's own format, so the
	// provider that wrote it is the only thing that can read it. A session
	// whose provider this build does not have has an unreadable log, which is
	// an empty transcript rather than an error.
	transcriptPath := s.resolveTranscriptPath(session)
	transcriber := provider.TranscriberFor(session.Source)
	if transcriptPath == "" || transcriber == nil {
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
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}

	// after_seq asks for what has arrived since the caller last looked, which
	// is what a client watching a running session wants on every event. The
	// epoch it quotes says which parse its seq numbers came from.
	var result *transcript.TranscriptResult
	if a := r.URL.Query().Get("after_seq"); a != "" {
		afterSeq := -1
		fmt.Sscanf(a, "%d", &afterSeq)
		result, err = transcript.Delta(transcriber.ParseLine, transcriptPath, r.URL.Query().Get("epoch"), afterSeq, limit)
	} else {
		result, err = transcript.Page(transcriber.ParseLine, transcriptPath, limit, offset)
	}
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

// handleSessionTouch records that a human is looking at this session.
//
// Deliberately cheap and fire-and-forget: clients call it on selection and on a
// heartbeat, and a failure means one missed sample rather than anything the
// user should hear about.
func (s *PublicServer) handleSessionTouch(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/touch")
	if err := s.shared.DB.TouchSession(id); err != nil {
		jsonError(w, "failed to record interaction", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

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

	result, err := s.shared.SendPrompt(id, req.Message)
	switch {
	case errors.Is(err, ErrSessionBusy), errors.Is(err, ErrSessionTerminated):
		// The session's state, not a failure of the request: reported with the
		// code the apps already switch on.
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
	case err != nil:
		jsonError(w, err.Error(), StatusOf(err))
	case result.Queued:
		jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "queued": true})
	default:
		jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "resumed": result.Resumed})
	}
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
	session, err := sh.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	// The vocabulary belongs to the provider, so the provider validates it.
	// A provider with no modes at all rejects every value, which is the right
	// answer: there is nothing to set.
	if provider.ModerFor(session.Source) == nil {
		jsonError(w, fmt.Sprintf("provider %s has no permission modes", session.Source), http.StatusBadRequest)
		return
	}
	if !provider.ValidMode(session.Source, mode) {
		jsonError(w, fmt.Sprintf("unknown permission mode: %q", mode), http.StatusBadRequest)
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

	sh.EndSession(id)

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
		Pinned *bool   `json:"pinned"`
		Title  *string `json:"title"`
		Status *string `json:"status"`
		Group  *string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Before anything else is written: the store refuses a key that names no
	// group, and a rejected grouping should not leave a half-applied patch.
	if req.Group != nil {
		if err := s.shared.DB.SetSessionGroup(id, *req.Group); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	pinned := session.Pinned
	if req.Pinned != nil {
		pinned = *req.Pinned
	}

	if err := s.shared.DB.UpdateSessionPinned(id, pinned); err != nil {
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
		},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"pinned":  pinned,
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

	// The shells opened beside it have no owner once the session is gone, and
	// nothing left to list them: they would run until the machine rebooted.
	if runner, ok := s.shared.shells(); ok {
		runner.KillShells(id)
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

	transcriptPath := s.resolveTranscriptPath(session)

	notify := func(eventType string, data interface{}) {
		s.shared.SSE.Broadcast(SSEEvent{Type: eventType, Data: data})
	}

	// Not the hook path: that one leaves a titled session alone, so asking it to
	// rename a session did nothing at all. Waits for the answer, so the caller
	// hears whether the name changed rather than being told "success" either way.
	titler := provider.TitlerFor(session.Source)
	if titler == nil {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"success": false, "error": "no_titler",
			"message": fmt.Sprintf("provider %s cannot name a session", session.Source),
		})
		return
	}
	title := titler.Title(s.shared.DB, id, session.CWD, transcriptPath, notify)
	if title == "" {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "no_title",
			"message": "the model did not return a usable title",
		})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "title": title})
}

// handleSessionOrder records a hand-arranged order for the session list.
//
// The whole arrangement in one request: dragging one card shifts every card it
// passed, and the client knows the result it wants. Sending positions one at a
// time would leave the list half-reordered if the second call failed.
func (s *PublicServer) handleSessionOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Order) == 0 {
		jsonError(w, "missing order", http.StatusBadRequest)
		return
	}

	if err := s.shared.DB.SetSessionOrder(req.Order); err != nil {
		log.Printf("session-order: %v", err)
		jsonError(w, "failed to save order", http.StatusInternalServerError)
		return
	}

	// Every client is looking at the same list, so they all need to hear about
	// it — the one that did the dragging has already moved the card itself.
	s.shared.SSE.Broadcast(SSEEvent{Type: "session_updated", Data: map[string]interface{}{
		"reordered": len(req.Order),
	}})

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
	// The same rule the apps get: what the clock started is its own list. The
	// TUI's session view is a sidebar too.
	jobs := "exclude"
	scheduleID := r.URL.Query().Get("schedule_id")
	if r.URL.Query().Get("filter") == "jobs" || scheduleID != "" {
		jobs = "only"
	}
	sessions, err := s.shared.DB.SearchSessions(store.SessionQuery{
		Jobs:       jobs,
		ScheduleID: scheduleID,
	})
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

	// The CLI's own directory is what an omitted cwd means here, because the
	// caller is a person standing in one. The apps have no directory to stand
	// in and default to home instead.
	if req.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			jsonError(w, "failed to get cwd", http.StatusInternalServerError)
			return
		}
		req.CWD = cwd
	}

	started, err := s.shared.StartSession(NewSession{
		Provider:        req.Provider,
		Prompt:          req.Prompt,
		Model:           req.Model,
		CWD:             req.CWD,
		PermissionMode:  req.PermissionMode,
		SkipPermissions: req.DangerouslySkipPermissions,
	})
	if err != nil {
		jsonError(w, err.Error(), StatusOf(err))
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"session_id": started.SessionID,
		"terminal":   started.Terminal,
		"cwd":        started.CWD,
	})
}

// startTerminal launches a provider's argv, with its environment when the
// backend can carry one.
//
// A provider needs Env only when its agent cannot be told which helios session
// it belongs to any other way, so a backend without EnvStarter is not broken —
// it just cannot host that provider's sessions.
func startTerminal(b backend.Backend, sessionID, cwd string, launch provider.Launch) (string, error) {
	if len(launch.Env) > 0 {
		es, ok := b.(backend.EnvStarter)
		if !ok {
			// Refused rather than started without it. A provider only asks for
			// an environment when its hooks cannot name the session any other
			// way, so starting anyway produces an agent that runs and is never
			// heard from — the failure this change exists to remove.
			return "", fmt.Errorf("backend %s cannot set the environment this provider needs", b.Name())
		}
		return es.StartWithEnv(sessionID, cwd, launch.Argv, launch.Env)
	}
	return b.Start(sessionID, cwd, launch.Argv)
}

// handleWrap binds a terminal the user started by hand — `helios wrap` — to a
// session record.
func (s *InternalServer) handleWrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle         string `json:"handle"`
		CWD            string `json:"cwd"`
		SessionID      string `json:"session_id"`
		Provider       string `json:"provider,omitempty"`
		PermissionMode string `json:"permission_mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		jsonError(w, "missing session_id", http.StatusBadRequest)
		return
	}
	// An older `helios wrap` sends no provider. Claude is the right guess for
	// those: it is the only one that wrapped before this field existed.
	if req.Provider == "" {
		req.Provider = "claude"
	}

	event := "Wrap"
	sess := &store.Session{
		SessionID: req.SessionID,
		Source:    req.Provider,
		CWD:       req.CWD,
		Status:    "starting",
		LastEvent: &event,
	}
	if err := s.shared.DB.UpsertSession(sess); err != nil {
		log.Printf("wrap: register session %s: %v", req.SessionID, err)
	}
	// Only a mode the user typed on the wrapped command is recorded. Wrap adds
	// none of its own, and a null column is what tells a later wake to leave the
	// flag off and let the CLI apply the same default it started under.
	if provider.ValidMode(req.Provider, req.PermissionMode) {
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

// PublicBind is the interface the public server is listening on. Set by the
// daemon at startup; the bind is fixed for the life of the process.
var PublicBind string

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

	resp := map[string]interface{}{"public_url": url}
	if restartRequiredForBind(req.Provider, PublicBind) {
		resp["restart_required"] = true
		resp["message"] = fmt.Sprintf("public API is bound to %s; restart the daemon so it listens on the LAN", PublicBind)
	}
	jsonResponse(w, http.StatusOK, resp)
}

// restartRequiredForBind reports whether the running listener can serve the URL
// the provider just handed out. The bind is chosen once at startup from the
// configured provider, so switching to "local" mid-run yields a LAN URL that
// nothing is listening on.
func restartRequiredForBind(provider, bind string) bool {
	return provider == "local" && (bind == "127.0.0.1" || bind == "localhost" || bind == "::1")
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

// Download URLs for the packaged clients, served on the landing page. Each one
// points at the latest release rather than a tag, which is why the release
// workflow strips the version out of the asset names.
var (
	APKDownloadURL           = releaseAsset("helios.apk")
	MacArm64DownloadURL      = releaseAsset("helios-desktop-macos-arm64.dmg")
	MacIntelDownloadURL      = releaseAsset("helios-desktop-macos-x64.dmg")
	LinuxAppImageDownloadURL = releaseAsset("helios-desktop-linux-x86_64.AppImage")
	LinuxDebDownloadURL      = releaseAsset("helios-desktop-linux-amd64.deb")
)

func releaseAsset(name string) string {
	return "https://github.com/kamrul1157024/helios/releases/latest/download/" + name
}

// ==================== Commands ====================

func (s *PublicServer) handleListCommands(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"commands": provider.Commands(),
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
		"event_types": provider.EventTypes(),
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
	lister := provider.ModelListerFor(providerID)
	if lister == nil {
		return nil, fmt.Errorf("unknown provider: %s", providerID)
	}
	models, err := lister.Models()
	if err != nil {
		return nil, err
	}
	modelCache.data[providerID] = models
	modelCache.fetchedAt[providerID] = time.Now()
	return models, nil
}

// providerView is what a client needs to render a provider: its identity, what
// it can do, and the mode vocabulary that belongs to it rather than to them.
type providerView struct {
	provider.Info
	Capabilities    provider.Capabilities `json:"capabilities"`
	PermissionModes []string              `json:"permission_modes,omitempty"`
	// Readiness is whether a session started now would work. Clients that
	// offer session creation filter on it, so a user is not given a choice
	// that fails; the setup surfaces show everything and use Blocker to say
	// what is missing.
	provider.Readiness
}

func (s *PublicServer) handleListProviders(w http.ResponseWriter, r *http.Request) {
	views := []providerView{}
	for _, p := range provider.All() {
		id := p.Info().ID
		views = append(views, providerView{
			Info:            p.Info(),
			Capabilities:    provider.CapabilitiesOf(id),
			PermissionModes: provider.PermissionModes(id),
			Readiness:       provider.ReadinessFor(id),
		})
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"providers": views})
}

// handleNotificationTypes serves the catalogue the clients used to hardcode.
//
// Each client held its own copy of the type list, its labels, which types
// block and their default alert state — four copies each — so adding a
// provider meant editing all of them and forgetting one was silent. This is
// the same fix permission_modes already had.
func (s *PublicServer) handleNotificationTypes(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"notification_types": provider.NotificationTypes(),
	})
}

// handleHooksHealth reports whether each provider's hooks will actually run.
//
// Worth its own endpoint because the interesting failure is silent: Codex
// reads an untrusted hook table, declines to run it, and says nothing, so the
// daemon receives no events and every session sits at "starting".
func (s *PublicServer) handleHooksHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]provider.HookHealth{}
	for _, p := range provider.All() {
		id := p.Info().ID
		if inst := provider.InstallerFor(id); inst != nil {
			health[id] = inst.HookHealth()
		}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"hooks": health})
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

	// An app has no working directory of its own, so an omitted cwd is home.
	// The CLI, which does have one, uses that instead.
	if req.CWD == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, "failed to determine home directory", http.StatusInternalServerError)
			return
		}
		req.CWD = home
	}

	started, err := s.shared.StartSession(NewSession{
		Provider:        req.Provider,
		Prompt:          req.Prompt,
		Model:           req.Model,
		CWD:             req.CWD,
		PermissionMode:  req.PermissionMode,
		SkipPermissions: req.DangerouslySkipPermissions,
	})
	if err != nil {
		jsonError(w, err.Error(), StatusOf(err))
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"session_id": started.SessionID,
		"terminal":   started.Terminal,
		"cwd":        started.CWD,
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
