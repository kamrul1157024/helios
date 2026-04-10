package server

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/store"
)

// ==================== Public Server API ====================

func (s *PublicServer) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	nType := r.URL.Query().Get("type")

	notifs, err := s.shared.Mgr.ListNotifications(status, nType)
	if err != nil {
		jsonError(w, "failed to list notifications", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"notifications": notifs,
	})
}

func (s *PublicServer) handleApproveNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/approve")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	if err := s.shared.Mgr.Resolve(id, "approved", source); err != nil {
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

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": "approved", "source": source},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Notification %s approved", id),
	})
}

func (s *PublicServer) handleDenyNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/deny")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	source := "browser"
	if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
		source = "device:" + kid
	}

	if err := s.shared.Mgr.Resolve(id, "denied", source); err != nil {
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

	s.shared.SSE.Broadcast(SSEEvent{
		Type: "notification_resolved",
		Data: map[string]string{"id": id, "action": "denied", "source": source},
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Notification %s denied", id),
	})
}

func (s *PublicServer) handleDismissNotification(w http.ResponseWriter, r *http.Request) {
	id := extractPathParam(r.URL.Path, "/api/notifications/", "/dismiss")
	if id == "" {
		jsonError(w, "missing notification id", http.StatusBadRequest)
		return
	}

	if err := s.shared.Mgr.Resolve(id, "dismissed", "browser"); err != nil {
		jsonError(w, "failed to dismiss", http.StatusInternalServerError)
		return
	}

	s.shared.SSE.Broadcast(SSEEvent{
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

func (s *PublicServer) handleBatchNotifications(w http.ResponseWriter, r *http.Request) {
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
		if err := s.shared.Mgr.Resolve(id, status, source); err != nil {
			result["success"] = false
			result["error"] = "already_resolved"
		} else {
			result["success"] = true
			s.shared.SSE.Broadcast(SSEEvent{
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

func (s *PublicServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"sse_clients": s.shared.SSE.ClientCount(),
		"pending":     s.shared.Mgr.PendingCount(),
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

// handlePair registers or updates a device. Called before login.
// The device sends its self-generated kid (UUID) and the public key derived from the QR seed.
// If another device already uses this public key, returns an error.
func (s *PublicServer) handlePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KID       string `json:"kid"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.KID == "" || req.PublicKey == "" {
		jsonError(w, "missing kid or public_key", http.StatusBadRequest)
		return
	}

	// Validate public key format (must be valid base64url-encoded 32-byte Ed25519 public key)
	if _, err := auth.PublicKeyFromBase64(req.PublicKey); err != nil {
		jsonError(w, "invalid public key format", http.StatusBadRequest)
		return
	}

	// Check if this key is already used by a different device
	existing, err := s.shared.DB.GetDeviceByPublicKey(req.PublicKey)
	if err != nil {
		jsonError(w, "failed to check key uniqueness", http.StatusInternalServerError)
		return
	}
	if existing != nil && existing.KID != req.KID {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{
			"success": false,
			"error":   "key_already_claimed",
			"message": "This QR code has already been used by another device. Generate a new QR from the terminal with: helios start",
		})
		return
	}

	// Create or update the device
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

	// Activate device if pending
	s.shared.DB.ActivateDevice(kid)
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

// ==================== Internal Server API ====================

func (s *InternalServer) handleInternalHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"internal_port": extractPort(s.httpServer.Addr),
		"sse_clients":   s.shared.SSE.ClientCount(),
		"pending":       s.shared.Mgr.PendingCount(),
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
	// Generate a keypair — no device record is created here.
	// The device will self-register via POST /api/auth/pair with its own kid.
	kp, err := auth.GenerateKeypair("")
	if err != nil {
		jsonError(w, "failed to generate keypair", http.StatusInternalServerError)
		return
	}

	// Build setup URL (key only, no kid — device generates its own)
	setupURL := ""
	if TunnelManager != nil {
		status := TunnelManager.Status()
		if url, ok := status["public_url"].(string); ok && url != "" {
			setupURL = fmt.Sprintf("%s/#/setup?key=%s", url, kp.PrivateKeyBase64())
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"key":       kp.PrivateKeyBase64(),
		"setup_url": setupURL,
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

	// Generate new keypair
	kp, err := auth.GenerateKeypair(req.KID)
	if err != nil {
		jsonError(w, "failed to generate keypair", http.StatusInternalServerError)
		return
	}

	// Update public key and reset to pending (will activate on next login)
	if err := s.shared.DB.RekeyDevice(req.KID, kp.PublicKeyBase64()); err != nil {
		jsonError(w, "failed to rekey device", http.StatusInternalServerError)
		return
	}

	// Build setup URL (key only, no kid)
	setupURL := ""
	if TunnelManager != nil {
		status := TunnelManager.Status()
		if url, ok := status["public_url"].(string); ok && url != "" {
			setupURL = fmt.Sprintf("%s/#/setup?key=%s", url, kp.PrivateKeyBase64())
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"kid":       req.KID,
		"key":       kp.PrivateKeyBase64(),
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

// handleAppDownload serves the APK — local file if available, otherwise redirects to GitHub release.
func (s *PublicServer) handleAppDownload(w http.ResponseWriter, r *http.Request) {
	// Try local APK first
	if APKPath != "" {
		if f, err := os.Open(APKPath); err == nil {
			defer f.Close()
			if stat, err := f.Stat(); err == nil {
				w.Header().Set("Content-Type", "application/vnd.android.package-archive")
				w.Header().Set("Content-Disposition", "attachment; filename=\"helios.apk\"")
				http.ServeContent(w, r, "helios.apk", stat.ModTime(), f)
				return
			}
		}
	}

	// Fall back to GitHub release
	if APKDownloadURL != "" {
		http.Redirect(w, r, APKDownloadURL, http.StatusFound)
		return
	}

	jsonError(w, "APK not available — build with: make apk", http.StatusNotFound)
}

// APKPath is set by daemon to the path of the local APK file.
var APKPath string

// APKDownloadURL is the GitHub release URL for the APK (pinned to version).
var APKDownloadURL = "https://github.com/kamrul1157024/helios/releases/download/v0.2.0/helios.apk"

// DMGDownloadURL is the GitHub release URL for the macOS DMG (pinned to version).
var DMGDownloadURL = "https://github.com/kamrul1157024/helios/releases/download/v0.2.0/helios.dmg"

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

