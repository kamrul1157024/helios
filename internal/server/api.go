package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
)

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	nType := r.URL.Query().Get("type")

	notifs, err := s.mgr.ListNotifications(status, nType)
	if err != nil {
		jsonError(w, "failed to list notifications", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"notifications": notifs,
	})
}

func (s *Server) handleApproveNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/approve")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	if err := s.mgr.Resolve(id, "approved", source); err != nil {
		if _, ok := err.(*store.AlreadyResolvedError); ok {
			jsonResponse(w, http.StatusGone, map[string]interface{}{
				"success": false,
				"error":   "already_resolved",
				"message": "This notification was already resolved",
			})
			return
		}
		jsonError(w, "failed to approve", http.StatusInternalServerError)
		return
	}

	s.sse.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": "approved", "source": source},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Notification %s approved", id),
	})
}

func (s *Server) handleDenyNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/deny")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	if err := s.mgr.Resolve(id, "denied", source); err != nil {
		if _, ok := err.(*store.AlreadyResolvedError); ok {
			jsonResponse(w, http.StatusGone, map[string]interface{}{
				"success": false,
				"error":   "already_resolved",
				"message": "This notification was already resolved",
			})
			return
		}
		jsonError(w, "failed to deny", http.StatusInternalServerError)
		return
	}

	s.sse.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": "denied", "source": source},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Notification %s denied", id),
	})
}

func (s *Server) handleDismissNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/dismiss")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	if err := s.mgr.Resolve(id, "dismissed", "browser"); err != nil {
		jsonError(w, "failed to dismiss", http.StatusInternalServerError)
		return
	}

	s.sse.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": "dismissed"},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

type batchRequest struct {
	NotificationIDs []string `json:"notification_ids"`
	Action          string   `json:"action"`
}

func (s *Server) handleBatchNotifications(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Action != "approve" && req.Action != "deny" {
		jsonError(w, "action must be 'approve' or 'deny'", http.StatusBadRequest)
		return
	}

	status := "approved"
	if req.Action == "deny" {
		status = "denied"
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	var results []map[string]interface{}
	for _, id := range req.NotificationIDs {
		result := map[string]interface{}{"id": id}
		if err := s.mgr.Resolve(id, status, source); err != nil {
			result["success"] = false
			result["error"] = "already_resolved"
		} else {
			result["success"] = true
			s.sse.Broadcast(SSEEvent{
				Type: "notification_resolved",
				Data: map[string]string{"id": id, "action": req.Action, "source": source},
			})
		}
		results = append(results, result)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"sse_clients": s.sse.ClientCount(),
		"pending":     s.mgr.PendingCount(),
	})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.db.ListDevices()
	if err != nil {
		jsonError(w, "failed to list devices", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"devices": devices,
	})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	kid := strings.TrimPrefix(r.URL.Path, "/api/auth/devices/")
	if kid == "" {
		jsonError(w, "missing device kid", http.StatusBadRequest)
		return
	}

	if err := s.db.RevokeDevice(kid); err != nil {
		jsonError(w, "failed to revoke device", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Device %s revoked", kid),
	})
}

func (s *Server) handleVerifyAuth(w http.ResponseWriter, r *http.Request) {
	// This endpoint just validates the JWT — if we get here, it's valid
	// (auth middleware already ran, or it's localhost)
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"valid": true,
	})
}

// Helpers

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
