package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/push"
	"github.com/kamrul1157024/helios/internal/store"
)

// Shared holds shared dependencies between internal and public servers.
type Shared struct {
	DB     *store.Store
	Mgr    *notifications.Manager
	SSE    *SSEBroadcaster
	Pusher *push.Sender
}

// InternalServer handles hooks (Claude) and admin API (CLI).
// Binds to 127.0.0.1 only. No auth required.
type InternalServer struct {
	httpServer *http.Server
	shared     *Shared
}

// PublicServer handles the frontend, push API, and notification actions.
// Binds to 0.0.0.0, exposed via tunnel. Cookie-based JWT auth.
type PublicServer struct {
	httpServer *http.Server
	shared     *Shared
}

func NewShared(db *store.Store, mgr *notifications.Manager, pusher *push.Sender) *Shared {
	return &Shared{
		DB:     db,
		Mgr:    mgr,
		SSE:    NewSSEBroadcaster(),
		Pusher: pusher,
	}
}

// NewInternalServer creates the localhost-only server for hooks and admin API.
func NewInternalServer(port int, shared *Shared) *InternalServer {
	s := &InternalServer{shared: shared}

	mux := http.NewServeMux()

	// Hook endpoint (generic — dispatches by type, e.g. POST /hooks/claude/permission)
	mux.HandleFunc("POST /hooks/{hookType...}", s.handleHook)

	// Internal admin API (CLI — no auth, localhost only)
	mux.HandleFunc("GET /internal/health", s.handleInternalHealth)
	mux.HandleFunc("GET /internal/tunnel/status", s.handleTunnelStatus)
	mux.HandleFunc("POST /internal/tunnel/start", s.handleTunnelStart)
	mux.HandleFunc("POST /internal/tunnel/stop", s.handleTunnelStop)
	mux.HandleFunc("POST /internal/device/create", s.handleDeviceCreate)
	mux.HandleFunc("POST /internal/device/rekey", s.handleDeviceRekey)
	mux.HandleFunc("GET /internal/device/list", s.handleDeviceList)
	mux.HandleFunc("POST /internal/device/revoke", s.handleDeviceRevoke)
	mux.HandleFunc("GET /internal/logs", s.handleInternalLogs)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	return s
}

// NewPublicServer creates the tunnel-exposed server for API.
func NewPublicServer(port int, shared *Shared) *PublicServer {
	s := &PublicServer{shared: shared}

	mux := http.NewServeMux()

	// Landing page (no auth — download links, exact root path only)
	mux.HandleFunc("GET /{$}", handleLanding)

	// Public endpoints (no auth)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/pair", s.handlePair)

	// Auth-protected API endpoints
	cookieAuth := cookieAuthMiddleware(shared.DB)

	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("GET /api/push/vapid-public-key", s.handleVAPIDPublicKey)
	protectedMux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	protectedMux.HandleFunc("POST /api/notifications/batch", s.handleBatchNotifications)
	protectedMux.Handle("GET /api/events", shared.SSE)
	protectedMux.HandleFunc("GET /api/auth/devices", s.handleListDevices)
	protectedMux.HandleFunc("GET /api/auth/device/me", s.handleDeviceMe)
	protectedMux.HandleFunc("POST /api/auth/device/me", s.handleUpdateDeviceMe)
	protectedMux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	protectedMux.HandleFunc("POST /api/push/unsubscribe", s.handlePushUnsubscribe)
	protectedMux.HandleFunc("POST /api/device/logs", s.handleDeviceLogs)
	protectedMux.HandleFunc("GET /api/app/download", s.handleAppDownload)

	// Dynamic path handlers
	protectedMux.HandleFunc("/api/notifications/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/action"):
			s.handleNotificationAction(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/dismiss"):
			s.handleDismissNotification(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	protectedMux.HandleFunc("/api/auth/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			s.handleRevokeDevice(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	// Wire protected routes through cookie auth middleware
	mux.Handle("/api/", cookieAuth(protectedMux))

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),
		Handler: mux,
	}

	return s
}

func (s *InternalServer) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *InternalServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *PublicServer) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *PublicServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
