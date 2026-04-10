package server

import (
	"encoding/json"
	"net/http"

	"github.com/kamrul1157024/helios/internal/store"
)

type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var req pushSubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		jsonError(w, "missing subscription fields", http.StatusBadRequest)
		return
	}

	deviceKID := ""
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		deviceKID = kid
	}

	sub := &store.PushSubscription{
		Endpoint:  req.Endpoint,
		P256dh:    req.Keys.P256dh,
		Auth:      req.Keys.Auth,
		DeviceKID: deviceKID,
	}

	if err := s.db.CreatePushSubscription(sub); err != nil {
		jsonError(w, "failed to save subscription", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Push subscription registered",
	})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.db.DeletePushSubscription(req.Endpoint); err != nil {
		jsonError(w, "failed to remove subscription", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func (s *Server) handleVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.pusher == nil {
		jsonError(w, "push not configured", http.StatusServiceUnavailable)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"public_key": s.pusher.VAPIDPublicKey(),
	})
}
